// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/runtime/atomic"
	"internal/runtime/gc"
	"internal/runtime/sys"
	"unsafe"
)

// Per-thread (in Go, per-P) cache for small objects.
// This includes a small object cache and local allocation stats.
// No locking needed because it is per-thread (per-P).
//
// mcaches are allocated from non-GC'd memory, so any heap pointers
// must be specially handled.
type mcache struct {
	_ sys.NotInHeap

	// The following members are accessed on every malloc,
	// so they are grouped here for better caching.
	nextSample  int64   // trigger heap sample after allocating this many bytes
	memProfRate int     // cached mem profile rate, used to detect changes
	scanAlloc   uintptr // bytes of scannable heap allocated

	// Allocator cache for tiny objects w/o pointers.
	// See "Tiny allocator" comment in malloc.go.

	// tiny points to the beginning of the current tiny block, or
	// nil if there is no current tiny block.
	//
	// tiny is a heap pointer. Since mcache is in non-GC'd memory,
	// we handle it by clearing it in releaseAll during mark
	// termination.
	//
	// tinyAllocs is the number of tiny allocations performed
	// by the P that owns this mcache.
	tiny       uintptr
	tinyoffset uintptr
	tinyAllocs uintptr

	// The rest is not accessed on every malloc.

	// alloc contains spans to allocate from, indexed by spanClass.
	alloc [numSpanClasses]*mspan

	// TODO(thepudds): better to interleave alloc and reusableListsNoScan/reusableListsScan so that
	// a single mallocgc call can often access both in the same cache line for a given spanClass.
	// It's not interleaved right now in part to have slightly smaller diff, and might be
	// negligible effect on current microbenchmarks.

	// reusableNoscan contains linked lists of reusable noscan heap objects, indexed by spanClass.
	// The next pointers are stored in the first word of the heap objects.
	reusableListsNoScan reusableListsNoScan
	// reusableListsScan contains reusableLinks forming chunked linked lists of
	// reusable scan objects, indexed by spanClass.
	reusableListsScan reusableListsScan
	// reusableCountsScan contains counts of reusable scan objects, indexed by spanClass.
	reusableCountsScan [gc.NumSizeClasses]int8
	// reusableLinkCache is a cache of *reusableLink not currently in use.
	// This helps reduce use of fixalloc..
	reusableLinkCache *reusableLink

	// Counts of objects freed by freegc and of allocations satisfied by
	// reusing them, along with their slot sizes in bytes. Flushed to
	// memstats.heapStats in releaseAll.
	explicitFrees      uintptr
	explicitFreeBytes  uintptr
	explicitReuses     uintptr
	explicitReuseBytes uintptr

	// freegcCheck records frees for GODEBUG=freegccheck. See freegccheck.go.
	freegcCheck *freegcCheckBuf

	stackcache [_NumStackOrders]stackfreelist

	// flushGen indicates the sweepgen during which this mcache
	// was last flushed. If flushGen != mheap_.sweepgen, the spans
	// in this mcache are stale and need to be flushed so they
	// can be swept. This is done in acquirep.
	flushGen atomic.Uint32
}

// A gclink is a node in a linked list of blocks, like mlink,
// but it is opaque to the garbage collector.
// The GC does not trace the pointers during collection,
// and the compiler does not emit write barriers for assignments
// of gclinkptr values. Code should store references to gclinks
// as gclinkptr, not as *gclink.
type gclink struct {
	next gclinkptr
}

// A gclinkptr is a pointer to a gclink, but it is opaque
// to the garbage collector.
type gclinkptr uintptr

// ptr returns the *gclink form of p.
// The result should be used for accessing fields, not stored
// in other data structures.
func (p gclinkptr) ptr() *gclink {
	return (*gclink)(unsafe.Pointer(p))
}

type stackfreelist struct {
	list gclinkptr // linked list of free stacks
	size uintptr   // total size of stacks in list
}

// dummy mspan that contains no free objects.
var emptymspan mspan

func allocmcache() *mcache {
	var c *mcache
	systemstack(func() {
		lock(&mheap_.lock)
		c = (*mcache)(mheap_.cachealloc.alloc())
		c.flushGen.Store(mheap_.sweepgen)
		unlock(&mheap_.lock)
	})
	for i := range c.alloc {
		c.alloc[i] = &emptymspan
	}
	c.nextSample = nextSample()

	return c
}

