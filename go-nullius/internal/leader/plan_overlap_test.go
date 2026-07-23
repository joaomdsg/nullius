package leader

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Over-decomposition (several pending steps on one file) makes serial
// craftsmen no-ops or conflicts — plan must warn so the leader merges
// before draining.
func TestPlanWarnsOnSameFilePendingOverlap(t *testing.T) {
	p := &PlanTool{Ledger: newLedger(t)}
	out, isErr := p.Run(context.Background(), json.RawMessage(
		`{"steps":[{"target":"a.go:F","intent":"add lock"},{"target":"a.go:G","intent":"add unlock"}]}`))
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if !strings.Contains(out, "WARNING") || !strings.Contains(out, "a.go") {
		t.Errorf("same-file overlap must warn: %q", out)
	}
}

func TestPlanNoWarningOnDistinctFiles(t *testing.T) {
	p := &PlanTool{Ledger: newLedger(t)}
	out, _ := p.Run(context.Background(), json.RawMessage(
		`{"steps":[{"target":"a.go:F","intent":"add lock"},{"target":"b.go:G","intent":"release timer"}]}`))
	if strings.Contains(out, "WARNING") {
		t.Errorf("distinct files must not warn: %q", out)
	}
}
