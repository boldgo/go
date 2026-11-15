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
	"internal/abi"
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
	// Read-only objects without pointers, such as strings, share the
	// program's string data when their contents match.
	var consts map[string]loader.Sym
	for i, s := range img.Syms {
		if s.RO && len(s.Relocs) == 0 {
			if consts == nil {
				consts = map[string]loader.Sym{}
				for i := loader.Sym(1); i < loader.Sym(ldr.NSym()); i++ {
					if strings.HasPrefix(ldr.SymName(i), "go:string.") {
						consts[string(ldr.Data(i))] = i
					}
				}
			}
			d := string(s.Data) + strings.Repeat("\x00", int(s.Size)-len(s.Data))
			if c, ok := consts[d]; ok {
				byName[s.Name] = c
				continue
			}
			syms[i] = ldr.CreateStaticSym(s.Name)
			consts[d] = syms[i]
			byName[s.Name] = syms[i]
			continue
		}
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
		if s == 0 && strings.HasPrefix(name, "go:itab.") {
			s = ctxt.synthItab(name, hashed)
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
			t := target(r.Sym, r.Text)
			if t == 0 {
				continue
			}
			sb.SetAddrPlus(ctxt.Arch, r.Off, t, r.Add)
			if r.Kind == "type" || r.Kind == "itab" {
				// The type is in an interface, as a conversion
				// would mark it, so its methods stay for type
				// assertions and reflection.
				u, _ := sb.AddRel(objabi.R_USEIFACE)
				u.SetSym(t)
				u.SetOff(int32(r.Off))
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

// synthItab builds the itab name, go:itab.T,I, which only the runtime
// creates in the program: the image holds a T in an I that a type
// assertion made. It returns 0 if the types are not in the program.
func (ctxt *Link) synthItab(name string, types map[string]loader.Sym) loader.Sym {
	ldr := ctxt.loader
	arch := ctxt.Arch
	rest := strings.TrimPrefix(name, "go:itab.")
	comma := strings.LastIndexByte(rest, ',')
	if comma < 0 {
		return 0
	}
	tsym, isym := types["type:"+rest[:comma]], types["type:"+rest[comma+1:]]
	if tsym == 0 || isym == 0 {
		return 0
	}
	ip := ldr.Data(isym)
	n := int(decodetypeIfaceMethodCount(arch, ip))
	irelocs := ldr.Relocs(isym)
	marr := decodeReloc(ldr, isym, &irelocs, int32(commonsize(arch)+arch.PtrSize))
	tp := ldr.Data(tsym)
	if !decodetypeHasUncommon(arch, tp) {
		return 0
	}
	trelocs := ldr.Relocs(tsym)
	uoff := abi.RTypeSize(decodetypeKind(arch, tp), arch.PtrSize)
	mcount := int(decodeInuxi(arch, tp[uoff+4:], 2))
	moff := uoff + int(decodeInuxi(arch, tp[uoff+4+2+2:], 4))
	s := ldr.CreateStaticSym(name)
	sb := ldr.MakeSymbolUpdater(s)
	sb.SetType(sym.SRODATA)
	sb.SetAddr(arch, 0, isym)
	sb.SetAddr(arch, int64(arch.PtrSize), tsym)
	sb.SetUint32(arch, int64(2*arch.PtrSize), uint32(decodeInuxi(arch, tp[2*arch.PtrSize:], 4)))
	fun := int64(2*arch.PtrSize + 8)
	for k := 0; k < n; k++ {
		ioff := int(marr.Add()) + k*8
		iname := decodetypeName(ldr, isym, &irelocs, ioff)
		ityp := decodeRelocSym(ldr, isym, &irelocs, int32(ioff+4))
		var ifn loader.Sym
		for j := 0; j < mcount; j++ {
			off := moff + j*16
			if decodetypeName(ldr, tsym, &trelocs, off) == iname && decodeRelocSym(ldr, tsym, &trelocs, int32(off+4)) == ityp {
				ifn = decodeRelocSym(ldr, tsym, &trelocs, int32(off+8))
				break
			}
		}
		if ifn == 0 {
			return 0
		}
		sb.SetAddr(arch, fun+int64(k*arch.PtrSize), ifn)
	}
	if n == 0 {
		sb.SetUint(arch, fun, 0)
	}
	sb.SetAlign(int32(arch.PtrSize))
	ldr.SetItab(s)
	return s
}
