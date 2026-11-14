// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssacompile

import (
	"strings"

	"cmd/compile/internal/logopt"
	"cmd/compile/internal/ssa"
	"cmd/compile/internal/ssa/block"
	"cmd/compile/internal/ssa/ssaop"
	"cmd/internal/obj"
)

// allocReport prints each runtime call left in the function that
// allocates from the heap or frees heap memory, with what the source
// asked for. Escape analysis (-m) tells why a variable lives on the
// heap; this reports every allocation that survived optimization,
// including those no variable names: appends that grow, conversions,
// concatenations, closures. It is off by default, like checkbce:
// enable it with -d=ssa/alloc_report/debug=1 or -json.
func allocReport(f *ssa.Func) {
	if f.Pass.Debug <= 0 && !logopt.Enabled() {
		return
	}
	for _, b := range f.Blocks {
		if b.Kind == block.BlockInvalid {
			continue
		}
		for _, v := range b.Values {
			if !isStaticCall(v) {
				continue
			}
			what := allocCallWhat(v)
			if what == "" {
				continue
			}
			if f.Pass.Debug > 0 {
				f.Warnl(v.Pos, "allocates: %s", what)
			}
			if logopt.Enabled() {
				logopt.LogOpt(v.Pos, "alloc", "allocreport", f.Name, what)
			}
		}
	}
}

// allocCallWhat describes the allocation or free a static call to the
// runtime performs, or returns "" if it does neither.
func allocCallWhat(v *ssa.Value) string {
	fn := v.Aux.(*ssa.AuxCall).Fn
	if fn == nil {
		return ""
	}
	if ssa.IsSpecializedMalloc(v.Aux) {
		// new(T) for a small T: (size, type, needzero).
		return allocNewWhat(allocTypeArgAt(v, 1))
	}
	name, ok := strings.CutPrefix(fn.Name, "runtime.")
	if !ok {
		return ""
	}
	switch name {
	case "newobject":
		return allocNewWhat(allocTypeArg(v))
	case "makeslice", "makeslice64", "makeslicecopy":
		return "make []" + allocTypeArg(v)
	case "growslice":
		return "append grows slice"
	case "growsliceNoAlias":
		return "append grows slice, frees old backing store"
	case "makemap", "makemap64", "makemap_small":
		return "make map"
	case "makechan", "makechan64":
		return "make chan"
	case "convT", "convTnoptr":
		return "convert " + allocTypeArg(v) + " to interface"
	case "convT16", "convT32", "convT64", "convTstring", "convTslice":
		return "convert to interface"
	case "concatstring2", "concatstring3", "concatstring4", "concatstring5", "concatstrings":
		return "string concatenation" + allocStackBuf(v)
	case "concatstring2NoAlias", "concatstring3NoAlias":
		return "string concatenation, extends or frees old string"
	case "concatbyte2", "concatbyte3", "concatbyte4", "concatbyte5", "concatbytes":
		return "[]byte concatenation"
	case "slicebytetostring":
		return "string([]byte)" + allocStackBuf(v)
	case "stringtoslicebyte":
		return "[]byte(string)" + allocStackBuf(v)
	case "stringtoslicerune":
		return "[]rune(string)" + allocStackBuf(v)
	case "slicerunetostring":
		return "string([]rune)" + allocStackBuf(v)
	case "intstring":
		return "string(rune)" + allocStackBuf(v)
	case "newproc":
		return "go statement"
	case "deferproc":
		return "defer on the heap"
	case "freegc":
		return "frees heap object"
	}
	return ""
}

func allocNewWhat(t string) string {
	if strings.HasPrefix(t, "struct { F uintptr") {
		return "closure"
	}
	return "new " + t
}

// allocTypeArg returns the name of the type a runtime allocation call
// takes as its first argument, or "T" if it is not a constant.
func allocTypeArg(v *ssa.Value) string {
	return allocTypeArgAt(v, 0)
}

func allocTypeArgAt(v *ssa.Value, i int) string {
	if len(v.Args) <= i {
		return "T"
	}
	a := stripCopies(v.Args[i])
	if a.Op != ssaop.OpAddr {
		return "T"
	}
	s, ok := a.Aux.(*obj.LSym)
	if !ok {
		return "T"
	}
	t, ok := strings.CutPrefix(s.Name, "type:")
	if !ok {
		return "T"
	}
	return strings.TrimPrefix(t, "noalg.")
}

// allocStackBuf notes when a string conversion or concatenation was
// given a stack buffer, which it uses instead of the heap for a result
// of up to 32 bytes.
func allocStackBuf(v *ssa.Value) string {
	if len(v.Args) == 0 || stripCopies(v.Args[0]).Op == ssaop.OpConstNil {
		return ""
	}
	return " (on the stack if at most 32 bytes)"
}