// freemcache releases resources associated with this
// mcache and puts the object onto a free list.
//
// In some cases there is no way to simply release
// resources, such as statistics, so donate them to
// a different mcache (the recipient).
func freemcache(c *mcache) {
	systemstack(func() {
		c.releaseAll()
		stackcache_clear(c)
		c.freeReusableLinks()

		// NOTE(rsc,rlh): If gcworkbuffree comes back, we need to coordinate
		// with the stealing of gcworkbufs during garbage collection to avoid
		// a race where the workbuf is double-freed.
		// gcworkbuffree(c.gcworkbuf)
		lock(&mheap_.lock)
		mheap_.cachealloc.free(unsafe.Pointer(c))
		unlock(&mheap_.lock)
	})
}

// getMCache is a convenience function which tries to obtain an mcache.
//
// Returns nil if we're not bootstrapping or we don't have a P. The caller's
// P must not change, so we must be in a non-preemptible state.
func getMCache(mp *m) *mcache {
	// Grab the mcache, since that's where stats live.
	pp := mp.p.ptr()
	var c *mcache
	if pp == nil {
		// We will be called without a P while bootstrapping,
		// in which case we use mcache0, which is set in mallocinit.
		// mcache0 is cleared when bootstrapping is complete,
		// by procresize.
		c = mcache0
	} else {
		c = pp.mcache
	}
	return c
}

// refill acquires a new span of span class spc for c. This span will
// have at least one free object. The current span in c must be full.
//
// Must run in a non-preemptible context since otherwise the owner of
// c could change.
func (c *mcache) refill(spc spanClass) {
	// Return the current cached span to the central lists.
	s := c.alloc[spc]

	if s.allocCount != s.nelems {
		throw("refill of span with free space remaining")
	}

	// Also check for unexpected unused reusable objects if freegc is enabled.
	if runtimeFreegcEnabled {
		c.refillCheckUnusedReusables(s, spc)
	}

	if s != &emptymspan {
		// Mark this span as no longer cached.
		if s.sweepgen != mheap_.sweepgen+3 {
			throw("bad sweepgen in refill")
		}
		mheap_.central[spc].mcentral.uncacheSpan(s)

		// Count up how many slots were used and record it.
		stats := memstats.heapStats.acquire()
		slotsUsed := int64(s.allocCount) - int64(s.allocCountBeforeCache)
		atomic.Xadd64(&stats.smallAllocCount[spc.sizeclass()], slotsUsed)

		// Flush tinyAllocs.
		if spc == tinySpanClass {
			atomic.Xadd64(&stats.tinyAllocCount, int64(c.tinyAllocs))
			c.tinyAllocs = 0
		}
		memstats.heapStats.release()

		// Count the allocs in inconsistent, internal stats.
		bytesAllocated := slotsUsed * int64(s.elemsize)
		gcController.totalAlloc.Add(bytesAllocated)

		// Clear the second allocCount just to be safe.
		s.allocCountBeforeCache = 0
	}

	// Get a new cached span from the central lists.
	s = mheap_.central[spc].mcentral.cacheSpan()
	if s == nil {
		throw("out of memory")
	}

	if s.allocCount == s.nelems {
		throw("span has no free space")
	}

	// Indicate that this span is cached and prevent asynchronous
	// sweeping in the next sweep phase.
	s.sweepgen = mheap_.sweepgen + 3

	// Store the current alloc count for accounting later.
	s.allocCountBeforeCache = s.allocCount

	// Update heapLive and flush scanAlloc.
	//
	// We have not yet allocated anything new into the span, but we
	// assume that all of its slots will get used, so this makes
	// heapLive an overestimate.
	//
	// When the span gets uncached, we'll fix up this overestimate
	// if necessary (see releaseAll).
	//
	// We pick an overestimate here because an underestimate leads
	// the pacer to believe that it's in better shape than it is,
	// which appears to lead to more memory used. See #53738 for
	// more details.
	usedBytes := uintptr(s.allocCount) * s.elemsize
	gcController.update(int64(s.npages*pageSize)-int64(usedBytes), int64(c.scanAlloc))
	c.scanAlloc = 0

	c.alloc[spc] = s
}

