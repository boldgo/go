// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssacompile

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/ssa"
	"cmd/compile/internal/ssa/block"
	"cmd/compile/internal/ssa/ssaop"
	"cmd/compile/internal/types"
	"cmd/internal/obj"
)

// ownership changes calls to growslice into calls to growsliceNoAlias,
// which frees the old backing array once it has been copied, when the SSA
// form shows that nothing can use the old array after the call. It
// complements the alias analysis in escape/alias.go, which works on syntax
// and gives up on the control flow that inlining introduces.
//
// For a call G = growslice(old, newLen, oldCap, ...), it requires:
//
//   - Origin. Following old and oldCap back together through Phis and
//     Copies reaches only nil, stack addresses, zerobase, or the pointer and
//     capacity of one fresh heap allocation (a makeslice or growslice
//     result). An
//     interior pointer, a global, a parameter, a loaded pointer, or a
//     capacity that does not belong to the allocation stops the change.
//
//   - Containment. Call the pointers that may point into one of those
//     allocations the aliases. No alias is stored to memory, converted to
//     an integer or interface, returned, or passed to a call that may
//     retain it, at any point from which G can still execute. A retaining
//     use from which G cannot execute happens after the allocation it sees
//     was last freed, so it is harmless unless that alias is live across G,
//     which the next rule rejects.
//
//   - Death. No alias computed before G is used after G.
//
// Together these mean old is the only reference to its array when G runs.
// Only element types without pointers are candidates; ssagen records them
// in Func.FreegcGrows.
//
// The same rules turn s = concatstring2(buf, s, x) (and concatstring3)
// into concatstring2NoAlias, which extends s in place when its object has
// room and otherwise frees it after copying. For strings the accepted
// heap origins are only results of the call itself, of other NoAlias
// concatenations, and of string conversions, since a plain concatenation
// may return one of its operands. The empty string, literals and the
// stack are accepted too, as the runtime extends and frees only heap
// objects.
func ownership(f *ssa.Func) {
	if base.Debug.FreegcSSA <= 0 {
		return
	}
	cands := f.FreegcGrows[:len(f.FreegcGrows):len(f.FreegcGrows)]
	if !base.Flag.Cfg.Instrumenting && !base.Flag.CompilingRuntime {
		for _, b := range f.Blocks {
			for _, v := range b.Values {
				if isStaticCall(v) {
					fn := v.Aux.(*ssa.AuxCall).Fn
					if d, ok := ownedCallOf(fn); ok && d.str && fn != d.noAlias {
						cands = append(cands, v)
					}
				}
			}
		}
	}
	o := &ownState{f: f}
	for _, g := range cands {
		if g.Block == nil || !isStaticCall(g) {
			continue // removed as dead
		}
		aux := g.Aux.(*ssa.AuxCall)
		d, ok := ownedCallOf(aux.Fn)
		if !ok || aux.Fn == d.noAlias || len(g.Args) != d.nargs+1 {
			continue
		}
		if o.users == nil {
			o.init()
		}
		what := "old backing store"
		if d.str {
			what = "old string"
		}
		if why := o.check(g, d); why != "" {
			if base.Debug.FreegcSSA >= 3 {
				f.Warnl(g.Pos, "ssa ownership: keeps %s: %s", what, why)
			}
			continue
		}
		na := *aux
		na.Fn = d.noAlias
		g.Aux = &na
		if base.Debug.FreegcSSA >= 2 {
			f.Warnl(g.Pos, "ssa ownership: frees %s", what)
		}
	}
	if f.FreshResults != nil {
		*f.FreshResults = o.freshResults()
	}
}

// An ownedCall describes a runtime call that copies an object the caller
// may own into a new one, together with its variant that frees or reuses
// the old object when the caller does own it.
type ownedCall struct {
	noAlias   *obj.LSym
	nargs     int   // register arguments, not counting memory
	ptr, size int   // arguments: the old object's pointer, and its capacity or length
	resPtr    int64 // results: the new object's pointer,
	resSize   int64 // and its capacity or length
	str       bool  // a string concatenation
}

