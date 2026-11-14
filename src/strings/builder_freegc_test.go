// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.runtimefreegc && !race

package strings_test

import (
	"runtime"
	"runtime/metrics"
	. "strings"
	"testing"
)

func explicitFrees() uint64 {
	runtime.GC() // flush per-P counts
	s := []metrics.Sample{{Name: "/gc/heap/explicit-frees:objects"}}
	metrics.Read(s)
	return s[0].Value.Uint64()
}

var builderSink []byte

// churn allocates and overwrites objects of many size classes, so that a
// buffer freed while still in use would be handed out and clobbered.
// Under GODEBUG=freegccheck, which never reuses freed objects, the GC
// that follows reports it instead.
func churn() {
	for size := 16; size <= 1<<16; size *= 2 {
		for range 8 {
			p := make([]byte, size)
			for i := range p {
				p[i] = 'x'
			}
			builderSink = p
		}
	}
	runtime.GC()
}

func TestBuilderFreesOldBuffers(t *testing.T) {
	const word = "0123456789"
	before := explicitFrees()
	var b Builder
	for range 10000 {
		b.WriteString(word)
	}
	if got := explicitFrees() - before; got < 5 {
		t.Errorf("growing a Builder to %d bytes freed %d old buffers, want at least 5", b.Len(), got)
	}
	churn()
	if b.String() != Repeat(word, 10000) {
		t.Errorf("Builder contents changed")
	}
}

func TestBuilderKeepsSharedBuffers(t *testing.T) {
	const word = "0123456789"
	var b Builder
	var got []string
	for range 500 {
		b.WriteString(word)
		got = append(got, b.String())
	}
	b.Grow(1 << 16)
	got = append(got, b.String())
	b.WriteString(word)
	churn()
	for i, s := range got[:500] {
		if s != Repeat(word, i+1) {
			t.Fatalf("string %d returned by String changed", i)
		}
	}
	if got[500] != got[499] {
		t.Fatalf("string returned before Grow changed")
	}
	if b.String() != Repeat(word, 501) {
		t.Errorf("Builder contents changed")
	}
}

// TestBuilderStringOfCopy checks that a string from a copy of a Builder,
// which a write would reject but String accepts, survives the Builder it
// was copied from growing.
func TestBuilderStringOfCopy(t *testing.T) {
	const word = "0123456789"
	var b Builder
	for range 100 {
		b.WriteString(word)
	}
	c := b
	s := c.String()
	for range 1000 {
		b.WriteString(word)
	}
	churn()
	if s != Repeat(word, 100) {
		t.Fatalf("string returned by a copy of a Builder changed")
	}
}
