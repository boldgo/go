// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ir

// boolGetters lists, sorted by name, the node methods that the node dumps
// print when they report true: the exported methods of node types that
// take no arguments and return a bool. The dumps call them through
// interfaces rather than reflect.Value.Method, which would make the linker
// keep every exported method of every type in the compiler.
// TestBoolGetters checks that the list is complete.
var boolGetters = [...]struct {
	name string
	get  func(Node) bool
}{
	{"ABIWrapper", func(n Node) bool { m, ok := n.(interface{ ABIWrapper() bool }); return ok && m.ABIWrapper() }},
	{"Addrtaken", func(n Node) bool { m, ok := n.(interface{ Addrtaken() bool }); return ok && m.Addrtaken() }},
	{"Alias", func(n Node) bool { m, ok := n.(interface{ Alias() bool }); return ok && m.Alias() }},
	{"AutoTemp", func(n Node) bool { m, ok := n.(interface{ AutoTemp() bool }); return ok && m.AutoTemp() }},
	{"Bounded", func(n Node) bool { m, ok := n.(interface{ Bounded() bool }); return ok && m.Bounded() }},
	{"Byval", func(n Node) bool { m, ok := n.(interface{ Byval() bool }); return ok && m.Byval() }},
	{"CheckPtr", func(n Node) bool { m, ok := n.(interface{ CheckPtr() bool }); return ok && m.CheckPtr() }},
	{"ClosureResultsLost", func(n Node) bool {
		m, ok := n.(interface{ ClosureResultsLost() bool })
		return ok && m.ClosureResultsLost()
	}},
	{"CoverageAuxVar", func(n Node) bool { m, ok := n.(interface{ CoverageAuxVar() bool }); return ok && m.CoverageAuxVar() }},
	{"Dupok", func(n Node) bool { m, ok := n.(interface{ Dupok() bool }); return ok && m.Dupok() }},
	{"HasDefer", func(n Node) bool { m, ok := n.(interface{ HasDefer() bool }); return ok && m.HasDefer() }},
	{"Implicit", func(n Node) bool { m, ok := n.(interface{ Implicit() bool }); return ok && m.Implicit() }},
	{"InlFormal", func(n Node) bool { m, ok := n.(interface{ InlFormal() bool }); return ok && m.InlFormal() }},
	{"InlLocal", func(n Node) bool { m, ok := n.(interface{ InlLocal() bool }); return ok && m.InlLocal() }},
	{"InlinabilityChecked", func(n Node) bool {
		m, ok := n.(interface{ InlinabilityChecked() bool })
		return ok && m.InlinabilityChecked()
	}},
	{"IsClosure", func(n Node) bool { m, ok := n.(interface{ IsClosure() bool }); return ok && m.IsClosure() }},
	{"IsClosureVar", func(n Node) bool { m, ok := n.(interface{ IsClosureVar() bool }); return ok && m.IsClosureVar() }},
	{"IsInlinedClosure", func(n Node) bool {
		m, ok := n.(interface{ IsInlinedClosure() bool })
		return ok && m.IsInlinedClosure()
	}},
	{"IsOutputParamHeapAddr", func(n Node) bool {
		m, ok := n.(interface{ IsOutputParamHeapAddr() bool })
		return ok && m.IsOutputParamHeapAddr()
	}},
	{"IsOutputParamInRegisters", func(n Node) bool {
		m, ok := n.(interface{ IsOutputParamInRegisters() bool })
		return ok && m.IsOutputParamInRegisters()
	}},
	{"IsPackageInit", func(n Node) bool { m, ok := n.(interface{ IsPackageInit() bool }); return ok && m.IsPackageInit() }},
	{"Libfuzzer8BitCounter", func(n Node) bool {
		m, ok := n.(interface{ Libfuzzer8BitCounter() bool })
		return ok && m.Libfuzzer8BitCounter()
	}},
	{"Needctxt", func(n Node) bool { m, ok := n.(interface{ Needctxt() bool }); return ok && m.Needctxt() }},
	{"Needzero", func(n Node) bool { m, ok := n.(interface{ Needzero() bool }); return ok && m.Needzero() }},
	{"NeverReturns", func(n Node) bool { m, ok := n.(interface{ NeverReturns() bool }); return ok && m.NeverReturns() }},
	{"NilCheckDisabled", func(n Node) bool {
		m, ok := n.(interface{ NilCheckDisabled() bool })
		return ok && m.NilCheckDisabled()
	}},
	{"NonMergeable", func(n Node) bool { m, ok := n.(interface{ NonMergeable() bool }); return ok && m.NonMergeable() }},
	{"NonNil", func(n Node) bool { m, ok := n.(interface{ NonNil() bool }); return ok && m.NonNil() }},
	{"OnStack", func(n Node) bool { m, ok := n.(interface{ OnStack() bool }); return ok && m.OnStack() }},
	{"OpenCodedDeferDisallowed", func(n Node) bool {
		m, ok := n.(interface{ OpenCodedDeferDisallowed() bool })
		return ok && m.OpenCodedDeferDisallowed()
	}},
	{"OpenDeferSlot", func(n Node) bool { m, ok := n.(interface{ OpenDeferSlot() bool }); return ok && m.OpenDeferSlot() }},
	{"Readonly", func(n Node) bool { m, ok := n.(interface{ Readonly() bool }); return ok && m.Readonly() }},
	{"Transient", func(n Node) bool { m, ok := n.(interface{ Transient() bool }); return ok && m.Transient() }},
	{"Used", func(n Node) bool { m, ok := n.(interface{ Used() bool }); return ok && m.Used() }},
	{"Walked", func(n Node) bool { m, ok := n.(interface{ Walked() bool }); return ok && m.Walked() }},
	{"Wrapper", func(n Node) bool { m, ok := n.(interface{ Wrapper() bool }); return ok && m.Wrapper() }},
}