// refillCheckUnusedReusables checks if there are any unexpected
// reusable objects present when refilling a span.
func (c *mcache) refillCheckUnusedReusables(s *mspan, spc spanClass) {
	// There are some cases unused reusable object are expected
	// when a refill is triggered.
	skipUnusedReusablesCheck := false
	switch {
	case writeBarrier.enabled:
		// When the write barrier is enabled, we might have some leftover
		// reusable objects. This is fine, but means we should skip
		// the unused reusable sanity check below.
		skipUnusedReusablesCheck = true
	case spc == tinySpanClass:
		// mallocgcTiny uses 16 byte slots from spc==5, but mallocgcTiny does
		// not reuse reusable heap objects, so mallocgcTiny can trigger a refill
		// for normal allocations while there are still reusable heap objects present.
		// This is also fine.
		skipUnusedReusablesCheck = true
	case !spc.noscan() && heapBitsInSpan(s.elemsize):
		// Similarly, be lenient on skipping the sanity check for scan objects
		// with heap bits in the span because we might have triggered a normal alloc
		// path after failing to find a matching reusable heap object based on
		// heap bits matching (and hence can need a refill with reusable objects
		// still present).
		skipUnusedReusablesCheck = true
	}

	if !skipUnusedReusablesCheck {
		if spc.noscan() && c.reusableListsNoScan.head(spc) != 0 {
			throw("unexpected refill of span with reusable noscan objects on free list")
		}
		if !spc.noscan() && c.reusableListsScan.head(spc) != nil {
			rl := c.reusableListsScan.head(spc)
			if rl.len > 0 {
				throw("unexpected refill of span with reusable scan objects on free list")
			}
		}
	}
}

// allocLarge allocates a span for a large object.
func (c *mcache) allocLarge(size uintptr, noscan bool) *mspan {
	if size+pageSize < size {
		throw("out of memory")
	}
	npages := size >> gc.PageShift
	if size&pageMask != 0 {
		npages++
	}

	// Deduct credit for this span allocation and sweep if
	// necessary. mHeap_Alloc will also sweep npages, so this only
	// pays the debt down to npage pages.
	deductSweepCredit(npages*pageSize, npages)

	spc := makeSpanClass(0, noscan)
	s := mheap_.alloc(npages, spc)
	if s == nil {
		throw("out of memory")
	}

	// Count the alloc in consistent, external stats.
	stats := memstats.heapStats.acquire()
	atomic.Xadd64(&stats.largeAlloc, int64(npages*pageSize))
	atomic.Xadd64(&stats.largeAllocCount, 1)
	memstats.heapStats.release()

	// Count the alloc in inconsistent, internal stats.
	gcController.totalAlloc.Add(int64(npages * pageSize))

	// Update heapLive.
	gcController.update(int64(s.npages*pageSize), 0)

	// Put the large span in the mcentral swept list so that it's
	// visible to the background sweeper.
	mheap_.central[spc].mcentral.fullSwept(mheap_.sweepgen).push(s)

	// Adjust s.limit down to the object-containing part of the span.
	//
	// This is just to create a slightly tighter bound on the limit.
	// It's totally OK if the garbage collector, in particular
	// conservative scanning, can temporarily observes an inflated
	// limit. It will simply mark the whole object or just skip it
	// since we're in the mark phase anyway.
	s.limit = s.base() + size
	s.initHeapBits()
	return s
}

