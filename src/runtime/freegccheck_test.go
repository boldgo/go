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

var freegcCheckSink []byte

func TestFreegcCheck(t *testing.T) {
	if !runtime.RuntimeFreegcEnabled {
		t.Skip("requires GOEXPERIMENT=runtimefreegc")
	}
	old := runtime.SetFreegcCheck(2)
	defer runtime.SetFreegcCheck(old)
	runtime.GC()

	// A wrong free: the global still references the object.
	freegcCheckSink = make([]byte, 64)
	before := runtime.FreegcCheckReported()
	runtime.Freegc(unsafe.Pointer(unsafe.SliceData(freegcCheckSink)), 64, true)
	runtime.GC()
	if got := runtime.FreegcCheckReported() - before; got < 1 {
		t.Errorf("reachable freed object was not reported")
	}
	freegcCheckSink = nil

	// Correct frees: nothing references the objects once they are freed.
	before = runtime.FreegcCheckReported()
	for range 100 {
		freeUnreachable()
	}
	runtime.GC()
	runtime.GC()
	if got := runtime.FreegcCheckReported() - before; got != 0 {
		t.Errorf("unreachable freed objects: got %d reports, want 0", got)
	}
}

//go:noinline
func freeUnreachable() {
	freegcCheckSink = make([]byte, 64)
	p := unsafe.Pointer(unsafe.SliceData(freegcCheckSink))
	freegcCheckSink = nil
	runtime.Freegc(p, 64, true)
}

func TestExplicitFreeMetrics(t *testing.T) {
	if !runtime.RuntimeFreegcEnabled {
		t.Skip("requires GOEXPERIMENT=runtimefreegc")
	}
	if runtime.FreegcCheck() != 0 {
		t.Skip("GODEBUG=freegccheck disables reuse")
	}
	samples := []metrics.Sample{
		{Name: "/gc/heap/explicit-frees:objects"},
		{Name: "/gc/heap/explicit-frees:bytes"},
		{Name: "/gc/heap/explicit-reuses:objects"},
		{Name: "/gc/heap/explicit-reuses:bytes"},
	}
	read := func() [4]uint64 {
		runtime.GC() // flush per-P counts
		metrics.Read(samples)
		var v [4]uint64
		for i, s := range samples {
			if s.Value.Kind() != metrics.KindUint64 {
				t.Fatalf("%s: unexpected kind %v", s.Name, s.Value.Kind())
			}
			v[i] = s.Value.Uint64()
		}
		return v
	}

	before := read()
	const n = 100
	for range n {
		freeUnreachable()
		freegcCheckSink = make([]byte, 64) // usually satisfied by reuse
	}
	freegcCheckSink = nil
	after := read()

	// Code elsewhere in the test binary may also free and reuse memory, so
	// these are lower bounds.
	if got := after[0] - before[0]; got < n {
		t.Errorf("explicit-frees:objects grew by %d, want at least %d", got, n)
	}
	if got := after[1] - before[1]; got < n*64 {
		t.Errorf("explicit-frees:bytes grew by %d, want at least %d", got, n*64)
	}
	reuses := after[2] - before[2]
	if reuses == 0 {
		t.Errorf("explicit-reuses:objects did not grow")
	}
	if got := after[3] - before[3]; got < reuses*16 {
		t.Errorf("explicit-reuses:bytes grew by %d for %d reuses", got, reuses)
	}
}
