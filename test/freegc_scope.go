// errorcheck -0 -d=freegcscope=2

//go:build goexperiment.runtimefreegc

// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Test which makes free their heap object when its scope ends.

package p

func perIteration(n, m int) (t int) {
	for range m {
		buf := make([]byte, n) // ERROR "freegc: make frees its previous object and on return"
		for _, b := range buf {
			t += int(b)
		}
	}
	return t
}

func perCall(n int) int {
	buf := make([]int, n) // ERROR "freegc: make frees its previous object and on return"
	return len(buf)
}

var sink []byte

func escapes(n int) {
	buf := make([]byte, n)
	sink = buf
}

func returned(n int) []byte {
	return make([]byte, n)
}

func outlivesIteration(n, m int) int {
	var last []byte
	for range m {
		buf := make([]byte, n)
		last = buf
	}
	return len(last)
}

func pointers(n int) int {
	buf := make([]*int, n)
	return len(buf)
}

func appended(n int) int {
	buf := make([]int, 0, n)
	buf = append(buf, 1)
	return len(buf)
}

func constantSize() int {
	buf := make([]byte, 16)
	return len(buf)
}

// Like the child side of a fork, a norace function may run where it must
// not call into the runtime.
//
//go:norace
func noRace(n int) int {
	buf := make([]byte, n)
	return len(buf)
}
