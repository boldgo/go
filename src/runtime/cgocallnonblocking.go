// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/runtime/sys"
	"unsafe"
)

// cgocallNonblocking calls fn(arg) on the system stack, as cgocall does,
// for a C function declared with #cgo nonblocking: one that returns
// quickly, never blocks and never calls back into Go.
//
// It skips entersyscall and exitsyscall, about half the cost of a cgo
// call. The goroutine keeps its P, and the scheduler, the garbage
// collector and the execution tracer see it as running until fn returns.
// A stop-the-world or a scan of the goroutine's stack waits for fn to
// return, so a function that does block stalls them, and other
// goroutines may not run at all if no other P is free to run them.
//
// cgocallNonblocking is an internal detail of cgo.
//
//go:linkname cgocallNonblocking
//go:nosplit
func cgocallNonblocking(fn, arg unsafe.Pointer) int32 {
	if !iscgo && GOOS != "solaris" && GOOS != "illumos" && GOOS != "windows" {
		throw("cgocall unavailable")
	}
	if fn == nil {
		throw("cgocall nil")
	}
	if raceenabled {
		racereleasemerge(unsafe.Pointer(&racecgosync))
	}

	mp := getg().m
	mp.ncgocall++
	mp.cgoCallers[0] = 0
	mp.incgo = true
	mp.incgoNonblock = true
	mp.ncgo++

	errno := asmcgocall(fn, arg)

	mp.incgo = false
	mp.incgoNonblock = false
	mp.ncgo--

	if raceenabled {
		raceacquire(unsafe.Pointer(&racecgosync))
	}
	if sys.DITSupported {
		// C code may have changed DIT on this thread.
		gp := getg()
		if ditEnabled := sys.DITEnabled(); !gp.ditWanted && ditEnabled {
			sys.DisableDIT()
		} else if gp.ditWanted && !ditEnabled {
			sys.EnableDIT()
		}
	}
	return errno
}