// ownedCallOf describes fn, which may be either variant.
func ownedCallOf(fn *obj.LSym) (ownedCall, bool) {
	switch fn {
	case ir.Syms.Growslice, ir.Syms.GrowsliceNoAlias:
		return ownedCall{noAlias: ir.Syms.GrowsliceNoAlias, nargs: 5, ptr: 0, size: 2, resPtr: 0, resSize: 2}, true
	case ir.Syms.GrowsliceBuf, ir.Syms.GrowsliceBufNoAlias:
		return ownedCall{noAlias: ir.Syms.GrowsliceBufNoAlias, nargs: 7, ptr: 0, size: 2, resPtr: 0, resSize: 2}, true
	case ir.Syms.Concatstring2, ir.Syms.Concatstring2NoAlias:
		return ownedCall{noAlias: ir.Syms.Concatstring2NoAlias, nargs: 5, ptr: 1, size: 2, resPtr: 0, resSize: 1, str: true}, true
	case ir.Syms.Concatstring3, ir.Syms.Concatstring3NoAlias:
		return ownedCall{noAlias: ir.Syms.Concatstring3NoAlias, nargs: 7, ptr: 1, size: 2, resPtr: 0, resSize: 1, str: true}, true
	}
	return ownedCall{}, false
}

type ownState struct {
	f     *ssa.Func
	users [][]*ssa.Value // users[v.ID] lists the values with v as an argument
	ctrls [][]*ssa.Block // ctrls[v.ID] lists the blocks with v as a control
}

func (o *ownState) init() {
	n := o.f.NumValues()
	o.users = make([][]*ssa.Value, n)
	o.ctrls = make([][]*ssa.Block, n)
	for _, b := range o.f.Blocks {
		for _, v := range b.Values {
			for _, a := range v.Args {
				o.users[a.ID] = append(o.users[a.ID], v)
			}
		}
		for _, c := range b.ControlValues() {
			o.ctrls[c.ID] = append(o.ctrls[c.ID], b)
		}
	}
}

// check reports why the old object of call g, described by d, might still
// be referenced after g, or "" if it cannot be.
func (o *ownState) check(g *ssa.Value, d ownedCall) string {
	roots, why := o.origins(g.Args[d.ptr], g.Args[d.size], g, d)
	if why != "" {
		return why
	}
	if len(roots) == 0 {
		return "old backing store is never on the heap"
	}

	aliases, alias := o.aliasesOf(roots)

	after := o.after(g)
	reach := reachesBlock(o.f, g.Block)
	canReachG := func(x *ssa.Value) bool {
		if x.Block != g.Block {
			return reach[x.Block.ID]
		}
		return reach[g.Block.ID] || !after[x.ID]
	}

	for _, a := range aliases {
		if len(o.ctrls[a.ID]) > 0 {
			return "pointer used as a block control"
		}
		for _, u := range o.users[a.ID] {
			if after[u.ID] && !after[a.ID] {
				return "old backing store used after the call: " + u.Op.String()
			}
			if isStaticCall(u) && u.Aux.(*ssa.AuxCall).Fn.Name == "runtime.freegc" {
				return "freed when its scope ends"
			}
			if u == g || derivesPointer(u.Op) || o.borrows(u, a) {
				continue
			}
			if canReachG(u) {
				return "pointer escapes before the call: " + escapeString(u)
			}
		}
	}

	live := o.liveOut(g.Block, aliases, alias)
	for i, a := range aliases {
		if live[i/64]&(1<<(i%64)) != 0 && !after[a.ID] {
			return "pointer live after the call"
		}
	}
	return ""
}

// aliasesOf returns the values that may point into one of the roots'
// allocations, and the index of each in that list.
func (o *ownState) aliasesOf(roots []*ssa.Value) ([]*ssa.Value, map[ssa.ID]int) {
	alias := make(map[ssa.ID]int)
	var aliases []*ssa.Value
	add := func(v *ssa.Value) {
		if _, ok := alias[v.ID]; !ok {
			alias[v.ID] = len(aliases)
			aliases = append(aliases, v)
		}
	}
	for _, r := range roots {
		add(r)
	}
	for i := 0; i < len(aliases); i++ {
		for _, u := range o.users[aliases[i].ID] {
			if derivesPointer(u.Op) {
				add(u)
			}
		}
	}
	return aliases, alias
}

