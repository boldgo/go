// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

// A decoder for raw DEFLATE streams (RFC 1951), for the symbol tables the
// linker compresses under -compresspclntab. It writes into a buffer of
// the decoded size, takes its decoding tables from the caller and keeps
// little on the stack, so tracebacks can use it anywhere, including in
// signal handlers. It checks its input rather than relying on bounds
// checks: a corrupt stream makes it report failure, never panic.

const (
	inflateFastBits = 10
	inflateMaxBits  = 15
)

// A huffman is a canonical Huffman code. fast maps the next fastBits
// input bits to symbol<<4 | length for codes of at most fastBits bits,
// and holds 0 for longer codes, which decode through count and symbol.
type huffman struct {
	count  [inflateMaxBits + 1]uint16
	symbol [288]uint16
	fast   [1 << inflateFastBits]uint16
}

// inflateScratch holds the decoding tables of an inflation, which are
// too large for the small stacks tracebacks may run on.
type inflateScratch struct {
	lit  huffman
	dist huffman
	lens [288 + 32]uint8
}

type inflater struct {
	in    []byte
	pos   int
	bits  uint64
	nbits uint
	out   []byte
	n     int
	*inflateScratch
}

var (
	inflateLenBase   = [29]uint16{3, 4, 5, 6, 7, 8, 9, 10, 11, 13, 15, 17, 19, 23, 27, 31, 35, 43, 51, 59, 67, 83, 99, 115, 131, 163, 195, 227, 258}
	inflateLenExtra  = [29]uint8{0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 0}
	inflateDistBase  = [30]uint16{1, 2, 3, 4, 5, 7, 9, 13, 17, 25, 33, 49, 65, 97, 129, 193, 257, 385, 513, 769, 1025, 1537, 2049, 3073, 4097, 6145, 8193, 12289, 16385, 24577}
	inflateDistExtra = [30]uint8{0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6, 7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13}
	inflateLenOrder  = [19]uint8{16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15}
)

// inflate decodes the raw DEFLATE stream in into out, which must be
// exactly as long as the decoded data, and reports whether it succeeded.
func inflate(in, out []byte, scratch *inflateScratch) bool {
	f := inflater{in: in, out: out, inflateScratch: scratch}
	return f.run()
}

// need reports whether the bit buffer holds n bits, loading more input
// if it does not.
func (f *inflater) need(n uint) bool {
	if f.nbits >= n {
		return true
	}
	if len(f.in)-f.pos >= 8 {
		// Load whole bytes until the buffer holds at least 56 bits.
		p := f.in[f.pos : f.pos+8]
		v := uint64(p[0]) | uint64(p[1])<<8 | uint64(p[2])<<16 | uint64(p[3])<<24 |
			uint64(p[4])<<32 | uint64(p[5])<<40 | uint64(p[6])<<48 | uint64(p[7])<<56
		k := (63 - f.nbits) / 8
		f.bits |= (v & (1<<(8*k) - 1)) << f.nbits
		f.pos += int(k)
		f.nbits += 8 * k
		return f.nbits >= n
	}
	for f.nbits < n {
		if f.pos >= len(f.in) {
			return false
		}
		f.bits |= uint64(f.in[f.pos]) << f.nbits
		f.pos++
		f.nbits += 8
	}
	return true
}

// take removes and returns the next n bits, which need has loaded.
func (f *inflater) take(n uint) uint32 {
	v := uint32(f.bits & (1<<n - 1))
	f.bits >>= n
	f.nbits -= n
	return v
}

func (f *inflater) run() bool {
	for {
		if !f.need(3) {
			return false
		}
		last := f.take(1)
		var ok bool
		switch f.take(2) {
		case 0:
			ok = f.stored()
		case 1:
			ok = f.fixed()
		case 2:
			ok = f.dynamic()
		}
		if !ok {
			return false
		}
		if last == 1 {
			return f.n == len(f.out)
		}
	}
}

func (f *inflater) stored() bool {
	// Drop the rest of the current byte, and return whole bytes still in
	// the bit buffer to the input.
	f.take(f.nbits % 8)
	f.pos -= int(f.nbits / 8)
	f.bits, f.nbits = 0, 0
	if len(f.in)-f.pos < 4 {
		return false
	}
	n := int(f.in[f.pos]) | int(f.in[f.pos+1])<<8
	nc := int(f.in[f.pos+2]) | int(f.in[f.pos+3])<<8
	f.pos += 4
	if n != ^nc&0xffff || len(f.in)-f.pos < n || len(f.out)-f.n < n {
		return false
	}
	copy(f.out[f.n:f.n+n], f.in[f.pos:f.pos+n])
	f.pos += n
	f.n += n
	return true
}

