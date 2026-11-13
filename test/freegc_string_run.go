// run

//go:build goexperiment.runtimefreegc

// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Test that string concatenations that extend or free their first operand
// never change or free a string that is still in use.

package main

import (
	"fmt"
	"runtime"
	"runtime/metrics"
	"strings"
	"sync"
)

//go:noinline
func join(xs []string) string {
	var s string
	for _, x := range xs {
		s += x
	}
	return s
}

//go:noinline
func joinSep(xs []string) string {
	s := "["
	for _, x := range xs {
		s += x + ","
	}
	return s
}

//go:noinline
func fromBytes(b []byte, xs []string) string {
	s := string(b)
	for _, x := range xs {
		s += x
	}
	return s
}

var saved []string

// joinSaving stores prefixes of s, which must then never change.
//
//go:noinline
func joinSaving(xs []string) string {
	var s string
	for i, x := range xs {
		s += x
		if i%7 == 0 {
			saved = append(saved, s)
		}
	}
	return s
}

// countA reads s between concatenations.
//
//go:noinline
func countA(xs []string) int {
	var s string
	n := 0
	for _, x := range xs {
		s += x
		n += strings.Count(s, "a")
	}
	return n
}

func words(n, seed int) []string {
	xs := make([]string, n)
	for i := range xs {
		k := 1 + (i*7+seed)%40
		xs[i] = strings.Repeat(string(rune('a'+(i+seed)%26)), k)
	}
	return xs
}

func wantCountA(xs []string) int {
	n, as := 0, 0
	for _, x := range xs {
		as += strings.Count(x, "a")
		n += as
	}
	return n
}

func check(seed int, results map[int][2]string) {
	xs := words(50+seed%150, seed)
	r := [2]string{join(xs), joinSep(xs)}
	if want := strings.Join(xs, ""); r[0] != want {
		panic(fmt.Sprintf("join(seed %d) = %q, want %q", seed, r[0], want))
	}
	if want := "[" + strings.Join(xs, ",") + ","; r[1] != want {
		panic(fmt.Sprintf("joinSep(seed %d) = %q, want %q", seed, r[1], want))
	}
	if got, want := countA(xs), wantCountA(xs); got != want {
		panic(fmt.Sprintf("countA(seed %d) = %d, want %d", seed, got, want))
	}
	if results != nil {
		results[seed] = r
	}
}

func explicitFrees() uint64 {
	runtime.GC() // flush per-P counts
	s := []metrics.Sample{{Name: "/gc/heap/explicit-frees:objects"}}
	metrics.Read(s)
	return s[0].Value.Uint64()
}

func main() {
	before := explicitFrees()

	results := make(map[int][2]string)
	for seed := range 500 {
		check(seed, results)
	}
	// Later concatenations must not have changed earlier results.
	for seed, r := range results {
		xs := words(50+seed%150, seed)
		if r[0] != strings.Join(xs, "") || r[1] != "["+strings.Join(xs, ",")+"," {
			panic(fmt.Sprintf("result for seed %d changed", seed))
		}
	}

	xs := words(300, 1)
	b := []byte(strings.Repeat("q", 40))
	if got, want := fromBytes(b, xs), string(b)+strings.Join(xs, ""); got != want {
		panic(fmt.Sprintf("fromBytes = %q, want %q", got, want))
	}
	if string(b) != strings.Repeat("q", 40) {
		panic("fromBytes changed its argument")
	}

	joinSaving(xs)
	for i, s := range saved {
		if want := strings.Join(xs[:7*i+1], ""); s != want {
			panic(fmt.Sprintf("saved[%d] = %q, want %q", i, s, want))
		}
	}

	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for seed := range 200 {
				check(1000*g+seed, nil)
			}
		}()
	}
	wg.Wait()

	if d := explicitFrees() - before; d < 1000 {
		panic(fmt.Sprintf("only %d explicit frees", d))
	}
}
