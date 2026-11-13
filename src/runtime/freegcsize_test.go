// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"runtime"
	"runtime/metrics"
	"testing"
	"unsafe"
)

var (
	freegcGlobal   [64]byte
	freegcSizeSink []byte
)

func explicitFreeCount() uint64 {
	runtime.GC() // flush per-P counts
	s := []metrics.Sample{{Name: "/gc/heap/explicit-frees:objects"}}
	metrics.Read(s)
	return s[0].Value.Uint64()
}

func TestFreegcWrongSize(t *testing.T) {
	if !runtime.RuntimeFreegcEnabled {
		t.Skip("requires GOEXPERIMENT=runtimefreegc")
	}
	if runtime.FreegcCheck() != 0 {
		t.Skip("freegccheck throws on a size that does not match the object")
	}

	freegcSizeSink = make([]byte, 64) // on the heap
	p := unsafe.Pointer(unsafe.SliceData(freegcSizeSink))
	freegcSizeSink = nil
	before := explicitFreeCount()
	runtime.Freegc(p, 16, true)                                // another size class
	runtime.Freegc(p, 64, false)                               // not a scan object
	runtime.Freegc(unsafe.Add(p, 16), 48, true)                // interior pointer
	runtime.Freegc(unsafe.Pointer(&freegcGlobal[0]), 64, true) // not on the heap
	if d := explicitFreeCount() - before; d != 0 {
		t.Errorf("freed %d objects with a mismatched pointer or size", d)
	}

	before = explicitFreeCount()
	runtime.Freegc(p, 64, true)
	if d := explicitFreeCount() - before; d != 1 {
		t.Errorf("a correct free counted %d frees, want 1", d)
	}
}