func (f *inflater) fixed() bool {
	l := f.lens[:288+30]
	for i := range l {
		switch {
		case i < 144:
			l[i] = 8
		case i < 256:
			l[i] = 9
		case i < 280:
			l[i] = 7
		case i < 288:
			l[i] = 8
		default:
			l[i] = 5
		}
	}
	return f.lit.build(l[:288]) && f.dist.build(l[288:]) && f.codes()
}

func (f *inflater) dynamic() bool {
	if !f.need(14) {
		return false
	}
	nlen := int(f.take(5)) + 257
	ndist := int(f.take(5)) + 1
	ncode := int(f.take(4)) + 4
	if nlen > 286 || ndist > 30 {
		return false
	}
	// The code for the code lengths, built in f.lit for the moment.
	l := f.lens[:19]
	for i := range l {
		l[i] = 0
	}
	for i := 0; i < ncode; i++ {
		if !f.need(3) {
			return false
		}
		l[inflateLenOrder[i]] = uint8(f.take(3))
	}
	if !f.lit.build(l) {
		return false
	}
	l = f.lens[:nlen+ndist]
	for i := 0; i < len(l); {
		sym := f.decode(&f.lit)
		if sym < 0 {
			return false
		}
		if sym < 16 {
			l[i] = uint8(sym)
			i++
			continue
		}
		var val uint8
		var rep int
		switch sym {
		case 16:
			if i == 0 || !f.need(2) {
				return false
			}
			val = l[i-1]
			rep = 3 + int(f.take(2))
		case 17:
			if !f.need(3) {
				return false
			}
			rep = 3 + int(f.take(3))
		default:
			if !f.need(7) {
				return false
			}
			rep = 11 + int(f.take(7))
		}
		if len(l)-i < rep {
			return false
		}
		for ; rep > 0; rep-- {
			l[i] = val
			i++
		}
	}
	if l[256] == 0 {
		// No end-of-block code.
		return false
	}
	return f.lit.build(l[:nlen]) && f.dist.build(l[nlen:]) && f.codes()
}

// build makes h the canonical code with the given code lengths. It
// reports false if the lengths are over-subscribed; an incomplete code
// is allowed, and decoding one of its missing codes fails.
func (h *huffman) build(lengths []uint8) bool {
	if len(lengths) > len(h.symbol) {
		return false
	}
	h.count = [inflateMaxBits + 1]uint16{}
	for _, l := range lengths {
		if l > inflateMaxBits {
			return false
		}
		h.count[l]++
	}
	h.count[0] = 0
	left := 1
	for l := 1; l <= inflateMaxBits; l++ {
		left = left<<1 - int(h.count[l])
		if left < 0 {
			return false
		}
	}
	var offs [inflateMaxBits + 2]uint16
	for l := 1; l <= inflateMaxBits; l++ {
		offs[l+1] = offs[l] + h.count[l]
	}
	for sym, l := range lengths {
		if l != 0 {
			h.symbol[offs[l]] = uint16(sym)
			offs[l]++
		}
	}
	for i := range h.fast {
		h.fast[i] = 0
	}
	code, index := 0, 0
	for l := 1; l <= inflateFastBits; l++ {
		for i := 0; i < int(h.count[l]); i++ {
			// The stream holds codes most significant bit first.
			rev := 0
			for b := 0; b < l; b++ {
				rev |= (code >> b & 1) << (l - 1 - b)
			}
			e := h.symbol[index]<<4 | uint16(l)
			for j := rev; j < len(h.fast); j += 1 << l {
				h.fast[j] = e
			}
			code++
			index++
		}
		code <<= 1
	}
	return true
}

// decode decodes a symbol with h, or returns -1 if the input does not
// hold a valid code.
func (f *inflater) decode(h *huffman) int {
	f.need(inflateFastBits)
	if e := h.fast[f.bits&(1<<inflateFastBits-1)]; e != 0 && uint(e&15) <= f.nbits {
		f.take(uint(e & 15))
		return int(e >> 4)
	}
	code, first, index := 0, 0, 0
	for l := 1; l <= inflateMaxBits; l++ {
		if !f.need(1) {
			return -1
		}
		code |= int(f.take(1))
		count := int(h.count[l])
		if code-count < first {
			return int(h.symbol[index+code-first])
		}
		index += count
		first = (first + count) << 1
		code <<= 1
	}
	return -1
}