// freshResults returns the bit set of f's results that are fresh: on
// every return, a pointer-free slice whose pointer and capacity, or a
// string whose pointer and length, pass origins, and whose allocations
// are used only by derived pointers, by calls that borrow them, and as
// that one result. A caller in another package may then treat the result
// as an allocation it owns.
func (o *ownState) freshResults() uint64 {
	var rets []*ssa.Value
	for _, b := range o.f.Blocks {
		switch b.Kind {
		case block.BlockRet:
			c := b.Controls[0]
			if c == nil || c.Op != ssaop.OpMakeResult {
				return 0
			}
			rets = append(rets, c)
		case block.BlockRetJmp:
			return 0
		}
	}
	if len(rets) == 0 {
		return 0
	}
	var mask uint64
	k := 0
	for i, r := range o.f.OwnAux.AbiInfo.OutParams() {
		ptr := k
		k += len(r.Registers)
		sizeReg, str, ok := ownedShape(r.Type)
		if !ok || i >= 64 || len(r.Registers) == 0 {
			continue
		}
		pos := rets[0].Pos // the first return
		if why := o.freshResult(rets, ptr, ptr+int(sizeReg), ownedCall{str: str}); why != "" {
			if base.Debug.FreegcFresh >= 2 {
				o.f.Warnl(pos, "ssa ownership: result %d is not fresh: %s", i, why)
			}
			continue
		}
		mask |= 1 << i
		if base.Debug.FreegcFresh >= 1 {
			o.f.Warnl(pos, "ssa ownership: result %d is fresh", i)
		}
	}
	return mask
}

// freshResult reports why the result in registers ptr (its pointer) and
// size (its capacity or length) of the MakeResults rets may not be
// fresh, or "" if it is.
func (o *ownState) freshResult(rets []*ssa.Value, ptr, size int, d ownedCall) string {
	var roots []*ssa.Value
	for _, m := range rets {
		rs, why := o.origins(m.Args[ptr], m.Args[size], nil, d)
		if why != "" {
			return why
		}
		roots = append(roots, rs...)
	}
	if len(roots) == 0 {
		return "never on the heap"
	}
	if o.users == nil {
		o.init()
	}
	aliases, _ := o.aliasesOf(roots)
	for _, a := range aliases {
		if len(o.ctrls[a.ID]) > 0 {
			return "pointer used as a block control"
		}
		for _, u := range o.users[a.ID] {
			switch {
			case u.Op == ssaop.OpMakeResult:
				for j, x := range u.Args {
					if x == a && j != ptr {
						return "also returned as another result"
					}
				}
			case isStaticCall(u) && u.Aux.(*ssa.AuxCall).Fn.Name == "runtime.freegc":
				return "freed when its scope ends"
			case derivesPointer(u.Op), o.borrows(u, a):
			default:
				return "pointer escapes: " + escapeString(u)
			}
		}
	}
	return ""
}

// ownedShape reports whether a value of type t can be owned: a string, or
// a slice of pointer-free elements. sizeReg is the register, after the
// pointer's, that holds the length or the capacity.
func ownedShape(t *types.Type) (sizeReg int64, str, ok bool) {
	switch {
	case t.IsString():
		return 1, true, true
	case t.IsSlice() && !t.Elem().HasPointers():
		return 2, false, true
	}
	return 0, false, false
}

