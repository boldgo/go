// asmcheck

//go:build goexperiment.runtimefreegc

// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package codegen

// Appends that only the SSA ownership pass proves safe to free, and appends
// it must leave alone.

func pushNonNeg(s []int, x int) []int {
	if x < 0 {
		return s
	}
	// The slice comes from a parameter, so on its own pushNonNeg
	// cannot free it.
	// amd64:`.*growslice\b` -`.*growsliceNoAlias\b`
	return append(s, x)
}

// Inlining pushNonNeg turns its early return into a goto, which the
// syntax-based alias analysis cannot follow.
func AppendInlined(n int) []int {
	var r []int
	for i := range n {
		// amd64:`.*growsliceNoAlias\b`
		r = pushNonNeg(r, i-5)
	}
	return r
}

func AppendGoto(n int) []int {
	var r []int
	i := 0
loop:
	if i < n {
		// amd64:`.*growsliceNoAlias\b`
		r = append(r, i)
		i++
		goto loop
	}
	return r
}

func AppendBesideClosure(n int, f func(int) int) []int {
	g := func(x int) int { return f(x) + 1 }
	var r []int
	for i := range n {
		// amd64:`.*growsliceNoAlias\b`
		r = append(r, g(i))
	}
	return r
}

var appendSink []int

func AppendEscapes(n int) {
	var r []int
	for i := range n {
		// amd64:`.*growslice\b` -`.*growsliceNoAlias\b`
		r = append(r, i)
		appendSink = r
	}
}

func AppendEscapesAfter(n int) {
	r := make([]int, 0, n/2)
	for i := range n {
		// amd64:`.*growsliceNoAlias\b`
		r = append(r, i)
	}
	appendSink = r
}

func AppendReducedCap(n int) []int {
	r := make([]int, 0, n/2)
	for i := range n {
		if i == 10 {
			r = r[:len(r):len(r)]
		}
		// amd64:`.*growslice\b` -`.*growsliceNoAlias\b`
		r = append(r, i)
	}
	return r
}

func AppendInterior(n int) []int {
	r := make([]int, 0, n/2)
	for i := range n {
		if i == 10 {
			r = r[1:]
		}
		// amd64:`.*growslice\b` -`.*growsliceNoAlias\b`
		r = append(r, i)
	}
	return r
}

func AppendOldLive(n int) (int, []int) {
	r := make([]int, 1, n/2)
	t := r
	for i := range n {
		// amd64:`.*growslice\b` -`.*growsliceNoAlias\b`
		r = append(r, i)
	}
	return t[0], r
}

func AppendCopy(n int, dst []int) []int {
	r := make([]int, 0, n/2)
	for i := range n {
		// amd64:`.*growsliceNoAlias\b`
		r = append(r, i)
		copy(dst, r)
	}
	return r
}

func AppendStoreThroughOld(n int) []int {
	r := make([]int, 1, n/2)
	for i := range n {
		old := r
		// amd64:`.*growslice\b` -`.*growsliceNoAlias\b`
		r = append(r, i)
		old[0] = i
	}
	return r
}

func AppendParam(r []int, n int) []int {
	for i := range n {
		// amd64:`.*growslice\b` -`.*growsliceNoAlias\b`
		r = append(r, i)
	}
	return r
}