// codes decodes a block's literals and matches with h.lit and h.dist.
func (f *inflater) codes() bool {
	// While the input holds a whole 8-byte load and the output has room
	// for the longest match, decode with the state in locals and refill
	// once per symbol; codesTail finishes the block.
	in, out := f.in, f.out
	pos, n := f.pos, f.n
	bits, nbits := f.bits, f.nbits
	lit, dist := &f.lit, &f.dist
	for len(in)-pos >= 8 && len(out)-n >= 258 {
		if nbits < 48 {
			// The bits loaded past nbits are the stream's next bits in
			// place, so loading them again changes nothing.
			p := in[pos : pos+8]
			bits |= (uint64(p[0]) | uint64(p[1])<<8 | uint64(p[2])<<16 | uint64(p[3])<<24 |
				uint64(p[4])<<32 | uint64(p[5])<<40 | uint64(p[6])<<48 | uint64(p[7])<<56) << nbits
			pos += int((63 - nbits) >> 3)
			nbits |= 56
		}
		// At least 48 bits are loaded: enough for the longest length
		// code, its extra bits, the longest distance code and its extra
		// bits.
		var sym int
		if e := lit.fast[bits&(1<<inflateFastBits-1)]; e != 0 {
			bits >>= e & 15
			nbits -= uint(e & 15)
			sym = int(e >> 4)
		} else {
			f.pos, f.bits, f.nbits = pos, bits, nbits
			sym = f.decode(lit)
			pos, bits, nbits = f.pos, f.bits, f.nbits
			if sym < 0 {
				return false
			}
		}
		if sym < 256 {
			out[n] = byte(sym)
			n++
			continue
		}
		if sym == 256 {
			f.pos, f.n, f.bits, f.nbits = pos, n, bits, nbits
			return true
		}
		sym -= 257
		if sym >= len(inflateLenBase) {
			return false
		}
		eb := uint(inflateLenExtra[sym])
		length := int(inflateLenBase[sym]) + int(bits&(1<<eb-1))
		bits >>= eb
		nbits -= eb
		var d int
		if e := dist.fast[bits&(1<<inflateFastBits-1)]; e != 0 {
			bits >>= e & 15
			nbits -= uint(e & 15)
			d = int(e >> 4)
		} else {
			f.pos, f.bits, f.nbits = pos, bits, nbits
			d = f.decode(dist)
			pos, bits, nbits = f.pos, f.bits, f.nbits
			if d < 0 {
				return false
			}
		}
		if d >= len(inflateDistBase) {
			return false
		}
		eb = uint(inflateDistExtra[d])
		d = int(inflateDistBase[d]) + int(bits&(1<<eb-1))
		bits >>= eb
		nbits -= eb
		if d > n {
			return false
		}
		src := n - d
		if d >= length {
			copy(out[n:n+length], out[src:src+length])
		} else {
			for i := 0; i < length; i++ {
				out[n+i] = out[src+i]
			}
		}
		n += length
	}
	f.pos, f.n, f.bits, f.nbits = pos, n, bits, nbits
	return f.codesTail()
}

// codesTail is codes for the end of the input or output, checking every
// load and store.
func (f *inflater) codesTail() bool {
	for {
		sym := f.decode(&f.lit)
		switch {
		case sym < 0:
			return false
		case sym < 256:
			if f.n >= len(f.out) {
				return false
			}
			f.out[f.n] = byte(sym)
			f.n++
		case sym == 256:
			return true
		default:
			sym -= 257
			if sym >= len(inflateLenBase) || !f.need(uint(inflateLenExtra[sym])) {
				return false
			}
			length := int(inflateLenBase[sym]) + int(f.take(uint(inflateLenExtra[sym])))
			d := f.decode(&f.dist)
			if d < 0 || d >= len(inflateDistBase) || !f.need(uint(inflateDistExtra[d])) {
				return false
			}
			dist := int(inflateDistBase[d]) + int(f.take(uint(inflateDistExtra[d])))
			if dist > f.n || len(f.out)-f.n < length {
				return false
			}
			src := f.n - dist
			if dist >= length {
				copy(f.out[f.n:f.n+length], f.out[src:src+length])
			} else {
				for i := 0; i < length; i++ {
					f.out[f.n+i] = f.out[src+i]
				}
			}
			f.n += length
		}
	}
}
