// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package maps

import (
	"internal/abi"
	"internal/goarch"
	"internal/runtime/sys"
	"math/bits"
	"unsafe"
)

// Static maps. An init image holds the maps its initializers built as
// static maps: a header whose writing field is staticWriting and whose
// dirPtr points to a staticTable laid out at link time and never written.
// Lookups and iteration read the table, and the first write turns the map
// into an ordinary one in place. Every map operation already tests
// writing, so ordinary maps pay nothing for this.
//
// The table's hash, staticHash, has fixed keys. The table's keys are fixed
// when the program is linked, so no input can lengthen its probes. Static
// keys are made of strings, integers and bools: staticTypeHash hashes
// their bytes.

const staticWriting = 0x80

// staticIter is Iter.dirIdx for an iterator over a static map.
const staticIter = -2

// staticWide is the smallest entry count whose offsets are uint32s.
const staticWide = 1 << 16

// A staticTable holds its entries in hash buckets: bucket b, the low bits
// of a hash, holds entries off[b] to off[b+1]. Keys and elems are stored
// directly, even those a group would hold indirectly.
type staticTable struct {
	n     uintptr        // entries
	mask  uintptr        // buckets - 1, a power of two minus one
	keys  unsafe.Pointer // [n]key
	elems unsafe.Pointer // [n]elem
	tags  unsafe.Pointer // [n]uint8, the top byte of each key's hash
	off   unsafe.Pointer // [mask+2]uint16, or uint32 if n >= staticWide
}

const (
	staticKey0 = 0x243f6a8885a308d3
	staticKey1 = 0x13198a2e03707344
	staticKey2 = 0xa4093822299f31d0
	staticKey3 = 0x082efa98ec4e6c89
	staticKey4 = 0x452821e638d01377
)

func staticMix(a, b uint64) uint64 {
	hi, lo := bits.Mul64(a, b)
	return hi ^ lo
}

