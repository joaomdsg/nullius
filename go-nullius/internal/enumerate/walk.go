package enumerate

import (
	"fmt"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
)

// walkNamed calls fn for every named node reachable from root, depth-first. Named
// nodes skip punctuation/anonymous tokens, which is what structural lenses care about.
func walkNamed(root *gts.Node, fn func(n *gts.Node)) {
	if root == nil {
		return
	}
	fn(root)
	for i := 0; i < root.NamedChildCount(); i++ {
		walkNamed(root.NamedChild(i), fn)
	}
}

// countNamed returns the number of named nodes in a parsed file (denominator for the
// selectivity ceiling).
func countNamed(f *ParsedFile) int {
	n := 0
	walkNamed(f.Tree.RootNode(), func(*gts.Node) { n++ })
	return n
}

// enclosingFn returns the name of the smallest NAMED function/method that contains node,
// best-effort. Closures (RoleClosure) are scope boundaries but anonymous, so they are
// skipped for naming — a statement inside a closure reports the nearest named function
// that encloses the closure. Node kinds and the name field come from the LangProfile;
// no profile (or no name) yields "".
func enclosingFn(f *ParsedFile, node *gts.Node) string {
	if node == nil || f.Profile == nil {
		return ""
	}
	target := node.StartByte()
	best := ""
	bestSpan := ^uint32(0)
	walkNamed(f.Tree.RootNode(), func(n *gts.Node) {
		if !f.Profile.Is(RoleFunction, n.Type(f.Lang)) {
			return
		}
		if n.StartByte() <= target && target < n.EndByte() {
			if span := n.EndByte() - n.StartByte(); span < bestSpan {
				if name := nodeText(f.Src, n.ChildByFieldName(f.Profile.nameField, f.Lang)); name != "" {
					bestSpan = span
					best = name
				}
			}
		}
	})
	return best
}

// BoolTautology is a WalkLens body: it flags comparison expressions whose truth value is
// constant by construction — a defect CLASS (dead/always-true guards) that is task-agnostic
// baseline coverage, not fixture-specific. The catch that motivated it: `len(x) >= 0` (always
// true) as a subscription predicate wakes every client on every change (the vialite over-wake
// miss). Purely syntactic, no type info: len(...) compared to 0 in the always-true / always-
// false directions, and comparisons of textually-identical operands (x==x, i<i). The verdict
// is effectively pre-seeded DEFECT (a constant guard is almost never intended), but Judge still
// rules — the lens only FINDS.
func BoolTautology(f *ParsedFile) []Candidate {
	var out []Candidate
	walkNamed(f.Tree.RootNode(), func(n *gts.Node) {
		if n.Type(f.Lang) != "binary_expression" {
			return
		}
		left := n.ChildByFieldName("left", f.Lang)
		right := n.ChildByFieldName("right", f.Lang)
		op := nodeText(f.Src, n.ChildByFieldName("operator", f.Lang))
		if left == nil || right == nil || op == "" {
			return
		}
		reason := tautologyReason(f, left, op, right)
		if reason == "" {
			return
		}
		out = append(out, Candidate{
			File:     f.Path,
			Line:     nodeLine(n),
			Fn:       enclosingFn(f, n),
			Snippet:  nodeText(f.Src, n),
			Facts:    map[string]string{"tautology": reason},
			Evidence: spanLines(n),
		})
	})
	return out
}

