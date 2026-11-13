// errorcheck -0 -d=freegcssa=2

//go:build goexperiment.runtimefreegc && (amd64 || arm64)

// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Test which appends the SSA ownership pass proves can free their old
// backing store. Appends the alias analysis in escape/alias.go already
// proves are not reported here.

package p

func namedResult(n int) (r []int) {
	for i := range n {
		r = append(r, i) // ERROR "ssa ownership: frees old backing store"
	}
	return
}

func reassignedNil(n int) []int {
	var r []int
	r = nil
	for i := range n {
		r = append(r, i) // ERROR "ssa ownership: frees old backing store"
	}
	return r
}

func backwardGoto(n int) []int {
	var r []int
	i := 0
loop:
	if i < n {
		r = append(r, i) // ERROR "ssa ownership: frees old backing store"
		i++
		goto loop
	}
	return r
}

func copiedEachIteration(n int, dst []int) []int {
	r := make([]int, 0, n/2)
	for i := range n {
		r = append(r, i) // ERROR "ssa ownership: frees old backing store"
		copy(dst, r)
	}
	return r
}

//go:noinline
func sum(s []int) (t int) {
	for _, x := range s {
		t += x
	}
	return t
}

func passedToNonRetainingCall(n int) (int, []int) {
	r := make([]int, 0, n/2)
	t := 0
	for i := range n {
		r = append(r, i) // ERROR "ssa ownership: frees old backing store"
		t += sum(r)
	}
	return t, r
}

//go:noinline
func zeroAll(s []int) {
	for i := range s {
		s[i] = 0
	}
}

func passedToMutatingCall(n int) []int {
	r := make([]int, 0, n/2)
	for i := range n {
		r = append(r, i) // ERROR "ssa ownership: frees old backing store"
		zeroAll(r)
	}
	return r
}

//go:noinline
func ident(s []int) []int { return s }

func passedThrough(n int) [][]int {
	r := make([]int, 0, n/2)
	var all [][]int
	for i := range n {
		r = append(r, i)
		all = append(all, ident(r))
	}
	return all
}

var sink []int

//go:noinline
func keep(s []int) { sink = s }

func passedToRetainingCall(n int) []int {
	r := make([]int, 0, n/2)
	for i := range n {
		r = append(r, i)
		keep(r)
	}
	return r
}

func storedInLoop(n int) {
	var r []int
	for i := range n {
		r = append(r, i)
		sink = r
	}
}

func oldStillUsed(n int) []int {
	r := make([]int, 1, n/2)
	for i := range n {
		old := r
		r = append(r, i)
		old[0] = i
	}
	return r
}

func reducedCap(n int) []int {
	r := make([]int, 0, n/2)
	for i := range n {
		if i == 10 {
			r = r[:len(r):len(r)]
		}
		r = append(r, i)
	}
	return r
}

func sameOldTwice(n int) []int {
	var r []int
	for i := range n {
		// The first append still needs the old backing store; the
		// second is its last use.
		r, r = append(r, i), append(r, i+1) // ERROR "ssa ownership: frees old backing store"
	}
	return r
}

func param(r []int, n int) []int {
	for i := range n {
		r = append(r, i)
	}
	return r
}
