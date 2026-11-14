// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ir

import (
	"go/importer"
	"go/token"
	"go/types"
	"internal/testenv"
	"slices"
	"testing"
)

// TestBoolGetters checks that boolGetters lists, in order, every method
// the node dumps used to find by reflection: the exported methods of
// node types that take no arguments and return a bool.
func TestBoolGetters(t *testing.T) {
	testenv.MustHaveGoBuild(t)
	pkg, err := importer.ForCompiler(token.NewFileSet(), "source", nil).Import("cmd/compile/internal/ir")
	if err != nil {
		t.Fatal(err)
	}
	node := pkg.Scope().Lookup("Node").Type().Underlying().(*types.Interface)
	want := map[string]bool{}
	for _, name := range pkg.Scope().Names() {
		tn, ok := pkg.Scope().Lookup(name).(*types.TypeName)
		if !ok || tn.IsAlias() {
			continue
		}
		if _, ok := tn.Type().Underlying().(*types.Struct); !ok {
			continue
		}
		ptr := types.NewPointer(tn.Type())
		if !types.Implements(ptr, node) {
			continue
		}
		ms := types.NewMethodSet(ptr)
		for i := range ms.Len() {
			m := ms.At(i).Obj()
			sig := m.Type().(*types.Signature)
			if !m.Exported() || sig.Params().Len() != 0 || sig.Results().Len() != 1 {
				continue
			}
			res := sig.Results().At(0).Type()
			if b, ok := res.Underlying().(*types.Basic); !ok || b.Kind() != types.Bool {
				continue
			}
			if res != types.Typ[types.Bool] {
				t.Errorf("(*%s).%s returns %v, which boolGetters cannot call", name, m.Name(), res)
			}
			want[m.Name()] = true
		}
	}
	var got []string
	for _, g := range boolGetters {
		got = append(got, g.name)
	}
	if !slices.IsSorted(got) {
		t.Errorf("boolGetters is not sorted by name")
	}
	for name := range want {
		if !slices.Contains(got, name) {
			t.Errorf("boolGetters is missing %s", name)
		}
	}
	for _, name := range got {
		if !want[name] {
			t.Errorf("boolGetters lists %s, which no node type has", name)
		}
	}
}
