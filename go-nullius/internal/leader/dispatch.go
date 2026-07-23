package leader

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/anthropics/anthropic-sdk-go"

	"go-nullius/internal/ledger"
)

// subTool is the minimal surface DispatchTool drives: hunt and scout both
// satisfy it. Kept as an interface so the batch/curation logic is testable
// without spawning real subprocesses.
type subTool interface {
	Run(context.Context, json.RawMessage) (string, bool)
}

// deferredHunter is the batch-aware hunt surface: hunt without the inline
// refuter dispatch, returning the ABSENTs so the batch can refute ALL
// hunts' claims in ONE dispatch instead of one per hunt.
type deferredHunter interface {
	RunDeferred(context.Context, json.RawMessage) (string, []ledger.Finding, bool)
}

// DispatchTool is the mechanical intent layer. The smart leader declares a
// BATCH of subagent tasks in ONE call; the orchestrator runs them (the fast
// semaphore inside the scout tool bounds real concurrency), curates the
// reports into a single digest, and hands that back. This keeps the
// leader's turns and resident context bounded: one intent → one curated
// return, instead of many individual tool calls each spilling raw output.
type DispatchTool struct {
	Hunt  subTool // fold-into-ledger hunter (leader.HuntTool)
	Scout subTool // read-only bulk scout (scout.Tool)
}

func (d *DispatchTool) Name() string { return "dispatch" }

func (d *DispatchTool) Def() anthropic.ToolUnionParam {
	return anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
		Name: "dispatch",
		Description: anthropic.String(
			"Mechanical batch dispatch: declare ALL the subagents you want run this round in ONE call. The orchestrator runs them on the fast tier (bounded concurrency), curates the findings, and returns ONE digest. Prefer this over many individual hunt/scout calls — it bounds turns and context. Each task: {kind:\"hunt\", lens, targets[]} folds findings into the ledger; {kind:\"scout\", objective, tier?} runs a read-only bulk dispatch (builds/tests/searches)."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"tasks": map[string]any{
					"type":        "array",
					"description": "batch of subagent intents to run this round",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"kind":       map[string]any{"type": "string", "enum": []string{"hunt", "scout"}},
							"lens":       map[string]any{"type": "string", "description": "hunt: the lens name"},
							"targets":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "hunt: path:symbol targets"},
							"objective":  map[string]any{"type": "string", "description": "scout: the complete self-contained dispatch"},
							"tier":       map[string]any{"type": "string", "enum": []string{"fast", "smart"}, "description": "scout: dispatch tier, default fast"},
							"timeout_ms": map[string]any{"type": "integer", "description": "scout: overall cap"},
						},
					},
				},
			},
			Required: []string{"tasks"},
		},
	}}
}

type dispatchTask struct {
	Kind      string   `json:"kind"`
	Lens      string   `json:"lens"`
	Targets   []string `json:"targets"`
	Objective string   `json:"objective"`
	Tier      string   `json:"tier"`
	TimeoutMs int      `json:"timeout_ms"`
}

func (d *DispatchTool) Run(ctx context.Context, input json.RawMessage) (string, bool) {
	var in struct {
		Tasks []dispatchTask `json:"tasks"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "dispatch: invalid input: need {tasks:[{kind,...}]}", true
	}
	if len(in.Tasks) == 0 {
		return "dispatch: tasks[] is empty — declare at least one {kind:hunt|scout} task", true
	}

	// Fan out; the fast semaphore inside the scout tool caps real
	// parallelism, so these goroutines simply queue on it. Hunts defer
	// their refutation: absents accumulate here and get ONE batched
	// refuter dispatch after the round, instead of one per hunt.
	results := make([]string, len(in.Tasks))
	absents := make([][]ledger.Finding, len(in.Tasks))
	var wg sync.WaitGroup
	for i, t := range in.Tasks {
		wg.Add(1)
		go func(i int, t dispatchTask) {
			defer wg.Done()
			results[i], absents[i] = d.runTask(ctx, i, t)
		}(i, t)
	}
	wg.Wait()

	// Curate: one labeled digest, task order preserved.
	var sb strings.Builder
	fmt.Fprintf(&sb, "dispatch digest — %d task(s):\n", len(in.Tasks))
	for _, r := range results {
		sb.WriteString(r)
		sb.WriteString("\n")
	}

	var all []ledger.Finding
	for _, a := range absents {
		all = append(all, a...)
	}
	if len(all) > 0 && d.Scout != nil {
		raw, _ := json.Marshal(map[string]any{"objective": refuteObjective(all)})
		refReport, refErr := d.Scout.Run(ctx, raw)
		if refErr {
			sb.WriteString("refuter dispatch FAILED (rule the ABSENTs yourself from decisive lines): " + refReport + "\n")
		} else {
			sb.WriteString("REFUTER REPORT (testimony — you rule):\n" + refReport + "\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n"), false
}

func (d *DispatchTool) runTask(ctx context.Context, i int, t dispatchTask) (string, []ledger.Finding) {
	switch t.Kind {
	case "hunt":
		if d.Hunt == nil {
			return fmt.Sprintf("[%d hunt] ERROR: no hunt tool wired", i+1), nil
		}
		raw, _ := json.Marshal(map[string]any{"lens": t.Lens, "targets": t.Targets})
		if dh, ok := d.Hunt.(deferredHunter); ok {
			out, abs, isErr := dh.RunDeferred(ctx, raw)
			return label(i, "hunt "+t.Lens, out, isErr), abs
		}
		out, isErr := d.Hunt.Run(ctx, raw)
		return label(i, "hunt "+t.Lens, out, isErr), nil
	case "scout":
		if d.Scout == nil {
			return fmt.Sprintf("[%d scout] ERROR: no scout tool wired", i+1), nil
		}
		raw, _ := json.Marshal(map[string]any{"objective": t.Objective, "tier": t.Tier, "timeout_ms": t.TimeoutMs})
		out, isErr := d.Scout.Run(ctx, raw)
		return label(i, "scout", out, isErr), nil
	default:
		return fmt.Sprintf("[%d] ERROR: unknown kind %q (want hunt|scout)", i+1, t.Kind), nil
	}
}

func label(i int, kind, out string, isErr bool) string {
	tag := fmt.Sprintf("[%d %s]", i+1, kind)
	if isErr {
		tag += " ERROR"
	}
	return tag + " " + out
}
