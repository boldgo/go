// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"internal/runtime/atomic"
	"internal/runtime/sys"
	"unsafe"
)

// A module linked with -compresspclntab keeps the tables that only
// tracebacks and symbolization read (function names, the file tables and
// the pc-file and pc-line tables) deflated. The runtime inflates them on
// first use into memory of their own, outside the heap, so the cost falls
// only on programs that ask for a function name, a file or a line.
//
// The pc-file and pc-line tables take offsets from linetabBase, past the
// end of pctab, which keeps them distinct from pctab's offsets in the
// pcvalue cache.
//
// Inflating takes no locks and does not allocate from the heap, so a
// traceback can trigger it from any context, including a signal handler
// that interrupted another inflation on the same thread; each inflates
// its own copy and the first to finish publishes it.

// pcHeaderCompressed is the header of a module whose magic is
// abi.CompressedPCLnTabMagic. Keep in sync with cmd/link and debug/gosym.
type pcHeaderCompressed struct {
	pcHeader
	tracetabOffset  uintptr // offset to the deflated tables from pcHeader
	tracetabSize    uintptr // size of the deflated tables
	cutabSize       uintptr // inflated, the tables are cutab,
	funcnametabSize uintptr // funcnametab,
	filetabSize     uintptr // filetab,
	linetabSize     uintptr // and the pc-file and pc-line tables
	linetabBase     uintptr // offset of the pc-file and pc-line tables
}

// inflatedTables records the inflated tables of a module.
type inflatedTables struct {
	_     sys.NotInHeap
	datap *moduledata
	next  *inflatedTables
	base  uintptr // start of the tables, or 0 if they could not be inflated
}

// inflatedList is the list of modules whose tables have been inflated.
var inflatedList atomic.UnsafePointer // *inflatedTables

func (md *moduledata) compressedTabs() bool {
	return md.pcHeader.magic == abi.CompressedPCLnTabMagic
}

func (md *moduledata) compressedHeader() *pcHeaderCompressed {
	return (*pcHeaderCompressed)(unsafe.Pointer(md.pcHeader))
}

// inflatedBase returns the start of md's inflated tables, inflating them
// if needed, or 0 if they cannot be inflated.
func (md *moduledata) inflatedBase() uintptr {
	for t := (*inflatedTables)(inflatedList.Load()); t != nil; t = t.next {
		if t.datap == md {
			return t.base
		}
	}
	h := md.compressedHeader()
	size := unsafe.Sizeof(inflatedTables{}) + h.cutabSize + h.funcnametabSize + h.filetabSize + h.linetabSize
	mem := sysAlloc(size, &memstats.other_sys, "pclntab")
	if mem == nil {
		return 0
	}
	t := (*inflatedTables)(mem)
	t.datap = md
	base := uintptr(mem) + unsafe.Sizeof(inflatedTables{})
	in := unsafe.Slice((*byte)(add(unsafe.Pointer(h), h.tracetabOffset)), h.tracetabSize)
	out := unsafe.Slice((*byte)(unsafe.Pointer(base)), size-unsafe.Sizeof(inflatedTables{}))
	scratch := sysAlloc(unsafe.Sizeof(inflateScratch{}), &memstats.other_sys, "pclntab")
	if scratch != nil && inflate(in, out, (*inflateScratch)(scratch)) {
		t.base = base
	} else {
		println("runtime: cannot inflate the compressed symbol tables of", md.modulename)
	}
	if scratch != nil {
		sysFree(scratch, unsafe.Sizeof(inflateScratch{}), &memstats.other_sys)
	}
	for {
		head := inflatedList.Load()
		for o := (*inflatedTables)(head); o != nil; o = o.next {
			if o.datap == md {
				// Another thread finished first.
				sysFree(mem, size, &memstats.other_sys)
				return o.base
			}
		}
		t.next = (*inflatedTables)(head)
		if inflatedList.CompareAndSwapNoWB(head, mem) {
			return t.base
		}
	}
}

// inflatedFuncnametab returns md's function name table, or nil if it
// cannot be inflated. md must have compressed tables.
func (md *moduledata) inflatedFuncnametab() []byte {
	base := md.inflatedBase()
	if base == 0 {
		return nil
	}
	h := md.compressedHeader()
	return unsafe.Slice((*byte)(unsafe.Pointer(base+h.cutabSize)), h.funcnametabSize)
}

// inflatedFileTabs returns md's cutab and filetab, or nils if they cannot
// be inflated. md must have compressed tables.
func (md *moduledata) inflatedFileTabs() ([]uint32, []byte) {
	base := md.inflatedBase()
	if base == 0 {
		return nil, nil
	}
	h := md.compressedHeader()
	cutab := unsafe.Slice((*uint32)(unsafe.Pointer(base)), h.cutabSize/4)
	filetab := unsafe.Slice((*byte)(unsafe.Pointer(base+h.cutabSize+h.funcnametabSize)), h.filetabSize)
	return cutab, filetab
}

// pcTable returns the pc-value table at off, or nil if it is a
// compressed table that cannot be inflated.
func (md *moduledata) pcTable(off uint32) []byte {
	if !md.compressedTabs() {
		return md.pctab[off:]
	}
	h := md.compressedHeader()
	if uintptr(off) < h.linetabBase {
		return md.pctab[off:]
	}
	base := md.inflatedBase()
	if base == 0 {
		return nil
	}
	off -= uint32(h.linetabBase)
	if uintptr(off) >= h.linetabSize {
		return nil
	}
	start := base + h.cutabSize + h.funcnametabSize + h.filetabSize + uintptr(off)
	return unsafe.Slice((*byte)(unsafe.Pointer(start)), h.linetabSize-uintptr(off))
}
