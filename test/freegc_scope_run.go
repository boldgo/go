// run

//go:build goexperiment.runtimefreegc

// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Test that makes freeing their heap object when its scope ends never
// free an object that is still in use.

package main

import (
	"fmt"
	"runtime"
	"runtime/metrics"
	"sync"
)

//go:noinline
func sumFilled(n, seed int) int {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = byte(i + seed)
	}
	t := 0
	for _, b := range buf {
		t += int(b)
	}
	return t
}

func wantSum(n, seed int) int {
	t := 0
	for i := range n {
		t += int(byte(i + seed))
	}
	return t
}

// twoSites keeps objects from two make sites live at once and checks
// that neither is reused while the other is written.
//
//go:noinline
func twoSites(n, iters int) {
	for it := range iters {
		a := make([]int64, n)
		for i := range a {
			a[i] = int64(it<<20 | i)
		}
		b := make([]int64, n+it%3)
		for i := range b {
			b[i] = -int64(it<<20 | i)
		}
		for i := range a {
			if a[i] != int64(it<<20|i) || b[i] != -int64(it<<20|i) {
				panic(fmt.Sprintf("iteration %d: corrupted at %d: %d %d", it, i, a[i], b[i]))
			}
		}
	}
}

// recurse holds its object across a recursive call that runs the same
// make site in a new frame.
//
//go:noinline
func recurse(depth, n int) {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = byte(depth)
	}
	if depth > 0 {
		recurse(depth-1, n)
	}
	for i, b := range buf {
		if b != byte(depth) {
			panic(fmt.Sprintf("depth %d: corrupted at %d: %d", depth, i, b))
		}
	}
}

func explicitFrees() uint64 {
	runtime.GC() // flush per-P counts
	s := []metrics.Sample{{Name: "/gc/heap/explicit-frees:objects"}}
	metrics.Read(s)
	return s[0].Value.Uint64()
}

func main() {
	before := explicitFrees()

	for seed := range 2000 {
		n := 100 + seed%700
		if got, want := sumFilled(n, seed), wantSum(n, seed); got != want {
			panic(fmt.Sprintf("sumFilled(%d, %d) = %d, want %d", n, seed, got, want))
		}
	}
	twoSites(40, 2000)
	recurse(50, 300)

	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			twoSites(50+g, 1000)
		}()
	}
	wg.Wait()

	if d := explicitFrees() - before; d < 2000 {
		panic(fmt.Sprintf("only %d explicit frees", d))
	}
}
