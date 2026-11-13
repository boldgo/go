// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package b

import "./a"

func Label(n int, xs []string) string {
	s := a.Itoa(n)
	for _, x := range xs {
		s += x // ERROR "ssa ownership: frees old string"
	}
	return s // ERROR "ssa ownership: result 0 is fresh"
}

func Extend(b []byte, n int) []byte {
	c := a.Clone(b)
	for i := range n {
		c = append(c, byte(i)) // ERROR "ssa ownership: frees old backing store"
	}
	return c // ERROR "ssa ownership: result 0 is fresh"
}

func ExtendCached(n int) []byte {
	c := a.Cached()
	for i := range n {
		c = append(c, byte(i))
	}
	return c
}

func Greet(xs []string) string {
	s := a.Name()
	for _, x := range xs {
		s += x
	}
	return s
}
