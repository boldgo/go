// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"internal/asan"
	"internal/goexperiment"
	"internal/msan"
	"internal/race"
	"internal/testenv"
	"testing"
	"unsafe"
)

// Stack allocation size for variable-sized allocations.
// Matches constant of the same name in ../walk/builtin.go:walkMakeSlice.
const maxStackSize = 32

//go:noinline
func genericUse[T any](s []T) {
	// Doesn't escape s.
}

func TestStackAllocation(t *testing.T) {
	testenv.SkipIfOptimizationOff(t)

	type testCase struct {
		f           func(int)
		elemSize    uintptr
		hasPointers bool
	}

	for _, tc := range []testCase{
		{
			f: func(n int) {
				genericUse(make([]int, n))
			},
			elemSize: unsafe.Sizeof(int(0)),
		},
		{
			f: func(n int) {
				genericUse(make([]*byte, n))
			},
			elemSize:    unsafe.Sizeof((*byte)(nil)),
			hasPointers: true,
		},
		{
			f: func(n int) {
				genericUse(make([]string, n))
			},
			elemSize:    unsafe.Sizeof(""),
			hasPointers: true,
		},
	} {
		max := maxStackSize / int(tc.elemSize)
		if n := testing.AllocsPerRun(10, func() {
			tc.f(max)
		}); n != 0 {
			t.Fatalf("unexpected allocation: %f", n)
		}
		want := 1.0
		if goexperiment.RuntimeFreegc && !tc.hasPointers && !race.Enabled && !asan.Enabled && !msan.Enabled {
			// The heap object is freed when f returns, so the next run
			// reuses it.
			want = 0
		}
		if n := testing.AllocsPerRun(10, func() {
			tc.f(max + 1)
		}); n != want {
			t.Fatalf("unexpected allocation: %f, want %f", n, want)
		}
	}
}
