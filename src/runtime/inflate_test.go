// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"bytes"
	"compress/flate"
	"debug/elf"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func deflateForTest(t *testing.T, data []byte, level int) []byte {
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, level)
	if err != nil {
		t.Fatal(err)
	}
	w.Write(data)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func inflateInputs(t *testing.T) map[string][]byte {
	src, err := os.ReadFile("symtab.go")
	if err != nil {
		t.Fatal(err)
	}
	r := rand.New(rand.NewPCG(1, 2))
	random := make([]byte, 100000)
	skewed := make([]byte, 100000)
	for i := range random {
		random[i] = byte(r.Uint32())
		skewed[i] = "aaaaaaaabbbbccd\x00"[r.IntN(16)]
	}
	return map[string][]byte{
		"empty":  nil,
		"byte":   {'x'},
		"zeros":  make([]byte, 200000),
		"source": bytes.Repeat(src, 3),
		"random": random,
		"skewed": skewed,
	}
}

func TestInflate(t *testing.T) {
	levels := []int{flate.NoCompression, flate.HuffmanOnly, flate.BestSpeed, flate.DefaultCompression, flate.BestCompression}
	for name, data := range inflateInputs(t) {
		for _, level := range levels {
			z := deflateForTest(t, data, level)
			out := make([]byte, len(data))
			if !runtime.Inflate(z, out) {
				t.Errorf("%s at level %d: inflate failed", name, level)
				continue
			}
			if !bytes.Equal(out, data) {
				t.Errorf("%s at level %d: inflated data differs", name, level)
			}
			if runtime.Inflate(z, make([]byte, len(data)+1)) {
				t.Errorf("%s at level %d: inflate succeeded into a buffer one byte too long", name, level)
			}
			if len(data) > 0 && runtime.Inflate(z, make([]byte, len(data)-1)) {
				t.Errorf("%s at level %d: inflate succeeded into a buffer one byte too short", name, level)
			}
		}
	}
}

// TestInflateCorrupt checks that inflate rejects or survives damaged
// input without panicking.
func TestInflateCorrupt(t *testing.T) {
	inputs := inflateInputs(t)
	r := rand.New(rand.NewPCG(3, 4))
	for _, name := range []string{"source", "skewed", "random"} {
		data := inputs[name]
		for _, level := range []int{flate.HuffmanOnly, flate.BestSpeed, flate.BestCompression} {
			z := deflateForTest(t, data, level)
			for range 300 {
				c := bytes.Clone(z)
				switch r.IntN(3) {
				case 0:
					c = c[:r.IntN(len(c))]
				case 1:
					for range 1 + r.IntN(4) {
						c[r.IntN(len(c))] ^= byte(1 << r.IntN(8))
					}
				case 2:
					c[r.IntN(len(c))] = byte(r.Uint32())
				}
				runtime.Inflate(c, make([]byte, len(data)))
			}
		}
	}
}

func inflateBenchInput(b *testing.B) (raw, z []byte) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		b.Fatal(err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			b.Fatal(err)
		}
		raw = append(raw, data...)
		if len(raw) > 1<<20 {
			break
		}
	}
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.BestCompression)
	if err != nil {
		b.Fatal(err)
	}
	w.Write(raw)
	w.Close()
	return raw, buf.Bytes()
}

func BenchmarkInflate(b *testing.B) {
	raw, z := inflateBenchInput(b)
	out := make([]byte, len(raw))
	b.SetBytes(int64(len(raw)))
	for b.Loop() {
		if !runtime.Inflate(z, out) {
			b.Fatal("inflate failed")
		}
	}
}

func BenchmarkInflateCompressFlate(b *testing.B) {
	raw, z := inflateBenchInput(b)
	out := make([]byte, len(raw))
	b.SetBytes(int64(len(raw)))
	for b.Loop() {
		if _, err := io.ReadFull(flate.NewReader(bytes.NewReader(z)), out); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkInflatePclntab inflates the test binary's own pclntab, which
// is closer to the tables -compresspclntab deflates than source text is.
func BenchmarkInflatePclntab(b *testing.B) {
	exe, err := os.Executable()
	if err != nil {
		b.Fatal(err)
	}
	f, err := elf.Open(exe)
	if err != nil {
		b.Skip("not ELF:", err)
	}
	defer f.Close()
	s := f.Section(".gopclntab")
	if s == nil {
		b.Skip("no .gopclntab")
	}
	raw, err := s.Data()
	if err != nil {
		b.Fatal(err)
	}
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.BestCompression)
	if err != nil {
		b.Fatal(err)
	}
	w.Write(raw)
	w.Close()
	z := buf.Bytes()
	out := make([]byte, len(raw))
	b.SetBytes(int64(len(raw)))
	for b.Loop() {
		if !runtime.Inflate(z, out) {
			b.Fatal("inflate failed")
		}
	}
}
