package enumerate

import "testing"

func TestNilLiteralArgFlagsContrastOnly(t *testing.T) {
	src := `package p

func fan(scope *int, msg string) {}

func alwaysNil(opt *int) {}

func f(x *int) {
	fan(x, "a")   // non-nil at arg 0
	fan(nil, "b") // nil at arg 0 — CONTRAST → flag
	alwaysNil(nil) // nil is the only value ever passed here → no flag
	alwaysNil(nil)
}
`
	f := parseSrc(t, src)
	got := linesOfLens(NilLiteralArg(f))

	if !got[9] { // fan(nil, "b")
		t.Errorf("nil arg with a non-nil sibling must flag (line 9); got %v", got)
	}
	if got[10] || got[11] { // alwaysNil(nil) — no contrast
		t.Errorf("nil with no non-nil sibling must NOT flag; got %v", got)
	}
	if got[8] { // fan(x, "a") — not a nil arg
		t.Errorf("a non-nil call must not flag; got %v", got)
	}
}

func TestNilLiteralArgNotYetRegistered(t *testing.T) {
	// Held out of the always-on baseline pending D2 discrimination (see defaults.go): it
	// surfaces correct candidates but the solo judge confirms FPs without the callee-summary.
	f := parseSrc(t, "package p\nfunc g(a *int){}\nfunc f(x *int){ g(x); g(nil) }\n")
	base, err := DefaultRegistry().BuildBaseline("go", f.Lang)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range base {
		if l.ID() == "nil-literal-arg" {
			t.Fatal("nil-literal-arg must NOT be registered as baseline until D2 discrimination is wired")
		}
	}
	// The lens itself still works when called directly (pins it for when it IS wired).
	if len(NilLiteralArg(f)) != 1 {
		t.Errorf("expected 1 contrastive nil arg from the lens function, got %d", len(NilLiteralArg(f)))
	}
}
