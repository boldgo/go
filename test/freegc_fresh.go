// errorcheck -0 -d=freegcssa=2,freegcfresh=1

//go:build goexperiment.runtimefreegc && (amd64 || arm64)

// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Test which results the SSA ownership pass proves fresh.

package p

func itoa(n int) string {
	var b [20]byte
	i := len(b)
	for {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
		if n == 0 {
			break
		}
	}
	return string(b[i:]) // ERROR "ssa ownership: result 0 is fresh"
}

func join(xs []string) string {
	var s string
	for _, x := range xs {
		s += x // ERROR "ssa ownership: frees old string"
	}
	return s // ERROR "ssa ownership: result 0 is fresh"
}

func clone(b []byte) []byte {
	c := make([]byte, len(b))
	copy(c, b)
	return c // ERROR "ssa ownership: result 0 is fresh"
}

func grow(n int) ([]int, error) {
	var b []int
	for i := range n {
		b = append(b, i)
	}
	return b, nil // ERROR "ssa ownership: result 0 is fresh"
}

func shortened(n int) []byte {
	b := make([]byte, n)
	return b[:n/2] // ERROR "ssa ownership: result 0 is fresh"
}

func ident(b []byte) []byte {
	return b
}

var global []byte

func stored(n int) []byte {
	b := make([]byte, n)
	global = b
	return b
}

func twice(n int) ([]byte, []byte) {
	b := make([]byte, n)
	return b, b
}

func offset(n int) []byte {
	b := make([]byte, n)
	return b[1:]
}

func pointers(n int) []*int {
	return make([]*int, n)
}

var cache string

func cached() string {
	return cache
}

func plain(a, b string) string {
	return a + b
}
