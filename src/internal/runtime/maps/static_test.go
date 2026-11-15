// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package maps_test

import (
	"encoding/binary"
	"fmt"
	"internal/runtime/maps"
	stdmaps "maps"
	"math/bits"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

// refStaticHash is staticHash written plainly, as the init image
// evaluator computes it.
func refStaticHash(p []byte) uint64 {
	const (
		key0 = 0x243f6a8885a308d3
		key1 = 0x13198a2e03707344
		key2 = 0xa4093822299f31d0
		key3 = 0x082efa98ec4e6c89
		key4 = 0x452821e638d01377
	)
	mix := func(a, b uint64) uint64 {
		hi, lo := bits.Mul64(a, b)
		return hi ^ lo
	}
	r4 := func(b []byte) uint64 { return uint64(binary.LittleEndian.Uint32(b)) }
	r8 := binary.LittleEndian.Uint64
	n := len(p)
	seed := uint64(key0)
	var a, b uint64
	switch {
	case n == 0:
		return seed
	case n < 4:
		a = uint64(p[0]) | uint64(p[n>>1])<<8 | uint64(p[n-1])<<16
	case n <= 8:
		a, b = r4(p), r4(p[n-4:])
		if n == 8 {
			a, b = r8(p), r8(p)
		}
	case n <= 16:
		a, b = r8(p), r8(p[n-8:])
	default:
		off, l := 0, n
		if l > 48 {
			seed1, seed2 := seed, seed
			for ; l > 48; l -= 48 {
				seed = mix(r8(p[off:])^key1, r8(p[off+8:])^seed)
				seed1 = mix(r8(p[off+16:])^key2, r8(p[off+24:])^seed1)
				seed2 = mix(r8(p[off+32:])^key3, r8(p[off+40:])^seed2)
				off += 48
			}
			seed ^= seed1 ^ seed2
		}
		for ; l > 16; l -= 16 {
			seed = mix(r8(p[off:])^key1, r8(p[off+8:])^seed)
			off += 16
		}
		a, b = r8(p[off+l-16:]), r8(p[off+l-8:])
	}
	return mix(key4^uint64(n), mix(a^key1, b^seed))
}

func TestStaticHash(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	buf := make([]byte, 160)
	for range 20 {
		for i := range buf {
			buf[i] = byte(r.Uint32())
		}
		for n := range len(buf) - 8 {
			for off := range 8 {
				b := buf[off : off+n]
				var p unsafe.Pointer
				if n > 0 {
					p = unsafe.Pointer(&b[0])
				}
				if got, want := maps.StaticHash(p, uintptr(n)), refStaticHash(b); got != want {
					t.Fatalf("StaticHash(%x) = %#x, want %#x", b, got, want)
				}
			}
		}
		k64 := r.Uint64()
		if got, want := maps.StaticHash64(k64), refStaticHash((*[8]byte)(unsafe.Pointer(&k64))[:]); got != want {
			t.Fatalf("StaticHash64(%#x) = %#x, want %#x", k64, got, want)
		}
		k32 := r.Uint32()
		if got, want := maps.StaticHash32(k32), refStaticHash((*[4]byte)(unsafe.Pointer(&k32))[:]); got != want {
			t.Fatalf("StaticHash32(%#x) = %#x, want %#x", k32, got, want)
		}
	}
}

type bigKey [130]byte

func itoa(i int) string { return fmt.Sprint(i) }

func TestStaticMap(t *testing.T) {
	for _, n := range []int{0, 1, 2, 7, 8, 9, 100, 1000} {
		t.Run(fmt.Sprint("string-int/", n), func(t *testing.T) {
			testStatic(t, n, func(i int) string { return fmt.Sprint("key", i, strings.Repeat("x", i%20)) }, func(i int) int { return i * 3 })
		})
	}
	t.Run("string-int/70000", func(t *testing.T) {
		testStatic(t, 70000, func(i int) string { return fmt.Sprint(i) }, func(i int) int { return -i })
	})
	t.Run("int64-string", func(t *testing.T) {
		testStatic(t, 50, func(i int) int64 { return int64(i) * -1e12 }, itoa)
	})
	t.Run("uint32-bool", func(t *testing.T) {
		testStatic(t, 50, func(i int) uint32 { return uint32(i) * 0x01000193 }, func(i int) bool { return i%2 == 0 })
	})
	t.Run("int8-int", func(t *testing.T) {
		testStatic(t, 40, func(i int) int8 { return int8(i - 20) }, func(i int) int { return i })
	})
	t.Run("uint16-struct", func(t *testing.T) {
		testStatic(t, 30, func(i int) uint16 { return uint16(i * 1000) }, func(int) struct{} { return struct{}{} })
	})
	t.Run("string-struct", func(t *testing.T) {
		testStatic(t, 30, itoa, func(int) struct{} { return struct{}{} })
	})
	t.Run("string-big", func(t *testing.T) {
		testStatic(t, 20, itoa, func(i int) (v [200]byte) { v[i] = 1; return })
	})
	t.Run("array-int", func(t *testing.T) {
		testStatic(t, 20, func(i int) [2]int32 { return [2]int32{int32(i), -int32(i)} }, func(i int) int { return i })
	})
	t.Run("bigkey-int", func(t *testing.T) {
		testStatic(t, 20, func(i int) (k bigKey) { k[129-i] = byte(i); return }, func(i int) int { return i })
	})
	t.Run("uintptr-string", func(t *testing.T) {
		testStatic(t, 20, func(i int) uintptr { return uintptr(i) << 20 }, itoa)
	})
	t.Run("bool-int", func(t *testing.T) {
		testStatic(t, 1, func(i int) bool { return i != 0 }, func(i int) int { return i + 1 })
	})
}

// testStatic checks a static map of n entries, key(i) to val(i), against
// the ordinary map. key(i) for i in [n, n+2) are not in it.
func testStatic[K, V comparable](t *testing.T, n int, key func(int) K, val func(int) V) {
	src := map[K]V{}
	for i := range n {
		src[key(i)] = val(i)
	}
	if len(src) != n {
		t.Fatalf("keys are not distinct")
	}
	absent, extra := key(n), key(n+1)
	static := func() map[K]V {
		m := maps.NewStaticMap(src)
		if !maps.IsStatic(m) {
			t.Fatalf("not a static map")
		}
		return m
	}
	same := func(m map[K]V, want map[K]V) {
		t.Helper()
		if len(m) != len(want) {
			t.Fatalf("len = %d, want %d", len(m), len(want))
		}
		for k, v := range want {
			if got, ok := m[k]; !ok || got != v {
				t.Fatalf("m[%v] = %v, %v, want %v", k, got, ok, v)
			}
		}
		got := map[K]V{}
		for k, v := range m {
			if _, dup := got[k]; dup {
				t.Fatalf("range returned %v twice", k)
			}
			got[k] = v
		}
		if !stdmaps.Equal(got, want) {
			t.Fatalf("range returned %v, want %v", got, want)
		}
	}

	m := static()
	same(m, src)
	var zero V
	if v, ok := m[absent]; ok || v != zero {
		t.Fatalf("m[absent] = %v, %v", v, ok)
	}
	rv := reflect.ValueOf(m)
	if rv.Len() != n || len(rv.MapKeys()) != n {
		t.Fatalf("reflect: len %d, %d keys", rv.Len(), len(rv.MapKeys()))
	}
	for it := rv.MapRange(); it.Next(); {
		if src[it.Key().Interface().(K)] != it.Value().Interface().(V) {
			t.Fatalf("reflect: %v: %v", it.Key(), it.Value())
		}
	}
	if rv.MapIndex(reflect.ValueOf(absent)).IsValid() {
		t.Fatalf("reflect: absent key found")
	}
	delete(m, absent)
	if !maps.IsStatic(m) {
		t.Fatalf("deleting an absent key made the map ordinary")
	}
	if n >= 2 {
		first := map[K]bool{}
		for range 200 {
			for k := range m {
				first[k] = true
				break
			}
		}
		if len(first) < 2 {
			t.Fatalf("iteration always starts at the same key")
		}
	}

	c := stdmaps.Clone(m)
	if !maps.IsStatic(c) {
		t.Fatalf("clone is not static")
	}
	c[extra] = val(n + 1)
	if maps.IsStatic(c) || !maps.IsStatic(m) {
		t.Fatalf("assigning to the clone: clone static %v, map static %v", maps.IsStatic(c), maps.IsStatic(m))
	}
	want := stdmaps.Clone(src)
	want[extra] = val(n + 1)
	same(c, want)
	same(m, src)

	m = static()
	m[extra] = val(n + 1)
	same(m, want)

	m = static()
	reflect.ValueOf(m).SetMapIndex(reflect.ValueOf(extra), reflect.ValueOf(val(n+1)))
	same(m, want)

	m = static()
	clear(m)
	same(m, map[K]V{})
	m[extra] = val(n + 1)
	same(m, map[K]V{extra: val(n + 1)})

	if n == 0 {
		return
	}
	k0 := key(0)
	m = static()
	delete(m, k0)
	want = stdmaps.Clone(src)
	delete(want, k0)
	same(m, want)

	// Deleting entries during iteration: none not yet reached is returned.
	m = static()
	count := 0
	for k := range m {
		count++
		for k2 := range src {
			if k2 != k {
				delete(m, k2)
			}
		}
	}
	if count != 1 || len(m) != 1 {
		t.Fatalf("deleting all others during range: %d returned, len %d", count, len(m))
	}

	// Inserting during iteration: every entry is still returned once.
	m = static()
	seen := map[K]int{}
	for k := range m {
		seen[k]++
		m[extra] = val(n + 1)
	}
	for k := range src {
		if seen[k] != 1 {
			t.Fatalf("inserting during range: %v returned %d times", k, seen[k])
		}
	}

	// Updating during iteration: entries not yet reached have the new value.
	m = static()
	for k := range m {
		for k2 := range src {
			if k2 != k {
				m[k2] = val(n + 1)
			}
		}
		for k2, v := range m {
			if k2 != k && v != val(n+1) {
				t.Fatalf("updating during range: %v is %v", k2, v)
			}
		}
		break
	}

	// Clearing during iteration ends it.
	m = static()
	count = 0
	for range m {
		count++
		clear(m)
	}
	if count != 1 {
		t.Fatalf("clearing during range: %d returned", count)
	}
}

func BenchmarkStaticMapAccess(b *testing.B) {
	for _, n := range []int{8, 1000} {
		src := map[string]int{}
		keys := []string{}
		for i := range n {
			k := fmt.Sprint("key-number-", i)
			src[k] = i
			keys = append(keys, k)
		}
		for _, kind := range []string{"ordinary", "static"} {
			m := src
			if kind == "static" {
				m = maps.NewStaticMap(src)
			}
			b.Run(fmt.Sprint(kind, "/", n), func(b *testing.B) {
				s := 0
				for i := range b.N {
					s += m[keys[i%n]]
				}
				staticSink = s
			})
		}
	}
}

var staticSink int