// origins follows a slice pointer p and capacity c, or a string pointer p
// and length c, back through Phis and Copies and returns the heap
// allocations they can come from. It fails if they can come from anything
// other than nil, the stack, a string literal, or the start and full
// capacity or length of an allocation.
func (o *ownState) origins(p, c, g *ssa.Value, d ownedCall) (roots []*ssa.Value, why string) {
	type pair struct{ p, c *ssa.Value }
	seen := make(map[[2]ssa.ID]bool)
	stack := []pair{{p, c}}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		p, c := stripCopies(x.p), stripCopies(x.c)
		if seen[[2]ssa.ID{p.ID, c.ID}] {
			continue
		}
		seen[[2]ssa.ID{p.ID, c.ID}] = true
		switch {
		case p.Op == ssaop.OpConstNil || p.Op == ssaop.OpLocalAddr,
			p.Op == ssaop.OpAddr && p.Aux == ir.Syms.Zerobase:
			// The runtime does not free nil, the stack, or zero-sized
			// allocations.
		case d.str && p.Op == ssaop.OpAddr:
			// A string literal. The runtime frees or extends only heap
			// objects.
		case p.Op == ssaop.OpPhi:
			for j, a := range p.Args {
				if c.Op == ssaop.OpPhi && c.Block == p.Block {
					stack = append(stack, pair{a, c.Args[j]})
				} else {
					stack = append(stack, pair{a, c})
				}
			}
		case c.Op == ssaop.OpPhi:
			for _, a := range c.Args {
				stack = append(stack, pair{p, a})
			}
		case p.Op == ssaop.OpSelectN:
			call := p.Args[0]
			if !isStaticCall(call) {
				return nil, "pointer comes from " + call.Op.String()
			}
			fn := call.Aux.(*ssa.AuxCall).Fn
			if cd, ok := ownedCallOf(fn); ok {
				if cd.str != d.str || p.AuxInt != cd.resPtr {
					return nil, "pointer comes from " + fn.Name
				}
				if c.Op != ssaop.OpSelectN || c.AuxInt != cd.resSize || c.Args[0] != call {
					return nil, "capacity is not that of the allocation"
				}
				if cd.str && call != g && fn != cd.noAlias {
					return nil, "string may be an operand of " + fn.Name
				}
			} else if d.str && freshString[fn.Name] {
				if p.AuxInt != 0 || c.Op != ssaop.OpSelectN || c.AuxInt != 1 || c.Args[0] != call {
					return nil, "length is not that of the string"
				}
			} else if bits := o.f.FreshCalls[call]; bits != 0 {
				i, t := resultAt(call.Aux.(*ssa.AuxCall), p.AuxInt)
				if i < 0 || i >= 64 || bits&(1<<i) == 0 {
					return nil, "result of " + fn.Name + " is not fresh"
				}
				sizeReg, str, ok := ownedShape(t)
				if !ok || str != d.str || c.Op != ssaop.OpSelectN || c.Args[0] != call || c.AuxInt != p.AuxInt+sizeReg {
					return nil, "capacity is not that of the allocation"
				}
			} else {
				capArg := 2
				switch {
				case d.str || p.AuxInt != 0:
					return nil, "pointer comes from " + fn.Name
				case (fn.Name == "runtime.makeslice" || fn.Name == "runtime.makeslice64") && len(call.Args) == 4:
				case fn.Name == "runtime.makeslicecopy" && len(call.Args) == 5:
					capArg = 1 // makeslicecopy(et, tolen, fromlen, from)
				case movedSlice[fn.Name]:
					// The results are (ptr, len, cap) of a new copy.
					capArg = -1
					if c.Op != ssaop.OpSelectN || c.AuxInt != 2 || c.Args[0] != call {
						return nil, "capacity is not that of the allocation"
					}
				case fn.Name == "runtime.mallocgc" && len(call.Args) == 4:
					// make+copy of pointer-free elements: mallocgc(cap *
					// elemSize, nil, false). freegc frees nothing if the
					// size it is given does not match the object.
					capArg = -1
					if !isMulOf(stripCopies(call.Args[0]), c) {
						return nil, "capacity is not that of the allocation"
					}
				default:
					return nil, "pointer comes from " + fn.Name
				}
				if capArg >= 0 && stripCopies(call.Args[capArg]) != c {
					return nil, "capacity is not that of the allocation"
				}
			}
			roots = append(roots, p)
		default:
			return nil, "pointer comes from " + p.Op.String()
		}
	}
	return roots, ""
}

// isStaticCall reports whether v is a static call, before or after
// expand calls rewrites it.
func isStaticCall(v *ssa.Value) bool {
	return v.Op == ssaop.OpStaticCall || v.Op == ssaop.OpStaticLECall
}

func stripCopies(v *ssa.Value) *ssa.Value {
	for v.Op == ssaop.OpCopy || v.Op == ssaop.OpNilCheck {
		v = v.Args[0]
	}
	return v
}

// derivesPointer reports whether a value with this op may point into the
// same allocation as one of its arguments.
func derivesPointer(op ssaop.Op) bool {
	switch op {
	case ssaop.OpPhi, ssaop.OpCopy, ssaop.OpNilCheck, ssaop.OpCondSelect,
		ssaop.OpOffPtr, ssaop.OpAddPtr, ssaop.OpPtrIndex,
		ssaop.OpSliceMake, ssaop.OpSlicePtr, ssaop.OpSlicePtrUnchecked,
		ssaop.OpStringMake, ssaop.OpStringPtr,
		ssaop.OpStructMake, ssaop.OpStructSelect, ssaop.OpArrayMake1, ssaop.OpArraySelect:
		return true
	}
	return false
}

