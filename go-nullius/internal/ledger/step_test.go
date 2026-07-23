package ledger

import (
	"path/filepath"
	"testing"
)

// The execution plan: the leader adds steps after ruling, a craftsman
// drains the pending ones, and the ledger persists status across the
// drain so a crashed run resumes instead of re-implementing done work.
func TestStepLifecycle(t *testing.T) {
	l, err := Load(filepath.Join(t.TempDir(), "hunt.json"))
	if err != nil {
		t.Fatal(err)
	}
	id := l.AddStep("state.go:List.Append", "guard Append with the island mutex", "concurrent Append keeps every element")
	if id == "" {
		t.Fatal("AddStep returned empty id")
	}
	// Same content is idempotent — re-planning must not duplicate a step.
	if id2 := l.AddStep("state.go:List.Append", "guard Append with the island mutex", "concurrent Append keeps every element"); id2 != id {
		t.Fatalf("duplicate step got a new id: %q vs %q", id2, id)
	}

	pend := l.PendingSteps()
	if len(pend) != 1 || pend[0].Status != StepPending {
		t.Fatalf("want 1 pending step, got %+v", pend)
	}

	if err := l.MarkStep(id, StepDone, "EXIT:0 test passes"); err != nil {
		t.Fatalf("MarkStep: %v", err)
	}
	if got := l.PendingSteps(); len(got) != 0 {
		t.Fatalf("done step still pending: %+v", got)
	}
	snap := l.SnapshotSteps()
	if len(snap) != 1 || snap[0].Status != StepDone || snap[0].Result == "" {
		t.Fatalf("snapshot lost the drained result: %+v", snap)
	}
}

// A prefix (not just the full id) must resolve a step, matching how the
// leader references rulings — and an unknown id must error, never
// silently no-op a mark.
func TestMarkStepPrefixAndUnknown(t *testing.T) {
	l, _ := Load(filepath.Join(t.TempDir(), "hunt.json"))
	id := l.AddStep("a.go:F", "do X", "asserts X")
	if err := l.MarkStep(id[:6], StepFailed, "build broke"); err != nil {
		t.Fatalf("prefix mark failed: %v", err)
	}
	if err := l.MarkStep("deadbeef", StepDone, "x"); err == nil {
		t.Fatal("marking an unknown step id must error")
	}
}

// Steps must survive a save/reload — the drain resumes across a restart.
func TestStepsPersist(t *testing.T) {
	p := filepath.Join(t.TempDir(), "hunt.json")
	l, _ := Load(p)
	id := l.AddStep("x.go:Y", "fix Y", "asserts Y")
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}
	l2, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	pend := l2.PendingSteps()
	if len(pend) != 1 || pend[0].ID != id {
		t.Fatalf("steps did not persist: %+v", pend)
	}
}
