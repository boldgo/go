// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"runtime"
	"strings"
	"testing"
	"unsafe"
)

func TestConcatStringNoAlias(t *testing.T) {
	if !runtime.RuntimeFreegcEnabled {
		t.Skip("requires GOEXPERIMENT=runtimefreegc")
	}
	if runtime.FreegcCheck() != 0 {
		t.Skip("frees strings the test still references")
	}

	// A 20-byte string in a 24-byte object is extended in place.
	a := strings.Clone(strings.Repeat("a", 20))
	p := uintptr(unsafe.Pointer(unsafe.StringData(a)))
	r := runtime.ConcatString2NoAlias(a, "bcd")
	if want := strings.Repeat("a", 20) + "bcd"; r != want {
		t.Fatalf("got %q, want %q", r, want)
	}
	if uintptr(unsafe.Pointer(unsafe.StringData(r))) != p {
		t.Errorf("not extended in place")
	}

	// Without room, the result is a new string.
	long := strings.Repeat("x", 100)
	r = runtime.ConcatString2NoAlias(r, long)
	if want := strings.Repeat("a", 20) + "bcd" + long; r != want {
		t.Fatalf("got %q, want %q", r, want)
	}
	if uintptr(unsafe.Pointer(unsafe.StringData(r))) == p {
		t.Errorf("123 bytes built in a 24-byte object")
	}

	// The result is never another operand, even when a0 is empty.
	r = runtime.ConcatString2NoAlias("", long)
	if r != long {
		t.Fatalf("got %q, want %q", r, long)
	}
	if unsafe.StringData(r) == unsafe.StringData(long) {
		t.Errorf("returned the second operand")
	}

	// Strings shorter than 16 bytes may share a tiny block, so they are
	// never extended in place.
	short := strings.Clone("abc")
	r = runtime.ConcatString2NoAlias(short, "d")
	if r != "abcd" {
		t.Fatalf("got %q, want %q", r, "abcd")
	}
	if unsafe.StringData(r) == unsafe.StringData(short) {
		t.Errorf("extended a tiny string in place")
	}

	// Appending a string to itself.
	s := strings.Clone(strings.Repeat("s", 17))
	s = runtime.ConcatString2NoAlias(s, s)
	if want := strings.Repeat("s", 34); s != want {
		t.Fatalf("got %q, want %q", s, want)
	}
}