// tautologyReason returns a short reason if (left op right) is a constant comparison, else "".
func tautologyReason(f *ParsedFile, left *gts.Node, op string, right *gts.Node) string {
	// len(...) vs 0 — len is always >= 0, so `>= 0` is always true and `< 0` is always false.
	// Both operand orders (len(x) >= 0 and 0 <= len(x)).
	lenLeft, zeroRight := isLenCall(f, left), isZeroLit(f, right)
	zeroLeft, lenRight := isZeroLit(f, left), isLenCall(f, right)
	if lenLeft && zeroRight {
		switch op {
		case ">=":
			return "len(...) >= 0 is always true"
		case "<":
			return "len(...) < 0 is always false"
		}
	}
	if zeroLeft && lenRight {
		switch op {
		case "<=":
			return "0 <= len(...) is always true"
		case ">":
			return "0 > len(...) is always false"
		}
	}
	// Identical operands: x==x / x<=x / x>=x are always true; x!=x / x<x / x>x always false.
	lt, rt := strings.TrimSpace(nodeText(f.Src, left)), strings.TrimSpace(nodeText(f.Src, right))
	if lt != "" && lt == rt {
		switch op {
		case "==", "<=", ">=", "!=", "<", ">":
			return "comparison of identical operands (" + lt + " " + op + " " + rt + ")"
		}
	}
	return ""
}

// LockWithoutRelease is a WalkLens body for the deadlock CLASS: a mutex acquired in a
// function with no matching release anywhere in that same function. It is task-agnostic —
// keyed purely on the sync.(RW)Mutex API method names (Lock/Unlock, RLock/RUnlock), which
// are stdlib vocabulary, never on any project's own identifiers. Per named function it
// counts lock and unlock method calls across the whole body (closures included, so a defer
// Unlock inside a spawned goroutine still counts as a release — over-approximating release
// presence keeps false alarms down); a lock method present with its partner absent flags
// each acquire site. Judge discriminates the residual FPs (a lock deliberately released by
// a callee, a hand-off pattern). Releasing via defer or a direct call both count.
func LockWithoutRelease(f *ParsedFile) []Candidate {
	if f.Profile == nil {
		return nil
	}
	pairs := map[string]string{"Lock": "Unlock", "RLock": "RUnlock"}
	var out []Candidate
	walkNamed(f.Tree.RootNode(), func(fn *gts.Node) {
		k := fn.Type(f.Lang)
		if k != "function_declaration" && k != "method_declaration" {
			return
		}
		present := map[string]bool{}         // method name -> called somewhere in this fn
		acquires := map[string][]*gts.Node{} // acquire method -> call sites
		walkNamed(fn, func(n *gts.Node) {
			if n.Type(f.Lang) != "call_expression" {
				return
			}
			sel := n.ChildByFieldName("function", f.Lang)
			if sel == nil || sel.Type(f.Lang) != "selector_expression" {
				return
			}
			name := nodeText(f.Src, sel.ChildByFieldName("field", f.Lang))
			if name == "" {
				return
			}
			present[name] = true
			if _, isAcquire := pairs[name]; isAcquire {
				acquires[name] = append(acquires[name], n)
			}
		})
		for acq, rel := range pairs {
			if present[rel] {
				continue // a matching release exists somewhere in the function
			}
			for _, site := range acquires[acq] {
				out = append(out, Candidate{
					File:     f.Path,
					Line:     nodeLine(site),
					Fn:       enclosingFn(f, site),
					Snippet:  nodeText(f.Src, site),
					Facts:    map[string]string{"acquire": acq, "missing": rel},
					Evidence: spanLines(fn), // the missing release is a whole-function property
				})
			}
		}
	})
	return out
}

