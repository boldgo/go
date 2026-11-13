// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"strings"

	"./a"
)

//go:noinline
func extend(b []byte, n int) []byte {
	for i := range n {
		b = append(b, byte('a'+i%26))
	}
	return b
}

//go:noinline
func extendCopy(n int) []byte {
	b := a.Copy()
	for i := range n {
		b = append(b, byte('a'+i%26))
	}
	return b
}

//go:noinline
func extendShared(n int) []byte {
	b := a.Shared()
	for i := range n {
		b = append(b, byte('a'+i%26))
	}
	return b
}

//go:noinline
func label(n int, xs []string) string {
	s := a.Label(n)
	for _, x := range xs {
		s += x
	}
	return s
}

//go:noinline
func greet(xs []string) string {
	s := a.SharedName()
	for _, x := range xs {
		s += x
	}
	return s
}

func main() {
	cache := string(a.Cache)
	name := a.Name
	xs := []string{"one", "two", "three", strings.Repeat("x", 50)}
	for n := range 300 {
		want := cache + string(extend(nil, n))
		if got := string(extendCopy(n)); got != want {
			panic(fmt.Sprintf("extendCopy(%d) = %q, want %q", n, got, want))
		}
		if got := string(extendShared(n)); got != want {
			panic(fmt.Sprintf("extendShared(%d) = %q, want %q", n, got, want))
		}
		k := 20 + n%100
		if got, want := label(k, xs), cache[:k]+strings.Join(xs, ""); got != want {
			panic(fmt.Sprintf("label(%d) = %q, want %q", k, got, want))
		}
		if got, want := greet(xs), name+strings.Join(xs, ""); got != want {
			panic(fmt.Sprintf("greet = %q, want %q", got, want))
		}
		if string(a.Cache) != cache || a.Name != name {
			panic("a's state changed")
		}
	}
}