func (c *mcache) releaseAll() {
	// Take this opportunity to flush scanAlloc.
	scanAlloc := int64(c.scanAlloc)
	c.scanAlloc = 0

	sg := mheap_.sweepgen
	dHeapLive := int64(0)
	for i := range c.alloc {
		s := c.alloc[i]
		if s != &emptymspan {
			slotsUsed := int64(s.allocCount) - int64(s.allocCountBeforeCache)
			s.allocCountBeforeCache = 0

			// Adjust smallAllocCount for whatever was allocated.
			stats := memstats.heapStats.acquire()
			atomic.Xadd64(&stats.smallAllocCount[spanClass(i).sizeclass()], slotsUsed)
			memstats.heapStats.release()

			// Adjust the actual allocs in inconsistent, internal stats.
			// We assumed earlier that the full span gets allocated.
			gcController.totalAlloc.Add(slotsUsed * int64(s.elemsize))

			if s.sweepgen != sg+1 {
				// refill conservatively counted unallocated slots in gcController.heapLive.
				// Undo this.
				//
				// If this span was cached before sweep, then gcController.heapLive was totally
				// recomputed since caching this span, so we don't do this for stale spans.
				dHeapLive -= int64(s.nelems-s.allocCount) * int64(s.elemsize)
			}

			// Release the span to the mcentral.
			mheap_.central[i].mcentral.uncacheSpan(s)
			c.alloc[i] = &emptymspan
		}
	}
	// Clear tinyalloc pool.
	c.tiny = 0
	c.tinyoffset = 0

	// Flush tinyAllocs and the explicit free and reuse counts (see countExplicitReuse).
	stats := memstats.heapStats.acquire()
	atomic.Xadd64(&stats.tinyAllocCount, int64(c.tinyAllocs))
	c.tinyAllocs = 0
	if c.explicitFrees != 0 || c.explicitReuses != 0 {
		atomic.Xadd64(&stats.explicitFreeCount, int64(c.explicitFrees))
		atomic.Xadd64(&stats.explicitFree, int64(c.explicitFreeBytes))
		atomic.Xadd64(&stats.explicitReuseCount, int64(c.explicitReuses))
		atomic.Xadd64(&stats.explicitReuse, int64(c.explicitReuseBytes))
		c.explicitFrees, c.explicitFreeBytes = 0, 0
		c.explicitReuses, c.explicitReuseBytes = 0, 0
	}
	memstats.heapStats.release()

	// Clear the reusable linked lists.
	c.dropReusableLists()

	// Update heapLive and heapScan.
	gcController.update(dHeapLive, scanAlloc)
}

// prepareForSweep flushes c if the system has entered a new sweep phase
// since c was populated. This must happen between the sweep phase
// starting and the first allocation from c.
func (c *mcache) prepareForSweep() {
	// Alternatively, instead of making sure we do this on every P
	// between starting the world and allocating on that P, we
	// could leave allocate-black on, allow allocation to continue
	// as usual, use a ragged barrier at the beginning of sweep to
	// ensure all cached spans are swept, and then disable
	// allocate-black. However, with this approach it's difficult
	// to avoid spilling mark bits into the *next* GC cycle.
	sg := mheap_.sweepgen
	flushGen := c.flushGen.Load()
	if flushGen == sg {
		return
	} else if flushGen != sg-2 {
		println("bad flushGen", flushGen, "in prepareForSweep; sweepgen", sg)
		throw("bad flushGen")
	}
	c.releaseAll()
	stackcache_clear(c)
	c.flushGen.Store(mheap_.sweepgen) // Synchronizes with gcStart
}

// TODO(thepudds): for tracking the set of reusable objects, we currently have
// two parallel approaches:
//
// 1. For noscan objects: a linked list of heap object, with the next pointers stored
//    in the first word of the formerly live heap objects.
//
// 2. For scan objects: a chunked linked list, with N pointers to heap objects per chunk
//    and the chunks linked together.
//
// The first approach is simpler and likely better performance. Importantly, it also uses
// zero extra memory, and we do not need any cap on how many noscan objects we track.
//
// However, we don't use the first approach for scan objects because a linked list for
// an mspan with types that mostly have user pointers in the first slot
// might have a performance hiccup where the GC could examine one of the objects
// in the free list (for example via a queued pointer during mark, or maybe an unlucky
// conservative scan), and then the GC might then walk and mark the ~complete list of objects
// in the free list by following the next pointers from that (otherwise dead) object,
// while thinking the next pointers are valid user pointers.
//
// We have a few options we can do for scan objects, but mostly that is being left for future work.

// reusableListsNoScan is a set of linked lists of reusable noscan objects,
// indexed by span class. The next pointers are stored in the first word
// of the heap objects.
//
// The type has some simple list manipulation methods (which are simple including to
// help with inlining on the non-reuse fast path in mallocgc).
type reusableListsNoScan struct {
	_     sys.NotInHeap
	lists [gc.NumSizeClasses]gclinkptr
}

func (r *reusableListsNoScan) head(spc spanClass) gclinkptr {
	if doubleCheckReusable && !spc.noscan() {
		throw("reusablesNoScan.head called for scan spanClass")
	}
	return r.lists[spc.sizeclass()]
}

func (r *reusableListsNoScan) push(spc spanClass, p gclinkptr) {
	p.ptr().next = r.head(spc)
	r.lists[spc.sizeclass()] = p
}

