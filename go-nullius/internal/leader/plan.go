package leader

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"

	"go-nullius/internal/ledger"
)

// PlanTool records the leader's execution plan into the ledger. After
// ruling, the leader declares the fixes it wants built as a batch of
// steps; a fast craftsman later drains them (see DrainTool). Planning is
// mechanical bookkeeping — the JUDGMENT (what to fix, the mechanism) is
// the leader's and rides in each step's intent.
type PlanTool struct {
	Ledger *ledger.Ledger
}

func (p *PlanTool) Name() string { return "plan" }

func (p *PlanTool) Def() anthropic.ToolUnionParam {
	return anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
		Name: "plan",
		Description: anthropic.String(
			"Record your execution plan: declare the fixes to build as a batch of steps. A fast craftsman drains them later (via drain), each in a throwaway context, so keep every step self-contained. Each step: {target: \"file.go:Symbol\" the fix touches, intent: the change AND the intended mechanism, test: the behavior its pinning test must assert}. ONE step per distinct change — do NOT split a single coherent fix into overlapping steps on the same file (craftsmen drain serially; the second would find the work already done). Planning does not implement — it queues work for the craftsman."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"steps": map[string]any{
					"type":        "array",
					"description": "execution-plan steps to queue for the craftsman",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"target": map[string]any{"type": "string", "description": "file.go:Symbol / file.go:line the fix touches"},
							"intent": map[string]any{"type": "string", "description": "the change to make and the intended mechanism"},
							"test":   map[string]any{"type": "string", "description": "the behavior the pinning test must assert"},
						},
						"required": []string{"target", "intent"},
					},
				},
			},
			Required: []string{"steps"},
		},
	}}
}

// pendingTargetOverlap lists files targeted by more than one PENDING step.
func pendingTargetOverlap(l *ledger.Ledger) []string {
	byFile := map[string]int{}
	for _, s := range l.PendingSteps() {
		f := s.Target
		if i := strings.IndexByte(f, ':'); i >= 0 {
			f = f[:i]
		}
		byFile[f]++
	}
	var dups []string
	for f, n := range byFile {
		if n > 1 {
			dups = append(dups, fmt.Sprintf("%s (%d steps)", f, n))
		}
	}
	sortStrings(dups)
	return dups
}

type planStep struct {
	Target string `json:"target"`
	Intent string `json:"intent"`
	Test   string `json:"test"`
}

func (p *PlanTool) Run(_ context.Context, input json.RawMessage) (string, bool) {
	var in struct {
		Steps []planStep `json:"steps"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "plan: invalid input: need {steps:[{target,intent,test?}]}", true
	}
	if len(in.Steps) == 0 {
		return "plan: steps[] is empty — declare at least one {target,intent} step", true
	}
	var ids []string
	for i, s := range in.Steps {
		if strings.TrimSpace(s.Target) == "" || strings.TrimSpace(s.Intent) == "" {
			return fmt.Sprintf("plan: step %d is missing target or intent — a craftsman cannot act on it", i+1), true
		}
		id := p.Ledger.AddStep(s.Target, s.Intent, s.Test)
		ids = append(ids, id[:8])
	}
	if err := p.Ledger.Save(); err != nil {
		return "plan: ledger save failed: " + err.Error(), true
	}
	out := fmt.Sprintf("planned %d step(s), pending drain: %s", len(ids), strings.Join(ids, " "))
	// Same-file pending steps are the over-decomposition smell: serial
	// craftsmen make the later one a no-op (or a conflict). Warn so the
	// leader can merge before draining.
	if dups := pendingTargetOverlap(p.Ledger); len(dups) > 0 {
		out += "\nWARNING: multiple pending steps target the same file — merge overlapping steps into one before draining: " + strings.Join(dups, ", ")
	}
	return out, false
}
