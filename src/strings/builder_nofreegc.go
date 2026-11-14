// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !goexperiment.runtimefreegc || race

package strings

// freesBuffers reports whether a Builder frees the buffers it moves off.
// The methods that test it compile, and cost the inliner, exactly as
// they would without the test: an if on a constant costs nothing.
const freesBuffers = false

type sharedFlag struct{}

func (b *Builder) stringBuf() []byte     { panic("unreachable") }
func (b *Builder) appendString(s string) { panic("unreachable") }
func (b *Builder) appendBytes(p []byte)  { panic("unreachable") }
func (b *Builder) appendByte(c byte)     { panic("unreachable") }

func (b *Builder) release(old []byte) {}

func (b *Builder) free(old []byte) {}
