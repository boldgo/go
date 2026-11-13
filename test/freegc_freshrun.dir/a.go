// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package a

import "strings"

var Cache = []byte(strings.Repeat("cache-", 20))

// Copy returns a fresh copy of Cache.
//
//go:noinline
func Copy() []byte {
	c := make([]byte, len(Cache))
	copy(c, Cache)
	return c
}

// Shared returns Cache itself.
//
//go:noinline
func Shared() []byte {
	return Cache[:len(Cache):len(Cache)]
}

var Name = strings.Repeat("name-", 10)

//go:noinline
func Label(n int) string {
	return string(Cache[:n])
}

//go:noinline
func SharedName() string {
	return Name
}
