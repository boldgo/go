// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import "unsafe"

// Reuse of large objects freed by freegc.
//
// A large object has a span of its own. Returning that span to the heap
// when freegc is called is not possible: the span sits in an mcentral span
// set for the sweeper, and a span set does not support removal, so a freed
// and recycled mspan could be swept twice. Instead, like small objects, a
// freed large span is kept on its P and handed to the next large allocation
// on that P that needs exactly the same number of pages. Only spans without
// pointers are kept, so no type information needs to change on reuse.
//
// The cache follows the rules of the small-object reusable lists: it is
// emptied by releaseAll, which every P runs before it allocates in a new
// sweep cycle, so the sweeper freeing a cached span (which the collector
// found unreachable) can never hand it out twice. A span that is never
// reused is collected by the next cycle, exactly as it would have been
// without freegc.

// reusableLargeSpans is the number of freed large spans a P keeps.
const reusableLargeSpans = 4

// freegcLarge makes the large pointer-free object at ptr available for
// reuse by this P.
func freegcLarge(ptr unsafe.Pointer) {
	mp := acquirem()
	if mp.mallocing != 0 {
		throw("freegc deadlock")
	}
	if mp.gsignal == getg() {
		throw("freegc during signal")
	}
	s := spanOfHeap(uintptr(ptr))
	if s == nil || s.spanclass != makeSpanClass(0, true) || s.base() != uintptr(ptr) {
		// Not the start of a large pointer-free object; nothing to do.
		releasem(mp)
		return
	}
	mp.mallocing = 1
	c := getMCache(mp)
	if c == nil {
		throw("freegc called without a P or outside bootstrapping")
	}
	if debug.clobberfree != 0 {
		clobberfree(ptr, s.elemsize)
	}
	if debug.freegccheck != 0 {
		freegcCheckRecord(c, ptr, s.elemsize)
	} else {
		if c.reusableLargeN == len(c.reusableLarge) {
			// Forget the oldest; the next cycle collects it.
			copy(c.reusableLarge[:], c.reusableLarge[1:])
			c.reusableLargeN--
		}
		c.reusableLarge[c.reusableLargeN] = s
		c.reusableLargeN++
		c.creditHeapLive(-int64(s.elemsize))
	}
	c.explicitFrees++
	c.explicitFreeBytes += s.elemsize
	mp.mallocing = 0
	releasem(mp)
}

// takeReusableLarge removes and returns a cached freed span of npages
// pages, or nil if there is none.
func (c *mcache) takeReusableLarge(npages uintptr) *mspan {
	for i := c.reusableLargeN - 1; i >= 0; i-- {
		if s := c.reusableLarge[i]; s.npages == npages {
			copy(c.reusableLarge[i:], c.reusableLarge[i+1:c.reusableLargeN])
			c.reusableLargeN--
			c.reusableLarge[c.reusableLargeN] = nil
			return s
		}
	}
	return nil
}

// mallocgcLargeReuse finishes a large pointer-free allocation of size
// bytes using span s from takeReusableLarge. It is called by mallocgcLarge
// with mp.mallocing set, and releases mp.
func mallocgcLargeReuse(mp *m, c *mcache, s *mspan, size uintptr, needzero bool) (unsafe.Pointer, uintptr) {
	x := unsafe.Pointer(s.base())
	s.limit = s.base() + size
	c.explicitReuses++
	c.explicitReuseBytes += s.elemsize
	c.creditHeapLive(int64(s.elemsize))

	// mallocgc charges the full span against the assist budget, but this
	// memory is already accounted for in heapLive, as on the small-object
	// reuse paths.
	if gcBlackenEnabled != 0 {
		addAssistCredit(s.elemsize)
	}
	if writeBarrier.enabled {
		// Allocate black during GC. The object holds no pointers.
		gcmarknewobject(s, uintptr(x))
	}
	mp.mallocing = 0
	releasem(mp)

	// The span was in use, so it is dirty regardless of s.needzero.
	if needzero {
		memclrNoHeapPointersChunked(s.elemsize, x) // x keeps the memory alive
	}
	publicationBarrier()
	return x, s.elemsize
}
