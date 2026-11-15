// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"cmd/internal/obj"
	"cmd/internal/objabi"
	"cmd/link/internal/loader"
	"cmd/link/internal/sym"
	"encoding/json"
	"os"
	"slices"
	"strings"
)

// Init images. An evaluator run before the link computes the state some
// packages' initializers leave and writes it as symbol contents: the
// globals' data and new objects they point to. GOLINK_INITIMAGE names
// that file. The linker writes the image into the program's data and does
// not schedule those packages' initializers, whose code is then dead
// unless something else uses it.

type initImage struct {
	Packages []struct {
		Path  string
		XVars []string
	}
	Syms []struct {
		Name   string
		Global bool
		Size   int64
		Data   []byte
		Ptrs   []int64
		Relocs []struct {
			Off  int64
			Sym  string
			Add  int64
			Text bool
			Kind string
		}
		RO bool
	}
}

var (
	// imageTasks holds the inittask symbols of the packages the image
	// initializes.
	imageTasks = map[loader.Sym]bool{}
	// imageMasks holds the pointer words of the image's new objects,
	// which have no Go type.
	imageMasks = map[loader.Sym][]int64{}
)

func (ctxt *Link) initImage() {
	file := os.Getenv("GOLINK_INITIMAGE")
	if file == "" {
		return
	}
	data, err := os.ReadFile(file)
	if err != nil {
		Exitf("init image: %v", err)
	}
	var img initImage
	if err := json.Unmarshal(data, &img); err != nil {
		Exitf("init image %s: %v", file, err)
	}
	ldr := ctxt.loader
	for _, p := range img.Packages {
		for _, x := range p.XVars {
			dot := strings.LastIndex(x, ".")
			if _, ok := strdata[objabi.PathToPrefix(x[:dot])+x[dot:]]; ok {
				Exitf("init image: the state of %s depends on %s, which -X sets", p.Path, x)
			}
		}
	}
	syms := make([]loader.Sym, len(img.Syms))
	byName := map[string]loader.Sym{}
	for i, s := range img.Syms {
		if s.Global {
			g := ldr.Lookup(s.Name, 0)
			if g == 0 {
				continue
			}
			if size := ldr.SymSize(g); size != s.Size {
				Exitf("init image: %s has size %d, the image %d", s.Name, size, s.Size)
			}
			syms[i] = g
		} else {
			syms[i] = ldr.CreateStaticSym(s.Name)
		}
		byName[s.Name] = syms[i]
	}
	var hashed map[string]loader.Sym
	missing := map[string]bool{}
	target := func(name string, text bool) loader.Sym {
		if s, ok := byName[name]; ok && s != 0 {
			return s
		}
		if text {
			if s := ldr.Lookup(name, sym.ABIToVersion(obj.ABIInternal)); s != 0 {
				return s
			}
		}
		s := ldr.Lookup(name, 0)
		if s == 0 && (strings.HasPrefix(name, "go:itab.") || strings.HasPrefix(name, "type:")) {
			// Content-addressable symbols are not in the name table.
			if hashed == nil {
				hashed = map[string]loader.Sym{}
				for i := loader.Sym(1); i < loader.Sym(ldr.NSym()); i++ {
					if n := ldr.SymName(i); strings.HasPrefix(n, "go:itab.") || strings.HasPrefix(n, "type:") {
						if _, ok := hashed[n]; !ok {
							hashed[n] = i
						}
					}
				}
			}
			s = hashed[name]
		}
		if s == 0 {
			missing[name] = true
		}
		return s
	}
	for i, s := range img.Syms {
		if syms[i] == 0 {
			continue
		}
		sb := ldr.MakeSymbolUpdater(syms[i])
		sb.ResetRelocs()
		d := make([]byte, s.Size)
		copy(d, s.Data)
		sb.SetData(d)
		sb.SetSize(s.Size)
		switch {
		case s.RO:
			sb.SetType(sym.SRODATA)
		case len(s.Ptrs) > 0:
			sb.SetType(sym.SDATA)
		default:
			sb.SetType(sym.SNOPTRDATA)
		}
		sb.SetReadOnly(false)
		for _, r := range s.Relocs {
			if t := target(r.Sym, r.Text); t != 0 {
				sb.SetAddrPlus(ctxt.Arch, r.Off, t, r.Add)
			}
		}
		if s.Global {
			continue
		}
		sb.SetAlign(8)
		ldr.SetAttrLocal(syms[i], true)
		if !s.RO && len(s.Ptrs) > 0 {
			slices.Sort(s.Ptrs)
			imageMasks[syms[i]] = s.Ptrs
		}
	}
	if len(missing) > 0 {
		var names []string
		for n := range missing {
			names = append(names, n)
		}
		slices.Sort(names)
		Exitf("init image: no symbols %s", strings.Join(names, " "))
	}
	for _, p := range img.Packages {
		if t := ldr.Lookup(objabi.PathToPrefix(p.Path)+"..inittask", 0); t != 0 {
			imageTasks[t] = true
		}
	}
}