func (r *reusableListsNoScan) pop(spc spanClass) gclinkptr {
	p := r.head(spc)
	if p != 0 {
		r.lists[spc.sizeclass()] = p.ptr().next
	}
	return p
}

// reusableListsScan is a set of chunked linked lists of reusable scan objects,
// indexed by span class.
//
// The type has some simple list manipulation methods (which are simple including to
// help with inlining on the non-reuse fast path in mallocgc).
type reusableListsScan struct {
	_     sys.NotInHeap
	lists [gc.NumSizeClasses]*reusableLink
}

func (r *reusableListsScan) head(spc spanClass) *reusableLink {
	if doubleCheckReusable && spc.noscan() {
		throw("reusablesScan.head called for noscan spanClass")
	}
	return r.lists[spc.sizeclass()]
}

func (r *reusableListsScan) push(spc spanClass, p *reusableLink) {
	p.next = r.head(spc)
	r.lists[spc.sizeclass()] = p
}

func (r *reusableListsScan) pop(spc spanClass) *reusableLink {
	p := r.head(spc)
	if p != nil {
		r.lists[spc.sizeclass()] = p.next
	}
	return p
}

// maxReusableLinksPerSpanClass is the maximum number of reusableLink
// we will keep track of per scan span class in an mcache.
// The 2 here means we track at most 28 reusable objects
// for heap objects with pointer. We will drop further reusable objects
// for that span class.
//
// This is very low currently to limit memory usage and also to limit
// the cost of clearing out reusable links during releaseAll, while
// still allowing us to see the performance of for example free/reuse
// in a cycle (which in its most basic form needs just one reusableLink).
const maxReusableLinksPerSpanClass = 2

// reusableLink is a node in an chunked linked list of reusable objects,
// currently only used for scan objects.
type reusableLink struct {
	// NOTE: if adding any fields here, allocReusableLink may
	// need to be updated to zero those fields.
	_ sys.NotInHeap
	// next points to the next reusableLink in the list.
	next *reusableLink
	// len is the number of reusable objects in this link.
	len int32
	// ptrs contains pointers to reusable objects.
	// The pointer is to the base of the heap object.
	// The 14 here gives 128 bytes today on 64-bit systems,
	// or usually two adjacent cache lines given these are aligned.
	ptrs [14]uintptr
}

func (c *mcache) allocReusableLink() *reusableLink {
	var rl *reusableLink
	if c.reusableLinkCache != nil {
		// Pop from the mcache's free list of reusableLinks.
		rl = c.reusableLinkCache
		c.reusableLinkCache = rl.next
	} else {
		systemstack(func() {
			lock(&mheap_.reusableLinkLock)
			rl = (*reusableLink)(mheap_.reusableLinkAlloc.alloc())
			unlock(&mheap_.reusableLinkLock)
		})
	}
	// TODO(thepudds): do a general review -- double-check we conservatively clearing next pointers (e.g., after popping).
	rl.next = nil
	rl.len = 0
	return rl
}

// freeReusableLinks returns all of an mcache's reusableLinks back
// to the fixalloc pool. It is only intended to be used during freemcache,
// and assumes it is on the system stack and that releaseAll was already called.
func (c *mcache) freeReusableLinks() {
	// releaseAll already moved all reusableLinks to the mcache's reusableLinkCache.
	// Note that we bound the total count of reusableLinks per mcache (currently 2x size classes
	// in worst case.)
	if c.reusableLinkCache != nil {
		next := c.reusableLinkCache
		// TODO(thepudds): see TODO in dropReusableLists about possibly improving this
		// so that we are not useing fixalloc for each link.
		// TODO(thepudds): debatable if better to have the lock inside the loop.
		lock(&mheap_.reusableLinkLock)
		for next != nil {
			rl := next
			next = rl.next
			(mheap_.reusableLinkAlloc.free(unsafe.Pointer(rl)))
		}
		unlock(&mheap_.reusableLinkLock)
	}
}

// countExplicitReuse records that an allocation was satisfied by an object
// that freegc made reusable.
func (c *mcache) countExplicitReuse(spc spanClass) {
	c.explicitReuses++
	c.explicitReuseBytes += uintptr(gc.SizeClassToSize[spc.sizeclass()])
}

