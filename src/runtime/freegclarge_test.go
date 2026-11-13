// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"runtime"
	"testing"
	"unsafe"
)

var largeSink []byte

// skipUnlessFreegcReuses skips tests that need freegc to make objects
// reusable.
func skipUnlessFreegcReuses(t *testing.T) {
	if !runtime.RuntimeFreegcEnabled {
		t.Skip("requires GOEXPERIMENT=runtimefreegc")
	}
	if runtime.FreegcCheck() != 0 {
		t.Skip("GODEBUG=freegccheck disables reuse")
	}
}

func TestFreegcLargeReuse(t *testing.T) {
	skipUnlessFreegcReuses(t)
	const size = 100 << 10
	// Stay on one P so the freed span is in this P's cache.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))

	reused := 0
	for range 100 {
		b := make([]byte, size)
		largeSink = b
		for i := range b {
			b[i] = 0xff
		}
		p := unsafe.SliceData(b)
		largeSink = nil
		runtime.Freegc(unsafe.Pointer(p), size, true)

		c := make([]byte, size)
		largeSink = c
		if unsafe.SliceData(c) == p {
			reused++
		}
		for i, x := range c {
			if x != 0 {
				t.Fatalf("reused large object not zeroed at %d: %#x", i, x)
			}
		}
		largeSink = nil
	}
	// A GC between the free and the allocation empties the cache, so
	// allow some misses.
	if reused < 50 {
		t.Errorf("reused %d of 100 freed large objects, want most", reused)
	}
}

func TestFreegcLargeDifferentSize(t *testing.T) {
	skipUnlessFreegcReuses(t)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))

	b := make([]byte, 100<<10)
	largeSink = b
	p := unsafe.SliceData(b)
	largeSink = nil
	runtime.Freegc(unsafe.Pointer(p), uintptr(len(b)), true)

	c := make([]byte, 200<<10)
	largeSink = c
	if unsafe.SliceData(c) == p {
		t.Errorf("a larger allocation reused a freed span that is too small")
	}
	largeSink = nil
}

func TestFreegcCreditsHeapLive(t *testing.T) {
	skipUnlessFreegcReuses(t)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))

	const n, size = 10000, 64
	objs := make([][]byte, n)
	for try := 0; ; try++ {
		runtime.GC()
		for i := range objs {
			objs[i] = make([]byte, size)
		}
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		gcs := stats.NumGC
		before := runtime.HeapLive()
		for i, b := range objs {
			objs[i] = nil
			runtime.Freegc(unsafe.Pointer(unsafe.SliceData(b)), size, true)
		}
		after := runtime.HeapLive()
		runtime.ReadMemStats(&stats)
		if stats.NumGC != gcs {
			// A GC recomputed heapLive in between.
			if try < 5 {
				continue
			}
			t.Skip("a GC ran during every attempt")
		}
		// Up to freegcCreditFlush of credit may still be pending on the P.
		if want := uint64(n*size - 64<<10); before < after || before-after < want {
			t.Errorf("heapLive went from %d to %d after freeing %d bytes, want a drop of at least %d", before, after, n*size, want)
		}
		return
	}
}

func TestFreegcLargeEvictionKeepsHeapLive(t *testing.T) {
	skipUnlessFreegcReuses(t)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))

	const n, size = 16, 256 << 10
	objs := make([][]byte, n)
	for try := 0; ; try++ {
		runtime.GC()
		for i := range objs {
			objs[i] = make([]byte, size)
		}
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		gcs := stats.NumGC
		before := runtime.HeapLive()
		for i, b := range objs {
			objs[i] = nil
			runtime.Freegc(unsafe.Pointer(unsafe.SliceData(b)), size, true)
		}
		after := runtime.HeapLive()
		runtime.ReadMemStats(&stats)
		if stats.NumGC != gcs {
			if try < 5 {
				continue
			}
			t.Skip("a GC ran during every attempt")
		}
		// Only the spans the P keeps for reuse leave heapLive; the ones
		// it forgets stay allocated until the next GC. Up to
		// freegcCreditFlush of credit may still be pending on the P.
		const kept, slack = runtime.ReusableLargeSpans * size, 64 << 10
		if before < after || before-after > kept+slack || before-after+slack < kept {
			t.Errorf("heapLive went from %d to %d after freeing %d large objects, want a drop of %d (the spans kept for reuse)", before, after, n, kept)
		}
		return
	}
}

func TestFreegcProfiled(t *testing.T) {
	skipUnlessFreegcReuses(t)
	defer func(old int) { runtime.MemProfileRate = old }(runtime.MemProfileRate)
	runtime.MemProfileRate = 1 // sample every allocation, including reuses
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))

	for _, size := range []int{64, 100 << 10} {
		for range 200 {
			b := make([]byte, size)
			largeSink = b
			largeSink = nil
			runtime.Freegc(unsafe.Pointer(unsafe.SliceData(b)), uintptr(size), true)
			// Reusing the slot samples it again; the free must have
			// ended the old profile record.
			largeSink = make([]byte, size)
		}
	}
	largeSink = nil
	runtime.GC()
}
