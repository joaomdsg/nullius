// Package leader implements the orchestration tools that make go-nullius
// nullius: hunt (lensed haiku hunters over named terrain), rule
// (mechanical quote-verified rulings), close (scout-verified close-out).
package leader

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"

	"go-nullius/internal/ledger"
)

// Dispatcher runs one scout dispatch (scout.Tool in production).
type Dispatcher interface {
	Run(ctx context.Context, input json.RawMessage) (string, bool)
}

// Lenses and their hunting instructions (prose-lens v0; the measured 6/6
// arc came from lensed hunters + the quoted-mechanism grammar).
var Lenses = map[string]string{
	"serialization":     "Is there a lock/serialization mechanism in the entrypoint's OWN body for the shared state it mutates? A lock elsewhere does not count.",
	"fault-survival":    "Anything cleared, overwritten, or dequeued BEFORE its write/send/flush is confirmed is PRESENT (defect) — queues, buffers, retry state, pending maps.",
	"scope-confinement": "At every fan-out/broadcast site: is the scope argument actually applied to the recipient set, or does the fan-out reach beyond its scope?",
	"wake-predicates":   "For every wait/wake predicate: can it be false at wake? Is it read under the same lock its writer holds?",
	"lost-updates":      "Read-modify-write on shared state without holding a lock across the full cycle (count++, map rebuild, load-then-store).",
	"lifecycle-races":   "Background sweeps/TTL expiry racing live use; shutdown/dispose racing in-flight work; use-after-close.",
	"swallowed-errors":  "Errors discarded (_ =, ignored returns, empty catch), logged-and-continued where the caller needed the failure, or masked by a later success.",
	"resource-release":  "Every acquire (file, conn, lock, goroutine, timer, temp file) with a path that skips its release — early returns, error paths, panics.",
}

const findingGrammar = `Output findings as ONE JSON object per line, nothing else around them:
{"file":"<path>","fn":"<function or symbol>","snippet_head":"<first line of the decisive code, verbatim>","verdict":"PRESENT|ABSENT|AMBIGUOUS","detail":"<one sentence: the mechanism found or missing, with path:line anchor>"}
Verdict grammar: PRESENT = the defect IS there (quote the decisive line). ABSENT = you verified the protecting mechanism exists (quote it). AMBIGUOUS = you could not decide (say what blocked you). Every verdict carries its quoted mechanism — an unquoted verdict is worthless.`

type HuntTool struct {
	Ledger *ledger.Ledger
	Scout  Dispatcher
}

func (h *HuntTool) Name() string { return "hunt" }

func (h *HuntTool) Def() anthropic.ToolUnionParam {
	names := make([]string, 0, len(Lenses))
	for k := range Lenses {
		names = append(names, k)
	}
	sort.Strings(names)
	return anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
		Name: "hunt",
		Description: anthropic.String(
			"Dispatch one lensed hunter (haiku scout) over named targets. Findings are filtered against the durable ledger (.nullius/hunt.json): already-ruled items are dropped, previously-fixed items that reappear come back as REGRESSED, fresh items join the open checklist. Every ABSENT is attacked by a batched refuter dispatch. Lenses: " + strings.Join(names, ", ")),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"lens":    map[string]any{"type": "string", "enum": names},
				"targets": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "path:symbol target list from the terrain map"},
			},
		},
	}}
}