// addReusableNoscan adds a noscan object pointer to the reusable pointer free list
// for a span class.
func (c *mcache) addReusableNoscan(spc spanClass, ptr uintptr) {
	if !runtimeFreegcEnabled {
		return
	}

	// Add to the reusable pointers free list.
	c.reusableListsNoScan.push(spc, gclinkptr(ptr))
}

// addReusableScan adds an object pointer to a chunked linked list of reusable pointers
// for a span class.
func (c *mcache) addReusableScan(spc spanClass, ptr uintptr) {
	if !runtimeFreegcEnabled {
		return
	}

	rl := c.reusableListsScan.head(spc)
	if rl == nil || rl.len == int32(len(rl.ptrs)) {
		sizeclass := spc.sizeclass()
		if c.reusableCountsScan[sizeclass] == maxReusableLinksPerSpanClass {
			// Currently conservatively allow at most 2 links per span class.
			// We are full, so drop this reusable object.
			return
		}

		// Allocate and push a new link.
		rl = c.allocReusableLink()
		c.reusableCountsScan[sizeclass]++
		c.reusableListsScan.push(spc, rl)
	}
	rl.ptrs[rl.len] = ptr
	rl.len++
}

// hasReusableNoscan reports whether there is a reusable object available for
// a noscan spc.
func (c *mcache) hasReusableNoscan(spc spanClass) bool {
	if !runtimeFreegcEnabled {
		return false
	}
	return c.reusableListsNoScan.head(spc) != 0
}

// hasReusableScan reports whether there is a reusable object available for
// a scan spc.
func (c *mcache) hasReusableScan(spc spanClass) bool {
	if !runtimeFreegcEnabled || c.reusableListsScan.head(spc) == nil {
		return false
	}
	rl := c.reusableListsScan.head(spc)
	return rl.len > 0 || rl.next != nil
}

// reusableLinkWithObjects returns a reusableLink containing reusable pointers for the span class.
// It must only be called after hasReusableScan has returned true.
func (c *mcache) reusableLinkWithObjects(spc spanClass) *reusableLink {
	head := c.reusableListsScan.head(spc)
	if head.len > 0 {
		// Use this one.
		return head
	}

	// head is empty. We'll use the next link, and
	// store the empty head in the reusableLinkCache.
	// Start by popping the empty head.
	c.reusableListsScan.pop(spc)
	c.reusableCountsScan[spc.sizeclass()]--
	empty := head
	next := empty.next
	if next == nil {
		throw("reusableLinkWithObjects: no reusableLink with reusable objects")
	}
	if next.len != int32(len(next.ptrs)) {
		throw("reusableLinkWithObjects: next reusable link is not full")
	}

	// Prepend the empty link to reusableLinkCache.
	empty.next = c.reusableLinkCache
	c.reusableLinkCache = empty

	return next
}

// dropReusableLists clears all reusable lists in the mcache.
func (c *mcache) dropReusableLists() {
	// Clearing the noscan lists is trivial because the free list
	// is stored in the heap objects themselves, so we can just
	// wipe the heads.
	clear(c.reusableListsNoScan.lists[:])

	// Clearing the scan lists currently requires iterating over
	// the different size classes, and we store the reusableLinks
	// back in a cache on the mcache to avoid going back to fixalloc later,
	// and also lets reusableLinks move between size classes over time.
	//
	// TODO(thepudds): we have a few options on how to improve this. One is we
	// could consider arranging things so that we can heap allocate the reusableLinks,
	// which means we could have a simpler and faster approach to dropping them,
	// and possibly get rid of their cache, though would require some care to allow heap allocation.
	for sizeclass := range c.reusableListsScan.lists {
		if c.reusableListsScan.lists[sizeclass] != nil {
			// For scan objects, we have chunked linked lists. We save the list nodes in
			// c.reusableLinkCache.
			rl := c.reusableListsScan.lists[sizeclass]

			// Prepend the list in c.reusableScan[i] to c.reusableLinkCache.
			// First, find the tail. Note we don't clear or reset each link,
			// and instead do that when pulling from the cache.
			// We currently deliberately have a very low maxReusableLinksPerSpanClass (currently 2),
			// and hence this loop will be short.
			for rl.next != nil {
				rl = rl.next
			}
			rl.next = c.reusableLinkCache
			c.reusableLinkCache = c.reusableListsScan.lists[sizeclass]
			c.reusableListsScan.lists[sizeclass] = nil
			c.reusableCountsScan[sizeclass] = 0
		}
	}
}
