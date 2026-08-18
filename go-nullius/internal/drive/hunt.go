package drive

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"go-nullius/internal/dispatch"
	"go-nullius/internal/mandate"
)

// runHunt dispatches one hunter per lens, each WITH its exact RECON targets
// and nothing else (nullius-lens-hunter.md convention; SKILL.md: "one
// hunter per lens, each dispatched WITH its terrain"). A hunter's dispatch
// failure marks its whole target set UNHUNTED — which blocks RULE, never a
// silently-empty checklist (DESIGN-mandates.md §4 HUNT fail-closed
// default).
func (m *Machine) runHunt(ctx context.Context, s *mandate.State, recon ReconResult) []mandate.ChecklistEntry {
	return m.runHuntTagged(ctx, s, recon, PhaseHunt, "hunt")
}

// runHuntTagged is runHunt with an overridable phase label and dispatch
// objective prefix, so AUDIT's re-sweep is distinguishable from the
// original HUNT dispatch (same fan-out mechanism, different receipts/call
// identity — resumability must be able to tell "HUNT already ran" from "AUDIT
// re-swept").
func (m *Machine) runHuntTagged(ctx context.Context, s *mandate.State, recon ReconResult, phase, objectivePrefix string) []mandate.ChecklistEntry {
	var entries []mandate.ChecklistEntry
	for _, lens := range Lenses {
		targets := recon.Targets[lens]
		if len(targets) == 0 {
			continue
		}
		req := dispatch.Request{
			Tier:      dispatch.TierScout,
			Objective: objectivePrefix + "/" + lens,
			Prompt:    huntPrompt(lens, targets, m.cfg.Root),
			MaxTokens: 2000,
		}
		resp, err := m.cfg.Adapter.Dispatch(ctx, req)
		s.Receipts = append(s.Receipts, mandate.Receipt{Phase: phase, Agent: "hunter/" + lens, Tokens: resp.Tokens, Ms: resp.Ms})
		if err != nil {
			for _, t := range targets {
				entries = append(entries, mandate.ChecklistEntry{
					Lens: lens, Target: t, Verdict: mandate.Unhunted,
					Note: fmt.Sprintf("hunter dispatch failed: %v", err),
				})
			}
			continue
		}
		lines := parseHunterLines(hunterText(resp.Text))
		covered := map[string]bool{}
		for _, l := range lines {
			covered[l.target] = true
		}
		var missing []string
		for _, t := range targets {
			if !covered[t] {
				missing = append(missing, t)
			}
		}
		if len(missing) > 0 {
			// ONE bounded mechanical retry with only the stragglers —
			// without it an uncovered target dead-ends the drive at RULE.
			req.Prompt = huntPrompt(lens, missing, m.cfg.Root)
			resp2, err2 := m.cfg.Adapter.Dispatch(ctx, req)
			s.Receipts = append(s.Receipts, mandate.Receipt{Phase: phase, Agent: "hunter/" + lens + "/retry", Tokens: resp2.Tokens, Ms: resp2.Ms})
			if err2 == nil {
				lines = append(lines, parseHunterLines(hunterText(resp2.Text))...)
			}
		}
		seen := map[string]bool{}
		for _, l := range lines {
			if seen[l.target] {
				continue
			}
			seen[l.target] = true
			entries = append(entries, mandate.ChecklistEntry{
				Lens: lens, Target: l.target, Verdict: l.verdict,
				Location: l.location, Quote: l.quote,
			})
		}
		for _, t := range targets {
			if !seen[t] {
				entries = append(entries, mandate.ChecklistEntry{
					Lens: lens, Target: t, Verdict: mandate.Unhunted,
					Note: "not covered in hunter reply after retry (cap/overflow)",
				})
			}
		}
	}
	return entries
}