// WriteToGuardedFieldWithoutLock is a WalkLens body for the missing-serialization CLASS: a
// method that WRITES a field of a receiver whose struct declares a sync.(RW)Mutex field, yet
// acquires no lock in the method body. Task-agnostic — the guard signal is a field whose type
// text contains "Mutex" (the sync API), never a project-specific mutex name. Over-approximate
// by design (DESIGN Q3): constructors, single-threaded init, and atomic-guarded writes will be
// flagged and left for Judge to clear — the lens only FINDS candidate unsynchronized writes.
func WriteToGuardedFieldWithoutLock(f *ParsedFile) []Candidate {
	if f.Profile == nil {
		return nil
	}
	guarded := guardedStructs(f)
	if len(guarded) == 0 {
		return nil
	}
	var out []Candidate
	walkNamed(f.Tree.RootNode(), func(fn *gts.Node) {
		if fn.Type(f.Lang) != "method_declaration" {
			return
		}
		recvName, recvType := receiverOf(f, fn)
		if recvType == "" || !guarded[recvType] {
			return
		}
		locked := false
		var writes []*gts.Node
		walkNamed(fn, func(n *gts.Node) {
			switch n.Type(f.Lang) {
			case "call_expression":
				if sel := n.ChildByFieldName("function", f.Lang); sel != nil && sel.Type(f.Lang) == "selector_expression" {
					switch nodeText(f.Src, sel.ChildByFieldName("field", f.Lang)) {
					case "Lock", "RLock":
						locked = true
					}
				}
			case "assignment_statement":
				if left := n.ChildByFieldName("left", f.Lang); left != nil {
					for i := 0; i < left.NamedChildCount(); i++ {
						if writesReceiverField(f, left.NamedChild(i), recvName) {
							writes = append(writes, n)
							break
						}
					}
				}
			}
		})
		if locked || len(writes) == 0 {
			return
		}
		for _, w := range writes {
			out = append(out, Candidate{
				File:     f.Path,
				Line:     nodeLine(w),
				Fn:       enclosingFn(f, w),
				Snippet:  nodeText(f.Src, w),
				Facts:    map[string]string{"guarded_struct": recvType},
				Evidence: spanLines(w),
			})
		}
	})
	return out
}

// guardedStructs returns the set of struct type names in f that declare a field whose type
// mentions a Mutex (sync.Mutex / sync.RWMutex) — the structs whose fields are meant to be
// lock-guarded.
func guardedStructs(f *ParsedFile) map[string]bool {
	out := map[string]bool{}
	walkNamed(f.Tree.RootNode(), func(n *gts.Node) {
		if n.Type(f.Lang) != "type_spec" {
			return
		}
		name := nodeText(f.Src, n.ChildByFieldName("name", f.Lang))
		body := n.ChildByFieldName("type", f.Lang)
		if name == "" || body == nil || body.Type(f.Lang) != "struct_type" {
			return
		}
		hasMutex := false
		walkNamed(body, func(fd *gts.Node) {
			if fd.Type(f.Lang) != "field_declaration" {
				return
			}
			if strings.Contains(nodeText(f.Src, fd.ChildByFieldName("type", f.Lang)), "Mutex") {
				hasMutex = true
			}
		})
		if hasMutex {
			out[name] = true
		}
	})
	return out
}

// receiverOf returns the receiver variable name and base type name of a method_declaration
// (pointer receivers unwrapped: `func (s *S)` → "s","S").
func receiverOf(f *ParsedFile, fn *gts.Node) (name, typ string) {
	recv := fn.ChildByFieldName("receiver", f.Lang)
	if recv == nil {
		return "", ""
	}
	var pd *gts.Node
	walkNamed(recv, func(n *gts.Node) {
		if pd == nil && n.Type(f.Lang) == "parameter_declaration" {
			pd = n
		}
	})
	if pd == nil {
		return "", ""
	}
	name = nodeText(f.Src, pd.ChildByFieldName("name", f.Lang))
	t := pd.ChildByFieldName("type", f.Lang)
	if t != nil && t.Type(f.Lang) == "pointer_type" && t.NamedChildCount() > 0 {
		t = t.NamedChild(0)
	}
	return name, nodeText(f.Src, t)
}

// writesReceiverField reports whether an lvalue node is `recv.field` (a write to a field of
// the method's receiver).
func writesReceiverField(f *ParsedFile, lval *gts.Node, recvName string) bool {
	if lval == nil || recvName == "" || lval.Type(f.Lang) != "selector_expression" {
		return false
	}
	return nodeText(f.Src, lval.ChildByFieldName("operand", f.Lang)) == recvName
}

