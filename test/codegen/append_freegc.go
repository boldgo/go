// asmcheck -gcflags=-d=freegcssa=0

//go:build goexperiment.runtimefreegc

// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The SSA ownership pass is off so that this file tests the alias
// analysis in escape/alias.go alone; append_freegc_ssa.go tests the pass.

package codegen

func Append1(n int) []int {
	var r []int
	for i := range n {
		// amd64:`.*growsliceNoAlias\b`
		r = append(r, i)
	}
	// amd64:`.*moveSliceNoCapNoScan\b`
	return r
}

func Append2(n int) (r []int) {
	for i := range n {
		// amd64:`.*growslice\b`
		r = append(r, i)
	}
	// amd64:`.*moveSliceNoCapNoScan\b`
	return
}

func Append3(n int) (r []int) {
	for i := range n {
		// amd64:`.*growslice\b`
		r = append(r, i)
	}
	// amd64:`.*moveSliceNoCapNoScan\b`
	return r
}

func Append4(n int) []int {
	var r []int
	for i := range n {
		// amd64:`.*growsliceBufNoAlias\b`
		r = append(r, i)
	}
	println(cap(r))
	// amd64:`.*moveSliceNoScan\b`
	return r
}

func Append5(n int) []int {
	var r []int
	for i := range n {
		// amd64:`.*growsliceBufNoAlias\b`
		r = append(r, i)
	}
	useSlice(r)
	// amd64:`.*moveSliceNoScan\b`
	return r
}

func Append5b(n int) []int {
	var r []int
	useSlice(r)
	for i := range n {
		// amd64:`.*growsliceBuf\b` -`.*growsliceBufNoAlias\b`
		r = append(r, i)
	}
	// amd64:`.*moveSliceNoScan\b`
	return r
}

func Append6(n int) []*int {
	var r []*int
	for i := range n {
		// TODO(thepudds): for now, the compiler only uses the NoAlias version
		// for element types without pointers.
		// amd64:`.*growslice\b`
		r = append(r, new(i))
	}
	// amd64:`.*moveSliceNoCap\b`
	return r
}

func Append7(n int) []*int {
	var r []*int
	for i := range n {
		// TODO(thepudds): for now, the compiler only uses the NoAlias version
		// for element types without pointers.
		// amd64:`.*growsliceBuf\b`
		r = append(r, new(i))
	}
	println(cap(r))
	// amd64:`.*moveSlice\b`
	return r
}

func Append8(n int, p *[]int) {
	var r []int
	for i := range n {
		// amd64:`.*growsliceNoAlias\b`
		r = append(r, i)
	}
	// amd64:`.*moveSliceNoCapNoScan\b`
	*p = r
}

func Append8b(n int, p *[]int) {
	var r []int
	// amd64:`.*moveSliceNoCapNoScan\b`
	*p = r
	for i := range n {
		// amd64:`.*growslice\b` -`.*growsliceNoAlias\b`
		r = append(r, i)
	}
}

func Append9(n int) []int {
	var r []int
	for i := range n {
		// amd64:`.*growsliceNoAlias\b`
		r = append(r, i)
	}
	println(len(r))
	// amd64:`.*moveSliceNoCapNoScan\b`
	return r
}

func Append10(n int) []int {
	var r []int
	for i := range n {
		// amd64:`.*growsliceNoAlias\b`
		r = append(r, i)
	}
	println(r[3])
	// amd64:`.*moveSliceNoCapNoScan\b`
	return r
}

func Append11(n int) []int {
	var r []int
	for i := range n {
		// amd64:`.*growsliceBufNoAlias\b`
		r = append(r, i)
	}
	r = r[3:5]
	// amd64:`.*moveSliceNoScan\b`
	return r
}

func Append12(n int) []int {
	var r []int
	r = nil
	for i := range n {
		// amd64:`.*growslice\b`
		r = append(r, i)
	}
	// amd64:`.*moveSliceNoCapNoScan\b`
	return r
}

func Append13(n int) []int {
	var r []int
	r, r = nil, nil
	for i := range n {
		// amd64:`.*growslice\b`
		r = append(r, i)
	}
	// amd64:`.*moveSliceNoCapNoScan\b`
	return r
}

func Append14(n int) []int {
	var r []int
	r = []int{3, 4, 5}
	for i := range n {
		// amd64:`.*growsliceBufNoAlias\b`
		r = append(r, i)
	}
	// amd64:`.*moveSliceNoScan\b`
	return r
}

func Append15(n int) []int {
	r := []int{3, 4, 5}
	for i := range n {
		// amd64:`.*growsliceBufNoAlias\b`
		r = append(r, i)
	}
	// amd64:`.*moveSliceNoScan\b`
	return r
}

func Append16(r []int, n int) []int {
	for i := range n {
		// amd64:`.*growslice\b`
		r = append(r, i)
	}
	// amd64:`.*moveSliceNoCapNoScan\b`
	return r
}

func Append17(n int) []int {
	var r []int
	for i := range n {
		// amd64:`.*growsliceNoAlias\b`
		r = append(r, i)
	}
	for i, x := range r {
		println(i, x)
	}
	// "range r" keeps pointer to backing store of r. Our analysis
	// is not yet precise enough to know it is OK in this case, so
	// currently no `moveSliceNoCapNoScan` here. See #79909.
	// amd64:-`.*moveSlice`
	return r
}

func Append21(n int) []int {
	var r []int
	for i := range n {
		// amd64:`.*growsliceNoAlias\b`
		r = append(r, i)
	}
	// amd64:`.*moveSliceNoCapNoScan\b`
	return r
}

func Append22(n int) int {
	// amd64:-`.*moveSlice`
	return Append21(n)[0]
}

func Append18(n int) ([]int, []int) {
	var r, old []int
	for i := range n {
		// amd64:`.*growslice\b` -`.*growsliceNoAlias\b`
		r, old = append(r, i), r
	}
	return r, old
}

func Append18b(n int) ([]int, []int) {
	r := make([]int, n, n)
	var old []int

	// amd64:`.*growslice(Buf)?\b` -`.*growsliceNoAlias\b`
	r, old = append(r, n), r
	return r, old
}

func Append19(n int) ([]int, int) {
	var r []int
	var last int
	for i := range n {
		// amd64:`.*growsliceNoAlias\b`
		r, last = append(r, i), i
	}
	return r, last
}

func Append20(n int) []int {
	var r []int
	for i := range n {
		// amd64:2`.*growslice\b` -`.*growsliceNoAlias\b`
		r, r = append(r, i), append(r, i+1)
	}
	return r
}

func Append24(n int) ([]int, []int) {
	var r, s []int
	for i := range n {
		// amd64:2`.*growsliceNoAlias\b` -`.*growslice\b`
		r, s = append(r, i), append(s, i)
	}
	return r, s
}

func Append25(n int) []int {
	r := []int{0}
	for i := range n {
		// amd64:`.*growslice(Buf)?\b` -`.*growsliceNoAlias\b`
		r, r[0] = append(r, i), i
	}
	return r
}

func Append26(n int) ([]int, []int) {
	var r, s []int
	for i := range n {
		// amd64:2`.*growslice\b` 1`.*growsliceNoAlias\b`
		r, r, s = append(r, i), append(r, i+1), append(s, i)
	}
	return r, s
}

//go:noinline
func useSlice(s []int) {
}