func (h *HuntTool) Run(ctx context.Context, input json.RawMessage) (string, bool) {
	var in struct {
		Lens    string   `json:"lens"`
		Targets []string `json:"targets"`
	}
	if err := json.Unmarshal(input, &in); err != nil || in.Lens == "" || len(in.Targets) == 0 {
		return "hunt: invalid input: need {lens, targets[]}", true
	}
	instr, ok := Lenses[in.Lens]
	if !ok {
		return "hunt: unknown lens " + in.Lens, true
	}

	objective := fmt.Sprintf(`You are a lens hunter. Apply EXACTLY ONE lens over the named targets — nothing else.

LENS %q: %s

TARGETS (read each; hunt only these):
%s

%s`, in.Lens, instr, "- "+strings.Join(in.Targets, "\n- "), findingGrammar)

	report, isErr := h.dispatch(ctx, objective)
	if isErr {
		return "hunt: hunter dispatch failed: " + report, true
	}

	findings := parseFindings(report, in.Lens)
	if len(findings) == 0 {
		return "hunt: hunter returned no parseable findings; raw report:\n" + report, false
	}
	fresh, regressed := h.Ledger.Filter(findings)
	dropped := len(findings) - len(fresh) - len(regressed)

	var sb strings.Builder
	fmt.Fprintf(&sb, "hunt %s: %d findings — %d fresh, %d REGRESSED, %d already-ruled (dropped, not re-billed)\n",
		in.Lens, len(findings), len(fresh), len(regressed), dropped)

	var absents []ledger.Finding
	for _, f := range fresh {
		h.Ledger.Rule(f, ledger.StatusOpen, "on checklist")
		fmt.Fprintf(&sb, "  [%s] %s %s %s — %s\n", f.Key()[:8], f.Verdict, f.File, f.Fn, f.Detail)
		if f.Verdict == "ABSENT" {
			absents = append(absents, f)
		}
	}
	for _, f := range regressed {
		h.Ledger.Rule(f, ledger.StatusOpen, "REGRESSED: previously ruled fixed, reappeared")
		fmt.Fprintf(&sb, "  [%s] REGRESSED %s %s — was fixed, reappeared\n", f.Key()[:8], f.File, f.Fn)
	}
	if err := h.Ledger.Save(); err != nil {
		return "hunt: ledger save failed: " + err.Error(), true
	}

	// Refuters attack every ABSENT — one batched dispatch, verbatim back
	// to the leader to rule on.
	if len(absents) > 0 {
		var items strings.Builder
		for _, f := range absents {
			fmt.Fprintf(&items, "- [%s] %s %s: claimed protected because: %s (claimed mechanism head: %q)\n",
				f.Key()[:8], f.File, f.Fn, f.Detail, f.SnippetHead)
		}
		refObj := fmt.Sprintf(`You are a refuter. Each item below was claimed SAFE (verdict ABSENT) under lens %q: %s
Attack each claim: read the cited code and try to show the protecting mechanism does NOT actually cover the suspect (wrong lock, wrong path, wrong lifecycle). Per item output one line:
[<key>] UPHELD <path:line quote of the covering mechanism> | REFUTED <path:line quote showing the gap>

ITEMS:
%s`, in.Lens, Lenses[in.Lens], items.String())
		refReport, refErr := h.dispatch(ctx, refObj)
		if refErr {
			sb.WriteString("refuter dispatch FAILED (rule the ABSENTs yourself from decisive lines): " + refReport + "\n")
		} else {
			sb.WriteString("REFUTER REPORT (testimony — you rule):\n" + refReport + "\n")
		}
	}
	return sb.String(), false
}

func (h *HuntTool) dispatch(ctx context.Context, objective string) (string, bool) {
	raw, _ := json.Marshal(map[string]any{"objective": objective})
	return h.Scout.Run(ctx, raw)
}

// parseFindings scans hunter output for one-JSON-per-line findings,
// skipping anything unparseable — hunters are testimony, not protocol.
func parseFindings(report, lens string) []ledger.Finding {
	var out []ledger.Finding
	for _, line := range strings.Split(report, "\n") {
		line = strings.TrimSpace(line)
		i := strings.IndexByte(line, '{')
		if i < 0 {
			continue
		}
		var f ledger.Finding
		if err := json.Unmarshal([]byte(line[i:]), &f); err != nil {
			continue
		}
		if f.File == "" || f.Verdict == "" {
			continue
		}
		f.Lens = lens
		out = append(out, f)
	}
	return out
}
