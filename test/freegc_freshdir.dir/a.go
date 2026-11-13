// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package a

//go:noinline
func Itoa(n int) string {
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

//go:noinline
func Clone(b []byte) []byte {
	c := make([]byte, len(b))
	copy(c, b)
	return c // ERROR "ssa ownership: result 0 is fresh"
}

var cache = make([]byte, 100)

//go:noinline
func Cached() []byte {
	return cache
}

var name string

//go:noinline
func Name() string {
	return name
}
