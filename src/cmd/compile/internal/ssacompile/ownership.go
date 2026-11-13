// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssacompile

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/ssa"
	"cmd/compile/internal/ssa/ssaop"
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
func ownership(f *ssa.Func) {
	if base.Debug.FreegcSSA <= 0 || len(f.FreegcGrows) == 0 {
		return
	}
	o := &ownState{f: f}
	for _, g := range f.FreegcGrows {
		if g.Block == nil || !isStaticCall(g) {
			continue // removed as dead
		}
		aux := g.Aux.(*ssa.AuxCall)
		noAlias, nargs := noAliasGrow(aux.Fn)
		if noAlias == nil || len(g.Args) != nargs+1 {
			continue
		}
		if o.users == nil {
			o.init()
		}
		if why := o.check(g); why != "" {
			if base.Debug.FreegcSSA >= 3 {
				f.Warnl(g.Pos, "ssa ownership: keeps old backing store: %s", why)
			}
			continue
		}
		na := *aux
		na.Fn = noAlias
		g.Aux = &na
		if base.Debug.FreegcSSA >= 2 {
			f.Warnl(g.Pos, "ssa ownership: frees old backing store")
		}
	}
}

// noAliasGrow returns the freeing variant of a growslice function and the
// number of arguments both take.
func noAliasGrow(fn *obj.LSym) (*obj.LSym, int) {
	switch fn {
	case ir.Syms.Growslice:
		return ir.Syms.GrowsliceNoAlias, 5
	case ir.Syms.GrowsliceBuf:
		return ir.Syms.GrowsliceBufNoAlias, 7
	}
	return nil, 0
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

// check reports why the old backing store of growslice call g might still
// be referenced after g, or "" if it cannot be.
func (o *ownState) check(g *ssa.Value) string {
	roots, why := o.origins(g.Args[0], g.Args[2])
	if why != "" {
		return why
	}
	if len(roots) == 0 {
		return "old backing store is never on the heap"
	}

	// Collect the aliases: values that may point into a root's allocation.
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

// origins follows a slice pointer p and capacity c back through Phis and
// Copies and returns the heap allocations they can come from. It fails if
// they can come from anything other than nil, the stack, or the start and
// full capacity of an allocation.
func (o *ownState) origins(p, c *ssa.Value) (roots []*ssa.Value, why string) {
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
		case p.Op == ssaop.OpSelectN && p.AuxInt == 0:
			call := p.Args[0]
			if !isStaticCall(call) {
				return nil, "pointer comes from " + call.Op.String()
			}
			switch fn := call.Aux.(*ssa.AuxCall).Fn; fn {
			case ir.Syms.Growslice, ir.Syms.GrowsliceNoAlias, ir.Syms.GrowsliceBuf, ir.Syms.GrowsliceBufNoAlias:
				if c.Op != ssaop.OpSelectN || c.AuxInt != 2 || c.Args[0] != call {
					return nil, "capacity is not that of the allocation"
				}
			default:
				if (fn.Name != "runtime.makeslice" && fn.Name != "runtime.makeslice64") || len(call.Args) != 4 {
					return nil, "pointer comes from " + fn.Name
				}
				if stripCopies(call.Args[2]) != c {
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
	case ssaop.OpStaticCall, ssaop.OpStaticLECall:
		fn := u.Aux.(*ssa.AuxCall).Fn
		if _, n := noAliasGrow(fn); n != 0 || fn == ir.Syms.GrowsliceNoAlias || fn == ir.Syms.GrowsliceBufNoAlias {
			// Copies the old array. Freeing it is decided at that call.
			return u.Args[0] == a
		}
		if fn == ir.Syms.Memmove || fn == ir.Syms.Memequal || nonRetaining[fn.Name] {
			return true
		}
		return o.paramsDoNotRetain(u, a)
	}
	return false
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
