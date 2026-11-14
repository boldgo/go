// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The compiler drops the pointer checks cmd/cgo inserts when the types
// show they cannot fail. TestPointerCheckElided looks for calls of
// runtime.cgoCheckPointer in each function: the Elided ones must have
// none, the Kept ones at least one.

package main

/*
static void takeptr(void *p) {}
*/
import "C"

import "unsafe"

type plain struct{ a, b int64 }

type withPointers struct {
	p *int
	a [7]int64
}

//go:noinline
func bytesElided(buf []byte) { C.takeptr(unsafe.Pointer(&buf[0])) }

//go:noinline
func arrayElided(a *[8]int64) { C.takeptr(unsafe.Pointer(&a[1])) }

//go:noinline
func fieldElided(s *plain) { C.takeptr(unsafe.Pointer(&s.b)) }

//go:noinline
func sliceElided(buf []byte) { C.takeptr(unsafe.Pointer(unsafe.SliceData(buf[1:]))) }

//go:noinline
func pointersKept(s []*int) { C.takeptr(unsafe.Pointer(&s[0])) }

//go:noinline
func fieldKept(s *withPointers) { C.takeptr(unsafe.Pointer(&s.p)) }

//go:noinline
func bareKept(p *[8]int64) { C.takeptr(unsafe.Pointer(p)) }

func main() {
	bytesElided(make([]byte, 4))
	arrayElided(new([8]int64))
	fieldElided(new(plain))
	sliceElided(make([]byte, 4))
	pointersKept(make([]*int, 1))
	fieldKept(new(withPointers))
	bareKept(new([8]int64))
}