// NilLiteralArg is a WalkLens body for the scope-confinement / D2-vs-FP CLASS: a call passing
// a literal `nil` in an argument position where SIBLING calls of the same callee pass a
// non-nil value. The contrast is the signal — a nil that is always nil is likely a genuine
// optional; a nil where the same callee elsewhere receives a real value is the shape of a
// scope/broadcast leak (pass-nil = app-wide instead of the owning scope). Task-agnostic: it
// reasons purely about literal nil vs non-nil at matching callee/arg positions, never about
// any project's parameter names. Judge and pair-discrimination sort the safe broadcast-all
// case from the leak — this lens only surfaces the contrastive candidates.
func NilLiteralArg(f *ParsedFile) []Candidate {
	type slot struct{ nilArg, nonNil bool }
	type site struct {
		callee string
		idx    int
		arg    *gts.Node
		call   *gts.Node
	}
	slots := map[string]map[int]*slot{}
	var sites []site
	walkNamed(f.Tree.RootNode(), func(n *gts.Node) {
		if n.Type(f.Lang) != "call_expression" {
			return
		}
		callee := nodeText(f.Src, n.ChildByFieldName("function", f.Lang))
		args := n.ChildByFieldName("arguments", f.Lang)
		if callee == "" || args == nil {
			return
		}
		for i := 0; i < args.NamedChildCount(); i++ {
			a := args.NamedChild(i)
			if slots[callee] == nil {
				slots[callee] = map[int]*slot{}
			}
			if slots[callee][i] == nil {
				slots[callee][i] = &slot{}
			}
			if strings.TrimSpace(nodeText(f.Src, a)) == "nil" {
				slots[callee][i].nilArg = true
				sites = append(sites, site{callee, i, a, n})
			} else {
				slots[callee][i].nonNil = true
			}
		}
	})
	var out []Candidate
	for _, s := range sites {
		if !slots[s.callee][s.idx].nonNil {
			continue // nil is the only value ever passed here → likely a genuine optional, skip
		}
		out = append(out, Candidate{
			File:     f.Path,
			Line:     nodeLine(s.arg),
			Fn:       enclosingFn(f, s.arg),
			Snippet:  nodeText(f.Src, s.call),
			Facts:    map[string]string{"callee": s.callee, "arg_index": fmt.Sprintf("%d", s.idx)},
			Evidence: spanLines(s.call),
		})
	}
	return out
}

func isLenCall(f *ParsedFile, n *gts.Node) bool {
	if n == nil || n.Type(f.Lang) != "call_expression" {
		return false
	}
	return nodeText(f.Src, n.ChildByFieldName("function", f.Lang)) == "len"
}

func isZeroLit(f *ParsedFile, n *gts.Node) bool {
	return n != nil && n.Type(f.Lang) == "int_literal" && strings.TrimSpace(nodeText(f.Src, n)) == "0"
}

// StmtAfterReturn is a WalkLens body: it flags any statement that is a later sibling of a
// return within the same statement sequence — unreachable code. This is a pure
// statement-ORDER property (sibling index ordering) that tree-sitter queries cannot
// express — the demonstrator for the walk backend. It is closure-safe by construction:
// each RoleStmtList is processed independently, so a return inside a closure cannot mark
// statements in an enclosing scope. Role-driven, so language-agnostic given a profile.
func StmtAfterReturn(f *ParsedFile) []Candidate {
	if f.Profile == nil {
		return nil
	}
	var out []Candidate
	walkNamed(f.Tree.RootNode(), func(n *gts.Node) {
		if !f.Profile.Is(RoleStmtList, n.Type(f.Lang)) {
			return
		}
		seenReturn := false
		for i := 0; i < n.NamedChildCount(); i++ {
			c := n.NamedChild(i)
			kind := c.Type(f.Lang)
			if seenReturn && kind != "comment" {
				out = append(out, Candidate{
					File:     f.Path,
					Line:     nodeLine(c),
					Fn:       enclosingFn(f, c),
					Snippet:  nodeText(f.Src, c),
					Facts:    map[string]string{"unreachable": nodeText(f.Src, c)},
					Evidence: spanLines(c),
				})
			}
			if f.Profile.Is(RoleReturn, kind) {
				seenReturn = true
			}
		}
	})
	return out
}