// borrows reports whether u uses pointer a without retaining it or
// deriving a new pointer from it.
func (o *ownState) borrows(u, a *ssa.Value) bool {
	switch u.Op {
	case ssaop.OpLoad, ssaop.OpZero, ssaop.OpKeepAlive:
		return true
	case ssaop.OpStore:
		return u.Args[1] != a // storing through a, not storing a
	case ssaop.OpMove:
		return true // copies the pointed-to memory
	case ssaop.OpEqPtr, ssaop.OpNeqPtr, ssaop.OpIsNonNil,
		ssaop.OpSliceLen, ssaop.OpSliceCap, ssaop.OpStringLen:
		return true
	case ssaop.OpConvert:
		// A conversion to an integer used only to compute and compare
		// integers, such as the check whether a slice is on the stack.
		return !u.Type.IsPtrShaped() && o.onlyCompared(u)
	case ssaop.OpStaticCall, ssaop.OpStaticLECall:
		fn := u.Aux.(*ssa.AuxCall).Fn
		if d, ok := ownedCallOf(fn); ok {
			if !d.str {
				// Copies the old array. Freeing it is decided at that call.
				return u.Args[0] == a
			}
			// A NoAlias concatenation copies its operands and decides at
			// that call whether it consumes the first. A plain one may
			// return an operand.
			return fn == d.noAlias
		}
		if fn == ir.Syms.Memmove || fn == ir.Syms.Memequal || nonRetaining[fn.Name] {
			return true
		}
		return o.paramsDoNotRetain(u, a)
	}
	return false
}

// onlyCompared reports whether the integer v is used only in integer
// arithmetic whose results are only compared, so that no pointer can be
// rebuilt from it.
func (o *ownState) onlyCompared(v *ssa.Value) bool {
	for _, u := range o.users[v.ID] {
		switch u.Op {
		case ssaop.OpEq64, ssaop.OpNeq64, ssaop.OpLess64, ssaop.OpLess64U, ssaop.OpLeq64, ssaop.OpLeq64U,
			ssaop.OpEq32, ssaop.OpNeq32, ssaop.OpLess32, ssaop.OpLess32U, ssaop.OpLeq32, ssaop.OpLeq32U:
		case ssaop.OpAdd64, ssaop.OpSub64, ssaop.OpAnd64, ssaop.OpAdd32, ssaop.OpSub32, ssaop.OpAnd32:
			if !o.onlyCompared(u) {
				return false
			}
		default:
			return false
		}
	}
	return len(o.ctrls[v.ID]) == 0
}

// paramsDoNotRetain reports whether every argument of call u that is a
// belongs to a parameter whose escape tag says the callee does not retain
// it. It relies on the escape tags exactly as stack allocation does.
func (o *ownState) paramsDoNotRetain(u, a *ssa.Value) bool {
	mask, ok := o.f.NoEscapeParams[u]
	if !ok {
		return false
	}
	// After expand calls, the arguments are the parameters' registers in
	// order, then memory. Stack-assigned parameters were stored to memory,
	// which counts as an escape.
	k := 0
	for i, p := range u.Aux.(*ssa.AuxCall).AbiInfo.InParams() {
		for range p.Registers {
			if k < len(u.Args) && u.Args[k] == a && (i >= 64 || mask&(1<<i) == 0) {
				return false
			}
			k++
		}
	}
	return k == len(u.Args)-1
}

// nonRetaining lists runtime functions the compiler calls with slice data
// pointers that only read or write through them.
var nonRetaining = map[string]bool{
	"runtime.memclrNoHeapPointers": true,
	"runtime.slicebytetostring":    true,
	"runtime.slicerunetostring":    true,
	"runtime.cmpstring":            true,
	"runtime.makeslicecopy":        true,
	"runtime.stringtoslicebyte":    true,
	"runtime.stringtoslicerune":    true,
	// The moveSlice functions copy old; with no capacity they return nil
	// or zerobase, never old.
	"runtime.moveSlice":            true,
	"runtime.moveSliceNoScan":      true,
	"runtime.moveSliceNoCap":       true,
	"runtime.moveSliceNoCapNoScan": true,
}

// movedSlice lists the runtime calls that move a slice built on the stack
// to a new heap object, returning its pointer, length and capacity.
var movedSlice = map[string]bool{
	"runtime.moveSlice":            true,
	"runtime.moveSliceNoScan":      true,
	"runtime.moveSliceNoCap":       true,
	"runtime.moveSliceNoCapNoScan": true,
}

// isMulOf reports whether size is n, or n times a constant.
func isMulOf(size, n *ssa.Value) bool {
	if size == n {
		return true
	}
	if size.Op != ssaop.OpMul64 && size.Op != ssaop.OpMul32 {
		return false
	}
	isConst := func(v *ssa.Value) bool { return v.Op == ssaop.OpConst64 || v.Op == ssaop.OpConst32 }
	x, y := stripCopies(size.Args[0]), stripCopies(size.Args[1])
	return x == n && isConst(y) || y == n && isConst(x)
}

