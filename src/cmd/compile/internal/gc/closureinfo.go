// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gc

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/objw"
	"cmd/compile/internal/typecheck"
	"cmd/internal/obj"
	"cmd/internal/objabi"
	"fmt"
	"path"
	"strings"
)

type funcLitInfo struct {
	fn   *ir.Func
	desc string
}

var funcLits []funcLitInfo

// recordClosures describes the package's function literals for the
// linker's init images (cmd/link/internal/ld/initimage.go), which hold
// closures an evaluator made and know them by source position. It must
// follow escape analysis, which decides which variables closures capture
// by value.
//
// A function literal's description is its symbol, the base name of its
// file, its line and column in that file, the size of its closure struct,
// then for each captured variable name:byval:offset:size. Copies of
// function literals that inlining made and range-over-func bodies are
// left out.
func recordClosures() {
	for _, fn := range typecheck.Target.Funcs {
		if fn.OClosure == nil || fn.RangeParent != nil {
			continue
		}
		if p := base.Ctxt.InnermostPos(fn.OClosure.Pos()); p.Base() != nil && p.Base().InliningIndex() >= 0 {
			continue
		}
		var b strings.Builder
		p := base.Ctxt.InnermostPos(fn.Pos())
		size := typecheck.ClosureType(fn.OClosure).Size()
		fmt.Fprintf(&b, "%s\t%s\t%d\t%d\t%d", fn.Linksym().Name, path.Base(p.Filename()), p.Line(), p.Col(), size)
		it := typecheck.NewClosureStructIter(fn.ClosureVars)
		for {
			n, typ, off := it.Next()
			if n == nil {
				break
			}
			fmt.Fprintf(&b, "\t%s:%t:%d:%d", n.Sym().Name, n.Byval(), off, typ.Size())
		}
		funcLits = append(funcLits, funcLitInfo{fn, b.String()})
	}
}

// dumpClosureInfo writes the descriptions of the function literals the
// package has code for in its ..closureinfo symbol, which nothing
// references, a line each. An R_KEEP relocation at the start of the line
// refers to the function, which is content addressable.
func dumpClosureInfo() {
	var b strings.Builder
	var rels []obj.Reloc
	for _, l := range funcLits {
		if s := l.fn.Linksym(); s.Type.IsText() {
			rels = append(rels, obj.Reloc{Type: objabi.R_KEEP, Off: int32(b.Len()), Sym: s})
			b.WriteString(l.desc)
			b.WriteByte('\n')
		}
	}
	funcLits = nil
	if b.Len() == 0 {
		return
	}
	lsym := typecheck.Lookup(".closureinfo").Linksym()
	lsym.WriteString(base.Ctxt, 0, b.Len(), b.String())
	objw.Global(lsym, int32(b.Len()), obj.RODATA|obj.NOPTR)
	for _, r := range rels {
		lsym.AddRel(base.Ctxt, r)
	}
}
