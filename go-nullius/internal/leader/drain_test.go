package leader

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// drain is the mechanical implementation phase: a fast craftsman drains
// every pending plan step in its own throwaway context, runs its local
// test, and the orchestrator marks each step done/failed from the
// craftsman's verbatim RESULT line — then hands the leader ONE summary to
// audit. This pins that routing, the marking, and the summary.
func TestDrainRunsPendingSteps(t *testing.T) {
	led := newLedger(t)
	led.AddStep("state.go:Append", "guard with mutex", "concurrent append keeps all")
	led.AddStep("live.go:Close", "release timer", "timer stopped on error")

	craft := &fakeSub{reply: "wrote fix + test\nRESULT: DONE local test EXIT:0"}
	d := &DrainTool{Ledger: led, Craftsman: craft}

	out, isErr := d.Run(context.Background(), json.RawMessage(`{}`))
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	// Both steps drained → none left pending.
	if got := len(led.PendingSteps()); got != 0 {
		t.Fatalf("steps left pending after drain: %d", got)
	}
	// Each step carries its objective (target + intent) to the craftsman.
	if len(craft.calls) != 2 {
		t.Fatalf("want 2 craftsman calls, got %d", len(craft.calls))
	}
	if !strings.Contains(craft.calls[0], "state.go:Append") && !strings.Contains(craft.calls[1], "state.go:Append") {
		t.Errorf("craftsman never got the Append target: %v", craft.calls)
	}
	if !strings.Contains(out, "2 step") || !strings.Contains(out, "done") {
		t.Errorf("summary should report drained counts: %q", out)
	}
}

// A craftsman that cannot prove its fix (no DONE, or an error) marks the
// step FAILED — never silently done — so the leader's audit sees it.
func TestDrainMarksFailure(t *testing.T) {
	led := newLedger(t)
	led.AddStep("a.go:F", "do X", "asserts X")

	craft := &fakeSub{reply: "RESULT: FAILED test still red"}
	d := &DrainTool{Ledger: led, Craftsman: craft}
	out, _ := d.Run(context.Background(), json.RawMessage(`{}`))

	steps := led.SnapshotSteps()
	if len(steps) != 1 || steps[0].Status != "failed" {
		t.Fatalf("unproven fix must be marked failed: %+v", steps)
	}
	if !strings.Contains(out, "failed") {
		t.Errorf("summary must surface the failure: %q", out)
	}
}

func TestDrainNoPendingSteps(t *testing.T) {
	d := &DrainTool{Ledger: newLedger(t), Craftsman: &fakeSub{}}
	out, isErr := d.Run(context.Background(), json.RawMessage(`{}`))
	if !isErr || !strings.Contains(out, "no pending") {
		t.Fatalf("drain with nothing planned must error clearly: %q isErr=%v", out, isErr)
	}
}
