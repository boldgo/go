// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.runtimefreegc && !race

package strings

import (
	"internal/bytealg"
	"unsafe"
)

// sharedFlag is set while a string returned by String may use the
// Builder's buffer, which must then be left to the garbage collector.
// The race detector would report concurrent calls to String, which
// write the flag, so it is not tracked in race builds.
type sharedFlag = bool

const freesBuffers = true

// stringBuf returns the bytes for String, which may then use the buffer.
// A copy of a Builder, which a write would reject but String accepts,
// gets bytes of its own: the Builder it was copied from does not know
// about the string and may free the buffer when it grows.
func (b *Builder) stringBuf() []byte {
	if b.addr != b {
		return append([]byte(nil), b.buf...)
	}
	if !b.shared {
		b.shared = true
	}
	return b.buf
}

// The append methods check for room as append does, so that the compiler
// knows the append on the fast path cannot grow b and leaves out its call
// to the runtime: a call on the fast path, or two calls reaching the
// same code, would make it save the caller's registers on every write.

func (b *Builder) appendString(s string) {
	if uint(len(b.buf)+len(s)) > uint(cap(b.buf)) {
		b.buf = append(b.growFree(len(s)), s...)
	} else {
		b.buf = append(b.buf, s...)
	}
}

func (b *Builder) appendBytes(p []byte) {
	if uint(len(b.buf)+len(p)) > uint(cap(b.buf)) {
		b.buf = append(b.growFree(len(p)), p...)
	} else {
		b.buf = append(b.buf, p...)
	}
}

func (b *Builder) appendByte(c byte) {
	if uint(len(b.buf)+1) > uint(cap(b.buf)) {
		b.buf = append(b.growFree(1), c)
	} else {
		b.buf = append(b.buf, c)
	}
}

// growFree returns b's buffer moved to a larger one with room for n more
// bytes, as append would grow it, and frees the old buffer unless a
// string from String uses it.
func (b *Builder) growFree(n int) []byte {
	buf := bytealg.Grow(&b.buf, n, !b.shared)
	b.shared = false
	return buf
}

// release frees old, the buffer b had before a write, if the write moved
// b to a new buffer.
func (b *Builder) release(old []byte) {
	if unsafe.SliceData(old) != unsafe.SliceData(b.buf) {
		b.free(old)
	}
}

// free frees old, a buffer b has just moved off, unless a string from
// String uses it.
func (b *Builder) free(old []byte) {
	if !b.shared {
		bytealg.Free(old)
	}
	b.shared = false
}