// rehuntUnhunted re-dispatches ONLY the UNHUNTED stragglers, grouped per
// lens, and splices any fresh verdicts over them; a target still uncovered
// stays UNHUNTED — the drive loop's re-entry bound decides whether that
// halts. Without this, stragglers dead-ended the drive at RULE (manual
// state surgery was the only way out).
func (m *Machine) rehuntUnhunted(ctx context.Context, s *mandate.State, entries []mandate.ChecklistEntry) []mandate.ChecklistEntry {
	targets := map[string][]string{}
	for _, e := range entries {
		if e.Verdict == mandate.Unhunted && e.Disposition == mandate.DispUndisposed {
			targets[e.Lens] = append(targets[e.Lens], e.Target)
		}
	}
	if len(targets) == 0 {
		return entries
	}
	fresh := m.runHuntTagged(ctx, s, ReconResult{Targets: targets}, PhaseRule, "rehunt")
	idx := map[string]mandate.ChecklistEntry{}
	for _, f := range fresh {
		idx[f.Lens+"|"+f.Target] = f
	}
	out := make([]mandate.ChecklistEntry, 0, len(entries))
	for _, e := range entries {
		if e.Verdict == mandate.Unhunted && e.Disposition == mandate.DispUndisposed {
			if f, ok := idx[e.Lens+"|"+e.Target]; ok {
				e = f
			}
		}
		out = append(out, e)
	}
	return out
}

// hunterText unwraps a JSON {"lines": [...]} reply (the api adapter is
// JSON-only) into the raw V| line grammar; raw text passes through for
// agentic adapters.
func hunterText(text string) string {
	var out struct {
		Lines []string `json:"lines"`
	}
	if err := extractJSON(text, &out); err == nil && len(out.Lines) > 0 {
		return strings.Join(out.Lines, "\n")
	}
	return text
}

type hunterLine struct {
	target   string
	verdict  mandate.Verdict
	location string
	quote    string
}

// parseHunterLines parses the "V|<target>|VERDICT|path:line|`quote`"
// grammar (nullius-lens-hunter.md); the AMBIGUOUS variant drops
// path:line/quote for a free-text "what would decide it" field, folded into
// quote here.
func parseHunterLines(text string) []hunterLine {
	var out []hunterLine
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "V|") {
			continue
		}
		parts := strings.SplitN(line, "|", 5)
		if len(parts) < 3 {
			continue
		}
		hl := hunterLine{target: strings.TrimSpace(parts[1])}
		switch strings.ToUpper(strings.TrimSpace(parts[2])) {
		case "PRESENT":
			hl.verdict = mandate.Present
		case "ABSENT":
			hl.verdict = mandate.Absent
		case "AMBIGUOUS":
			hl.verdict = mandate.Ambiguous
		default:
			continue
		}
		if len(parts) >= 4 {
			hl.location = strings.TrimSpace(parts[3])
		}
		if len(parts) >= 5 {
			hl.quote = strings.TrimSpace(parts[4])
		}
		out = append(out, hl)
	}
	return out
}

// huntPromptBudget bounds the total inlined-window bytes per hunter prompt.
const huntPromptBudget = 28 * 1024

func huntPrompt(lens string, targets []string, root string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are a nullius lens hunter for the %q lens.\n", lens)
	b.WriteString(lensBrief(lens))
	b.WriteString("Per target, decide from the quoted code below whether the protective mechanism is PRESENT, ABSENT, or AMBIGUOUS.\n")
	b.WriteString("Judge ONLY from the windows shown. Produce one verdict line per target:\n")
	b.WriteString("V|<target>|PRESENT|path:line|`quote`\n")
	b.WriteString("V|<target>|ABSENT|path:line|`quote`\n")
	b.WriteString("V|<target>|AMBIGUOUS|<what would decide it>\n")
	b.WriteString("Reply with ONLY a JSON object: {\"lines\": [\"V|...\", ...]} — one array element per verdict line.\n\n")
	b.WriteString("Targets (windows mechanically extracted by the driver):\n")
	for _, t := range targets {
		fmt.Fprintf(&b, "- %s\n", t)
		if b.Len() > huntPromptBudget {
			b.WriteString("  (window omitted — prompt budget; report AMBIGUOUS)\n")
			continue
		}
		path, sym := splitTarget(t)
		line := symbolLine(root, path, sym)
		if line == 0 {
			b.WriteString("  (symbol not found in file — report AMBIGUOUS)\n")
			continue
		}
		if win, start, _, err := EnclosingWindow(filepath.Join(root, path), line); err == nil {
			fmt.Fprintf(&b, "  window (%s:%d):\n%s\n", path, start, indent(win))
		}
	}
	return b.String()
}
