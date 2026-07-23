package leader

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// plan is how the leader records its execution plan: after ruling, it
// declares fix steps that a fast craftsman will later drain. Each step
// lands in the ledger as pending, and the tool echoes the short ids so
// the leader can reference them.
func TestPlanAddsSteps(t *testing.T) {
	led := newLedger(t)
	p := &PlanTool{Ledger: led}

	in := `{"steps":[
		{"target":"state.go:List.Append","intent":"guard with island mutex","test":"concurrent Append keeps all elements"},
		{"target":"live.go:Live.Close","intent":"release the timer on the error path","test":"timer stopped when handler errors"}
	]}`
	out, isErr := p.Run(context.Background(), json.RawMessage(in))
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if got := len(led.PendingSteps()); got != 2 {
		t.Fatalf("want 2 pending steps, got %d", got)
	}
	if !strings.Contains(out, "2 step") {
		t.Errorf("summary should report the count: %q", out)
	}
}

func TestPlanEmptyIsError(t *testing.T) {
	p := &PlanTool{Ledger: newLedger(t)}
	if _, isErr := p.Run(context.Background(), json.RawMessage(`{"steps":[]}`)); !isErr {
		t.Fatal("empty steps[] must be an error result")
	}
}

// A step with no target or no intent is unactionable for the craftsman —
// it must be rejected, not silently planned.
func TestPlanRejectsIncompleteStep(t *testing.T) {
	p := &PlanTool{Ledger: newLedger(t)}
	in := `{"steps":[{"target":"","intent":"do something","test":"t"}]}`
	if _, isErr := p.Run(context.Background(), json.RawMessage(in)); !isErr {
		t.Fatal("a step missing its target must be rejected")
	}
}
