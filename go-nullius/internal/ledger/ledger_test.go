package ledger

import (
	"path/filepath"
	"testing"
)

func f(file, lens, fn, head string) Finding {
	return Finding{File: file, Lens: lens, Fn: fn, SnippetHead: head, Verdict: "PRESENT"}
}

func TestKeyStableAcrossWhitespaceDrift(t *testing.T) {
	a := f("a.go", "fault-survival", "flush", "buf = nil // cleared before write")
	b := f("a.go", "fault-survival", "flush", "  buf = nil   //  cleared before write ")
	if a.Key() != b.Key() {
		t.Error("whitespace drift changed the identity key")
	}
	c := f("a.go", "serialization", "flush", "buf = nil // cleared before write")
	if a.Key() == c.Key() {
		t.Error("different lens produced the same key")
	}
}

func TestFilterAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".nullius", "hunt.json")
	l, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	fixed := f("a.go", "lost-updates", "inc", "count++ without lock")
	refuted := f("b.go", "serialization", "run", "go func() { ... }()")
	novel := f("c.go", "swallowed-errors", "save", "_ = os.Remove(tmp)")

	l.Rule(fixed, StatusFixed, "added mutex + test")
	l.Rule(refuted, StatusRefuted, "quoted: guarded by singleflight at b.go:10")
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}

	l2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	fresh, regressed := l2.Filter([]Finding{fixed, refuted, novel})
	if len(fresh) != 1 || fresh[0].Key() != novel.Key() {
		t.Errorf("fresh = %v, want only the novel finding", fresh)
	}
	if len(regressed) != 1 || regressed[0].Key() != fixed.Key() {
		t.Errorf("regressed = %v, want the previously-fixed finding", regressed)
	}
}

func TestLoadMissingIsEmpty(t *testing.T) {
	l, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil || len(l.Rulings) != 0 {
		t.Fatalf("missing ledger should load empty, got %v / %v", l, err)
	}
}
