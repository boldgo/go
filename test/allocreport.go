// errorcheck -0 -d=ssa/alloc_report/debug=1

// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Test the allocation report.

package p

type T struct{ a, b *int }

var (
	sink    any
	sinkT   *T
	sinkS   []int
	sinkStr string
	sinkB   []byte
	sinkF   func() int
	sinkM   map[int]int
)

func newT() {
	sinkT = &T{} // ERROR "allocates: new p.T$"
}

func makeSlice(n int) {
	sinkS = make([]int, n) // ERROR "allocates: make \[\]int$"
}

func grow(s []int, x int) []int {
	return append(s, x) // ERROR "allocates: append grows slice"
}

func box(x T) {
	sink = x // ERROR "allocates: (convert p.T to interface|new p.T)$"
}

func concat(a, b string) {
	sinkStr = a + b // ERROR "allocates: string concatenation$"
}

func concatLocal(a, b string) int {
	s := a + b // ERROR "allocates: string concatenation \(on the stack if at most 32 bytes\)$"
	return len(s)
}

func closure(n int) {
	sinkF = func() int { return n } // ERROR "allocates: closure$"
}

func toString(b []byte) {
	sinkStr = string(b) // ERROR "allocates: string\(\[\]byte\)$"
}

func toBytes(s string) {
	sinkB = []byte(s) // ERROR "allocates: \[\]byte\(string\)$"
}

func h() {}

func goStmt() {
	go h() // ERROR "allocates: go statement$"
}

func makeMap() {
	sinkM = make(map[int]int) // ERROR "allocates: make map$"
}

func onStack() int {
	x := new(int)
	*x = 1
	return *x
}
