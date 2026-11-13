// errorcheck -0 -d=freegcssa=2

//go:build goexperiment.runtimefreegc && (amd64 || arm64)

// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Test which string concatenations the SSA ownership pass lets free or
// extend their first operand.

package p

func fromEmpty(xs []string) string {
	var s string
	for _, x := range xs {
		s += x // ERROR "ssa ownership: frees old string"
	}
	return s
}

func fromLiteral(xs []string) string {
	s := "prefix: "
	for _, x := range xs {
		s += x // ERROR "ssa ownership: frees old string"
	}
	return s
}

func fromBytes(b []byte, xs []string) string {
	s := string(b)
	for _, x := range xs {
		s += x // ERROR "ssa ownership: frees old string"
	}
	return s
}

func threeOperands(xs []string) string {
	var s string
	for _, x := range xs {
		s += x + "," // ERROR "ssa ownership: frees old string"
	}
	return s
}

//go:noinline
func count(s string, c byte) int {
	n := 0
	for i := range len(s) {
		if s[i] == c {
			n++
		}
	}
	return n
}

func passedToNonRetainingCall(xs []string) int {
	var s string
	n := 0
	for _, x := range xs {
		s += x // ERROR "ssa ownership: frees old string"
		n += count(s, 'a')
	}
	return n
}

func param(s string, xs []string) string {
	for _, x := range xs {
		s += x
	}
	return s
}

func fromConcat(a, b string, xs []string) string {
	s := a + b
	for _, x := range xs {
		s += x
	}
	return s
}

var sink string

func stored(xs []string) {
	var s string
	for _, x := range xs {
		s += x
		sink = s
	}
}

//go:noinline
func keep(s string) {
	sink = s
}

func passedToRetainingCall(xs []string) {
	var s string
	for _, x := range xs {
		s += x
		keep(s)
	}
}

func oldStillUsed(xs []string) int {
	var s string
	n := 0
	for _, x := range xs {
		t := s
		s += x
		n += len(t) + count(t, 'a')
	}
	return n + len(s)
}

func substring(xs []string) string {
	var s string
	for _, x := range xs {
		s = s[:len(s)/2] + x
	}
	return s
}

func prepend(xs []string) string {
	var s string
	for _, x := range xs {
		s = x + s
	}
	return s
}
