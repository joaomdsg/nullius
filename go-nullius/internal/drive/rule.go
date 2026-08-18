package drive

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"go-nullius/internal/dispatch"
	"go-nullius/internal/mandate"
)

// Plan pins one confirmed defect's fix: mechanism, test name, sketch, blast
// radius (mirrors go-nullius/internal/machine's PlanOut shape,
// DESIGN-mandates.md §7 — an unpinned plan re-creates the measured hazard of
// a craftsman botching its own fix identically to how it found the bug).
type Plan struct {
	Target      string
	Intent      string
	TestName    string
	TestSketch  string
	BlastRadius string
}

// Patch is a --rule-patches unified diff for a trivial fix, applied
// mechanically by EXECUTE rather than handed to a craftsman session
// (DESIGN-mandates.md §7, PITTED, default off).
type Patch struct {
	Target string
	Diff   string
}

type ruleSuspectOut struct {
	Target      string `json:"target"`
	Disposition string `json:"disposition"` // CONFIRMED | DISMISSED | RISK
	Intent      string `json:"intent,omitempty"`
	TestName    string `json:"test_name,omitempty"`
	TestSketch  string `json:"test_sketch,omitempty"`
	BlastRadius string `json:"blast_radius,omitempty"`
	Patch       string `json:"patch,omitempty"`
}

type ruleModelOut struct {
	Suspects []ruleSuspectOut `json:"suspects"`
}

// RuleResult is RULE's disposed checklist plus the fixes it pinned.
type RuleResult struct {
	Entries []mandate.ChecklistEntry
	Plans   []Plan
	Patches []Patch
}

// runRule is the RULE frontier dispatch: checklist + mechanically-extracted
// enclosing-function windows + mandate CONTRACT, nothing else
// (DESIGN-mandates.md §4: "the frontier model sees checklist + windows +
// mandate, nothing else"). It does NOT itself enforce "no line left
// unruled" — that mechanical gate lives in the drive loop via
// mandate.AllDisposed, since a dispatch failure must leave suspects
// genuinely undisposed rather than the model faking completeness.
func (m *Machine) runRule(ctx context.Context, s *mandate.State, entries []mandate.ChecklistEntry, contract string, rulePatches bool) RuleResult {
	// A PRESENT verdict with a quoted mechanism IS the protecting mechanism
	// on record — clear it mechanically rather than letting it block
	// AllDisposed. PRESENT without a quote stays undisposed: fail closed.
	for i, e := range entries {
		if e.Verdict == mandate.Present && e.Disposition == mandate.DispUndisposed && strings.TrimSpace(e.Quote) != "" {
			entries[i].Disposition = mandate.DispDismissed
			entries[i].Note = "mechanism present — auto-cleared on quoted testimony"
		}
	}

	suspects := suspectsOf(entries)
	if len(suspects) == 0 {
		return RuleResult{Entries: entries}
	}

	prompt := rulePrompt(m.cfg.Root, contract, suspects, rulePatches)
	req := dispatch.Request{Tier: dispatch.TierFrontier, Objective: "rule", Prompt: prompt, MaxTokens: 6000}
	resp, err := m.cfg.Adapter.Dispatch(ctx, req)
	s.Receipts = append(s.Receipts, mandate.Receipt{Phase: PhaseRule, Agent: "frontier/rule", Tokens: resp.Tokens, Ms: resp.Ms})

	byTarget := map[string]ruleSuspectOut{}
	if err == nil {
		var out ruleModelOut
		if perr := extractJSON(resp.Text, &out); perr == nil {
			for _, so := range out.Suspects {
				byTarget[so.Target] = so
			}
		}
	}

	res := RuleResult{Entries: append([]mandate.ChecklistEntry(nil), entries...)}
	for i, e := range res.Entries {
		if !isSuspect(e) {
			continue
		}
		so, ok := byTarget[e.Target]
		if !ok {
			continue // left undisposed: mechanical "no line left unruled" gate blocks advance
		}
		switch strings.ToUpper(strings.TrimSpace(so.Disposition)) {
		case "CONFIRMED":
			res.Entries[i].Disposition = mandate.DispConfirmed
			if rulePatches && strings.TrimSpace(so.Patch) != "" {
				res.Patches = append(res.Patches, Patch{Target: e.Target, Diff: so.Patch})
			} else {
				res.Plans = append(res.Plans, Plan{
					Target:      e.Target,
					Intent:      orDefault(so.Intent, "address the confirmed defect: "+e.Quote),
					TestName:    orDefault(so.TestName, "TestFix_"+sanitizeIdent(e.Target)),
					TestSketch:  orDefault(so.TestSketch, "add a test that fails on the current behavior and passes after the fix"),
					BlastRadius: orDefault(so.BlastRadius, "unknown (plan fallback)"),
				})
			}
		case "DISMISSED":
			res.Entries[i].Disposition = mandate.DispDismissed
		case "RISK":
			res.Entries[i].Disposition = mandate.DispRisk
		default:
			// unrecognized disposition: leave undisposed, fail closed
		}
	}
	return res
}

func suspectsOf(entries []mandate.ChecklistEntry) []mandate.ChecklistEntry {
	var out []mandate.ChecklistEntry
	for _, e := range entries {
		if isSuspect(e) {
			out = append(out, e)
		}
	}
	return out
}

func isSuspect(e mandate.ChecklistEntry) bool {
	return e.Verdict == mandate.Absent || e.Verdict == mandate.Ambiguous
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func sanitizeIdent(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "Defect"
	}
	return b.String()
}

// parseLocation splits a "path:line" location into its parts.
func parseLocation(loc string) (file string, line int, ok bool) {
	i := strings.LastIndexByte(loc, ':')
	if i < 0 {
		return "", 0, false
	}
	file = loc[:i]
	n, err := strconv.Atoi(loc[i+1:])
	if err != nil {
		return "", 0, false
	}
	return file, n, true
}

func rulePrompt(root, contract string, suspects []mandate.ChecklistEntry, rulePatches bool) string {
	var b strings.Builder
	b.WriteString("You are the RULE ruling. Every suspect below MUST end with a disposition: CONFIRMED, DISMISSED, or RISK. You see only the checklist, the enclosing-function windows, and the mandate CONTRACT — nothing else.\n\n")
	fmt.Fprintf(&b, "CONTRACT:\n%s\n\n", contract)
	b.WriteString("Suspects:\n")
	for _, e := range suspects {
		fmt.Fprintf(&b, "- target=%q lens=%q verdict=%s location=%s\n", e.Target, e.Lens, e.Verdict, e.Location)
		if e.Quote != "" {
			fmt.Fprintf(&b, "  quote: %s\n", e.Quote)
		}
		if file, line, ok := parseLocation(e.Location); ok {
			// Resolve against the mandate root, not the process cwd —
			// windows silently vanished whenever nullius ran elsewhere.
			if win, _, _, werr := EnclosingWindow(filepath.Join(root, file), line); werr == nil {
				fmt.Fprintf(&b, "  window:\n%s\n", indent(win))
			}
		}
	}
	if rulePatches {
		b.WriteString("\nFor a CONFIRMED trivial fix, you may emit \"patch\" as a unified diff instead of a plan.\n")
	}
	b.WriteString("\nReply with ONLY a JSON object: {\"suspects\":[{\"target\":\"...\",\"disposition\":\"CONFIRMED|DISMISSED|RISK\",\"intent\":\"...\",\"test_name\":\"...\",\"test_sketch\":\"...\",\"blast_radius\":\"...\"}]}\n")
	return b.String()
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "    " + l
	}
	return strings.Join(lines, "\n")
}
