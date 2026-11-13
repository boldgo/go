// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// GODEBUG=freegccheck verifies the frees inserted by the compiler under
// GOEXPERIMENT=runtimefreegc.
//
// With freegccheck set, freegc does not make freed objects reusable. It
// records them instead, and the next garbage collection checks that each
// recorded object is unreachable. An object the collector still marks was
// freed while something could reach it, which means the compiler's proof
// that it was dead was wrong. freegccheck=1 throws at the first such
// object; freegccheck=2 prints each one and keeps running.
//
// Because nothing is reused, a program run with freegccheck behaves like a
// program that never called freegc, so it is safe to run whole test suites
// under it.
//
// The innermost frames of asynchronously preempted goroutines are scanned
// conservatively, and a stale register there often still holds a pointer
// to an object that was just freed. Such an object is still marked, since
// it might really be in use, but it is not reported: its check moves to a
// later cycle, where a stale register rarely survives and a real reference
// is found again.

package runtime

import (
	"internal/goarch"
	"unsafe"
)

const freegcCheckEntries = 4096

type freegcCheckEntry struct {
	// addr is a uintptr so that recording an object does not keep it alive.
	addr uintptr
	size uintptr
	// pcs holds the return PCs of freegc's caller and the next two frames.
	pcs [3]uintptr
	// freedInMark records that the object was freed while the collector
	// was marking. It may have been marked (or allocated black) before
	// the free, so its first check is deferred to the following cycle.
	freedInMark bool
	// conservative records that this cycle's conservative stack scan
	// found a pointer to the object.
	conservative bool
	// deferrals counts consecutive cycles whose check was deferred
	// because of conservative marking.
	deferrals uint8
}

// freegcCheckMaxDeferrals bounds how many cycles an object that keeps being
// marked only conservatively stays recorded.
const freegcCheckMaxDeferrals = 8

// freegcCheckBuf is a per-P ring of recorded frees. It is allocated on the
// first recorded free, so it costs one nil pointer per P when unused.
type freegcCheckBuf struct {
	entries     [freegcCheckEntries]freegcCheckEntry
	n           int    // valid entries
	next        int    // next slot to write
	overwritten uint64 // entries lost to wrap-around before being checked
}

// freegcCheckReported counts objects reported by freegccheck=2.
var freegcCheckReported uint64

// freegcCheckRecord records an object passed to freegc instead of making it
// reusable. It must be called with mp.mallocing set, from the freegc call.
func freegcCheckRecord(c *mcache, ptr unsafe.Pointer, size uintptr) {
	b := c.freegcCheck
	if b == nil {
		b = (*freegcCheckBuf)(persistentalloc(unsafe.Sizeof(freegcCheckBuf{}), goarch.PtrSize, &memstats.other_sys))
		c.freegcCheck = b
	}
	if b.n < len(b.entries) {
		b.n++
	} else {
		b.overwritten++
	}
	e := &b.entries[b.next]
	b.next = (b.next + 1) % len(b.entries)
	e.addr = uintptr(ptr)
	e.size = size
	e.freedInMark = gcphase != _GCoff
	e.conservative = false
	e.deferrals = 0
	e.pcs = [3]uintptr{}
	// Skip callers, freegcCheckRecord and freegc.
	callers(3, e.pcs[:])
}

// freegcCheckVerify checks the recorded frees against the mark bits of the
// cycle that just finished. It runs during mark termination with the world
// stopped, after marking completes and before sweeping starts.
func freegcCheckVerify() {
	for _, pp := range allp {
		c := pp.mcache
		if c == nil || c.freegcCheck == nil {
			continue
		}
		b := c.freegcCheck
		kept := 0
		for i := 0; i < b.n; i++ {
			e := b.entries[i]
			s := spanOfHeap(e.addr)
			if s == nil {
				continue
			}
			marked := s.markBitsForIndex(s.objIndex(e.addr)).isMarked()
			if e.freedInMark {
				// Unmarked: the collector also found it dead, and this
				// cycle's sweep may give its slot to another object, so
				// it cannot be checked later. Marked: it survives the
				// sweep, so check it in the next cycle.
				if marked {
					e.freedInMark = false
					b.entries[kept] = e
					kept++
				}
				continue
			}
			if marked && e.conservative {
				if e.deferrals < freegcCheckMaxDeferrals {
					e.conservative = false
					e.deferrals++
					b.entries[kept] = e
					kept++
				}
				continue
			}
			if marked {
				freegcCheckReport(&e)
			}
		}
		b.n = kept
		b.next = kept % len(b.entries)
	}
}

// freegcCheckConservative notes that a conservative scan marked the object
// at obj, of the given size, so that a recorded free of it is not reported
// in this cycle. The entries belong to other Ps, which may be recording
// concurrently; a lost or misplaced flag only moves a check by one cycle.
func freegcCheckConservative(obj, size uintptr) {
	for _, pp := range allp {
		c := pp.mcache
		if c == nil || c.freegcCheck == nil {
			continue
		}
		b := c.freegcCheck
		for i := 0; i < b.n; i++ {
			if e := &b.entries[i]; e.addr-obj < size {
				e.conservative = true
			}
		}
	}
}

func freegcCheckReport(e *freegcCheckEntry) {
	print("runtime: freegccheck: object ", hex(e.addr), " of size ", e.size, " is still reachable after it was freed at:\n")
	for _, pc := range e.pcs {
		if pc == 0 {
			break
		}
		f := findfunc(pc)
		if !f.valid() {
			print("\t", hex(pc), "\n")
			continue
		}
		file, line := funcline(f, pc-1)
		print("\t", funcname(f), "\n\t\t", file, ":", line, "\n")
	}
	if debug.freegccheck == 1 {
		throw("freegccheck: freed object is still reachable")
	}
	freegcCheckReported++
}