func load32(p unsafe.Pointer) uint32 {
	b := (*[4]byte)(p)
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

func load64(p unsafe.Pointer) uint64 {
	b := (*[8]byte)(p)
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
}

// staticHash hashes the n bytes at p. It is memHashFallback's wyhash with
// fixed keys, 64-bit on every platform, reading little endian.
func staticHash(p unsafe.Pointer, n uintptr) uint64 {
	var a, b uint64
	seed := uint64(staticKey0)
	switch {
	case n == 0:
		return seed
	case n < 4:
		a = uint64(*(*byte)(p)) | uint64(*(*byte)(unsafe.Add(p, n>>1)))<<8 | uint64(*(*byte)(unsafe.Add(p, n-1)))<<16
	case n == 4:
		a = uint64(load32(p))
		b = a
	case n < 8:
		a = uint64(load32(p))
		b = uint64(load32(unsafe.Add(p, n-4)))
	case n == 8:
		a = load64(p)
		b = a
	case n <= 16:
		a = load64(p)
		b = load64(unsafe.Add(p, n-8))
	default:
		l := n
		if l > 48 {
			seed1, seed2 := seed, seed
			for ; l > 48; l -= 48 {
				seed = staticMix(load64(p)^staticKey1, load64(unsafe.Add(p, 8))^seed)
				seed1 = staticMix(load64(unsafe.Add(p, 16))^staticKey2, load64(unsafe.Add(p, 24))^seed1)
				seed2 = staticMix(load64(unsafe.Add(p, 32))^staticKey3, load64(unsafe.Add(p, 40))^seed2)
				p = unsafe.Add(p, 48)
			}
			seed ^= seed1 ^ seed2
		}
		for ; l > 16; l -= 16 {
			seed = staticMix(load64(p)^staticKey1, load64(unsafe.Add(p, 8))^seed)
			p = unsafe.Add(p, 16)
		}
		a = load64(unsafe.Add(p, l-16))
		b = load64(unsafe.Add(p, l-8))
	}
	return staticMix(staticKey4^uint64(n), staticMix(a^staticKey1, b^seed))
}

func staticHash64(k uint64) uint64 {
	if goarch.BigEndian {
		k = sys.Bswap64(k)
	}
	return staticMix(staticKey4^8, staticMix(k^staticKey1, k^staticKey0))
}

func staticHash32(k uint32) uint64 {
	if goarch.BigEndian {
		k = sys.Bswap32(k)
	}
	a := uint64(k)
	return staticMix(staticKey4^4, staticMix(a^staticKey1, a^staticKey0))
}

func staticHashStr(k string) uint64 {
	return staticHash(unsafe.Pointer(unsafe.StringData(k)), uintptr(len(k)))
}

func staticKeyHash(typ *abi.MapType, key unsafe.Pointer) uint64 {
	return staticTypeHash(typ.Key, key)
}

// staticTypeHash hashes the value of type t at p: as plain memory if t is
// plain memory, and otherwise by its strings' bytes and the hashes of its
// fields or elements, skipping blank fields as == does. A struct of one
// field and an array of one element hash as that field or element, as the
// compiler's choice of map functions treats them.
func staticTypeHash(t *abi.Type, p unsafe.Pointer) uint64 {
	if t.TFlag&abi.TFlagRegularMemory != 0 {
		return staticHash(p, t.Size_)
	}
	h := uint64(staticKey2)
	switch t.Kind() {
	case abi.String:
		return staticHashStr(*(*string)(p))
	case abi.Struct:
		fs := (*abi.StructType)(unsafe.Pointer(t)).Fields
		if len(fs) == 1 && !fs[0].Name.IsBlank() {
			return staticTypeHash(fs[0].Typ, p)
		}
		for _, f := range fs {
			if !f.Name.IsBlank() {
				h = staticMix(h^staticKey1, staticTypeHash(f.Typ, unsafe.Add(p, f.Offset))^staticKey3)
			}
		}
	case abi.Array:
		at := (*abi.ArrayType)(unsafe.Pointer(t))
		if at.Len == 1 {
			return staticTypeHash(at.Elem, p)
		}
		for i := range at.Len {
			h = staticMix(h^staticKey1, staticTypeHash(at.Elem, unsafe.Add(p, i*at.Elem.Size_))^staticKey3)
		}
	}
	return h
}

// bucket returns the entries of h's bucket.
func (s *staticTable) bucket(h uint64) (uintptr, uintptr) {
	b := uintptr(h) & s.mask
	if s.n < staticWide {
		o := (*[2]uint16)(unsafe.Add(s.off, b*2))
		return uintptr(o[0]), uintptr(o[1])
	}
	o := (*[2]uint32)(unsafe.Add(s.off, b*4))
	return uintptr(o[0]), uintptr(o[1])
}

func (s *staticTable) tag(e uintptr) uint8 {
	return *(*uint8)(unsafe.Add(s.tags, e))
}

func (s *staticTable) key(typ *abi.MapType, e uintptr) unsafe.Pointer {
	return unsafe.Add(s.keys, e*typ.Key.Size_)
}

func (s *staticTable) elem(typ *abi.MapType, e uintptr) unsafe.Pointer {
	return unsafe.Add(s.elems, e*typ.Elem.Size_)
}

// find returns the entry holding key.
func (s *staticTable) find(typ *abi.MapType, key unsafe.Pointer) (uintptr, bool) {
	h := staticKeyHash(typ, key)
	t := uint8(h >> 56)
	lo, hi := s.bucket(h)
	for e := lo; e < hi; e++ {
		if s.tag(e) == t && typ.Key.Equal(key, s.key(typ, e)) {
			return e, true
		}
	}
	return 0, false
}

// The staticGet functions are lookups in a non-empty map whose writing
// field is set: they serve a static map and report a concurrent write to
// any other.

func (m *Map) staticGet(typ *abi.MapType, key unsafe.Pointer) (unsafe.Pointer, bool) {
	if m.writing != staticWriting {
		fatal("concurrent map read and map write")
	}
	s := (*staticTable)(m.dirPtr)
	if i, ok := s.find(typ, key); ok {
		return s.elem(typ, i), true
	}
	return unsafe.Pointer(&zeroVal[0]), false
}

func (m *Map) staticGetStr(typ *abi.MapType, key string) (unsafe.Pointer, bool) {
	if m.writing != staticWriting {
		fatal("concurrent map read and map write")
	}
	s := (*staticTable)(m.dirPtr)
	h := staticHashStr(key)
	t := uint8(h >> 56)
	lo, hi := s.bucket(h)
	for e := lo; e < hi; e++ {
		if s.tag(e) == t && *(*string)(unsafe.Add(s.keys, e*unsafe.Sizeof(key))) == key {
			return s.elem(typ, e), true
		}
	}
	return unsafe.Pointer(&zeroVal[0]), false
}

func (m *Map) staticGet64(typ *abi.MapType, key uint64) (unsafe.Pointer, bool) {
	if m.writing != staticWriting {
		fatal("concurrent map read and map write")
	}
	s := (*staticTable)(m.dirPtr)
	lo, hi := s.bucket(staticHash64(key))
	for e := lo; e < hi; e++ {
		if *(*uint64)(unsafe.Add(s.keys, e*8)) == key {
			return s.elem(typ, e), true
		}
	}
	return unsafe.Pointer(&zeroVal[0]), false
}

func (m *Map) staticGet32(typ *abi.MapType, key uint32) (unsafe.Pointer, bool) {
	if m.writing != staticWriting {
		fatal("concurrent map read and map write")
	}
	s := (*staticTable)(m.dirPtr)
	lo, hi := s.bucket(staticHash32(key))
	for e := lo; e < hi; e++ {
		if *(*uint32)(unsafe.Add(s.keys, e*4)) == key {
			return s.elem(typ, e), true
		}
	}
	return unsafe.Pointer(&zeroVal[0]), false
}

func (m *Map) staticGetWithKey(typ *abi.MapType, key unsafe.Pointer) (unsafe.Pointer, unsafe.Pointer, bool) {
	if m.writing != staticWriting {
		fatal("concurrent map read and map write")
	}
	s := (*staticTable)(m.dirPtr)
	if i, ok := s.find(typ, key); ok {
		return s.key(typ, i), s.elem(typ, i), true
	}
	return nil, nil, false
}

// unstatic is called by a write to a map whose writing field is set: it
// makes a static map an ordinary one holding the same entries, and reports
// a concurrent write to any other.
func (m *Map) unstatic(typ *abi.MapType) {
	if m.writing != staticWriting {
		fatal("concurrent map writes")
	}
	s := (*staticTable)(m.dirPtr)
	*m = Map{}
	if s == nil {
		NewMap(typ, 0, m)
		return
	}
	NewMap(typ, s.n, m)
	for i := range s.n {
		typedmemmove(typ.Elem, m.PutSlot(typ, s.key(typ, i)), s.elem(typ, i))
	}
}

// unstaticDelete is unstatic for a delete of key from a non-empty map. A
// static map without key stays as it is. It reports whether the delete
// has work left.
func (m *Map) unstaticDelete(typ *abi.MapType, key unsafe.Pointer) bool {
	if m.writing == staticWriting {
		if _, ok := (*staticTable)(m.dirPtr).find(typ, key); !ok {
			return false
		}
	}
	m.unstatic(typ)
	return true
}

// unstaticClear is unstatic for a clear, which leaves an ordinary empty map.
func (m *Map) unstaticClear() {
	if m.writing != staticWriting {
		fatal("concurrent map writes")
	}
	*m = Map{seed: uintptr(rand()), clearSeq: m.clearSeq + 1}
}

func (it *Iter) initStatic(m *Map) {
	it.m = m
	it.dirIdx = staticIter
	it.group.data = m.dirPtr
	it.entryOffset = rand() % uint64((*staticTable)(m.dirPtr).n)
}

func (it *Iter) nextStatic() {
	s := (*staticTable)(it.group.data)
	for ; it.entryIdx < uint64(s.n); it.entryIdx++ {
		i := uintptr(it.entryIdx + it.entryOffset)
		if i >= s.n {
			i -= s.n
		}
		key, elem := s.key(it.typ, i), s.elem(it.typ, i)
		if it.m.writing != staticWriting {
			// The map has been written since Init: return what it
			// still holds, as for a map grown during iteration.
			var ok bool
			if key, elem, ok = it.m.getWithKey(it.typ, key); !ok {
				continue
			}
		}
		it.entryIdx++
		it.key = key
		it.elem = elem
		return
	}
	it.key = nil
	it.elem = nil
}