// resultAt returns the index and type of the result of call aux whose
// first register is r, or -1.
func resultAt(aux *ssa.AuxCall, r int64) (int, *types.Type) {
	k := int64(0)
	for i, p := range aux.AbiInfo.OutParams() {
		if k == r && len(p.Registers) > 0 {
			return i, p.Type
		}
		k += int64(len(p.Registers))
	}
	return -1, nil
}

// freshString lists runtime calls whose string result is always a new
// copy, on the heap or in the caller's stack buffer.
var freshString = map[string]bool{
	"runtime.slicebytetostring": true,
	"runtime.slicerunetostring": true,
}

func escapeString(u *ssa.Value) string {
	if isStaticCall(u) {
		return "passed to " + u.Aux.(*ssa.AuxCall).Fn.Name
	}
	if u.Op == ssaop.OpStore {
		return "stored"
	}
	return u.Op.String()
}

// after returns the values in g's block that are computed after g, that
// is, that depend on g's results or memory.
func (o *ownState) after(g *ssa.Value) map[ssa.ID]bool {
	after := make(map[ssa.ID]bool)
	work := []*ssa.Value{g}
	for len(work) > 0 {
		v := work[len(work)-1]
		work = work[:len(work)-1]
		for _, u := range o.users[v.ID] {
			if u.Block == g.Block && u.Op != ssaop.OpPhi && !after[u.ID] {
				after[u.ID] = true
				work = append(work, u)
			}
		}
	}
	return after
}

// reachesBlock returns, indexed by block ID, whether a block can reach
// target by following at least one edge.
func reachesBlock(f *ssa.Func, target *ssa.Block) []bool {
	reach := make([]bool, f.NumBlocks())
	work := []*ssa.Block{target}
	for len(work) > 0 {
		b := work[len(work)-1]
		work = work[:len(work)-1]
		for _, e := range b.Preds {
			if p := e.Block(); !reach[p.ID] {
				reach[p.ID] = true
				work = append(work, p)
			}
		}
	}
	return reach
}

// liveOut returns, as a bit set indexed like vals, which of vals are live
// at the end of block b. idx maps a value ID to its index in vals.
func (o *ownState) liveOut(b *ssa.Block, vals []*ssa.Value, idx map[ssa.ID]int) []uint64 {
	f := o.f
	words := (len(vals) + 63) / 64
	nb := f.NumBlocks()
	gen := make([]uint64, nb*words)
	kill := make([]uint64, nb*words)
	in := make([]uint64, nb*words)
	set := func(s []uint64, i int) { s[i/64] |= 1 << (i % 64) }
	row := func(s []uint64, b *ssa.Block) []uint64 { return s[int(b.ID)*words : int(b.ID+1)*words] }

	for _, b := range f.Blocks {
		g, k := row(gen, b), row(kill, b)
		for _, v := range b.Values {
			if i, ok := idx[v.ID]; ok {
				set(k, i)
			}
			if v.Op == ssaop.OpPhi {
				continue // Phi arguments are used at the ends of predecessors
			}
			for _, a := range v.Args {
				if i, ok := idx[a.ID]; ok && a.Block != b {
					set(g, i)
				}
			}
		}
		for _, c := range b.ControlValues() {
			if i, ok := idx[c.ID]; ok && c.Block != b {
				set(g, i)
			}
		}
	}

	out := make([]uint64, words)
	computeOut := func(b *ssa.Block) {
		clear(out)
		for _, e := range b.Succs {
			s := e.Block()
			for w, x := range row(in, s) {
				out[w] |= x
			}
			for _, v := range s.Values {
				if v.Op == ssaop.OpPhi {
					if i, ok := idx[v.Args[e.Index()].ID]; ok {
						set(out, i)
					}
				}
			}
		}
	}
	po := f.Postorder()
	for changed := true; changed; {
		changed = false
		for _, b := range po {
			computeOut(b)
			r, g, k := row(in, b), row(gen, b), row(kill, b)
			for w := range r {
				x := g[w] | out[w]&^k[w]
				if x != r[w] {
					r[w] = x
					changed = true
				}
			}
		}
	}
	computeOut(b)
	return append([]uint64(nil), out...)
}
