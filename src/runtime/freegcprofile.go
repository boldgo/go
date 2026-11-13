// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import "unsafe"

// freegcSpecials prepares the object at p, of the given size, to be reused
// after freegc. If the heap profiler sampled the object, it records the
// free and removes the profile record, as sweeping would, so that a reuse
// of the slot can be sampled as a new allocation. It reports false, and
// changes nothing, if p is not a heap object or has any other special
// record, such as a finalizer, cleanup, or weak pointer; registering one
// requires handing the pointer to the runtime, so the compiler never frees
// such an object, but freegc keeps it rather than trust that.
//
// It must be called with mp.mallocing set.
func freegcSpecials(p, size uintptr) bool {
	s := spanOfHeap(p)
	if s == nil {
		return false
	}
	if s.specials == nil {
		return true
	}

	// Sweeping reads the specials list without the lock.
	s.ensureSwept()
	offset := p - s.base()
	lock(&s.speciallock)
	iter := &s.specials
	for *iter != nil && (*iter).offset < offset {
		iter = &(*iter).next
	}
	for x := *iter; x != nil && x.offset == offset; x = x.next {
		if x.kind != _KindSpecialProfile {
			unlock(&s.speciallock)
			return false
		}
	}
	var prof *special
	if x := *iter; x != nil && x.offset == offset {
		prof = x
		*iter = x.next
	}
	if s.specials == nil {
		spanHasNoSpecials(s)
	}
	unlock(&s.speciallock)
	if prof != nil {
		freeSpecial(prof, unsafe.Pointer(p), size)
	}
	return true
}

// sampleReuse samples an allocation satisfied by reusing a freed object
// for the heap profile, as for a new allocation of size bytes at x.
func (c *mcache) sampleReuse(mp *m, x unsafe.Pointer, size uintptr) {
	c.nextSample -= int64(size)
	if c.nextSample < 0 || MemProfileRate != c.memProfRate {
		profilealloc(mp, x, size)
	}
}
