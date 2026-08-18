package mandate

import (
	"fmt"
	"os"
	"strings"
)

// AppendCards appends a validated round of cards to the INTERVIEW section
// body — cards accumulate across rounds (mandate.md §3: "driver appends
// cards"); the user's inline edits to earlier rounds are preserved verbatim
// since we only add text, never rewrite what's already there.
func AppendCards(interviewBody string, cards []Card) (string, error) {
	if err := ValidateRound(cards); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(strings.TrimSpace(strings.ReplaceAll(interviewBody, "_(no cards yet)_", "")))
	for _, c := range cards {
		b.WriteString("\n\n")
		b.WriteString(strings.TrimRight(RenderCard(c), "\n"))
	}
	return strings.TrimSpace(b.String()) + "\n", nil
}

// Scaffold creates the file-protocol tree for a new mandate
// (.nullius/mandates/<slug>/) and writes the initial mandate.md, empty
// checklist.md/ledger.md, and state.json. It errors if the mandate already
// exists — init never silently overwrites in-progress work.
func Scaffold(root, slug, head string, opts InitOptions) (*State, error) {
	p := Paths(root, slug)
	if _, err := os.Stat(p.MandateMD); err == nil {
		return nil, fmt.Errorf("mandate: %s already exists at %s", slug, p.Dir)
	}
	if err := os.MkdirAll(p.PlansDir, 0o755); err != nil {
		return nil, fmt.Errorf("mandate: scaffold %s: %w", slug, err)
	}

	intent := strings.TrimSpace(opts.Intent)
	if intent == "" {
		intent = "_(fill in: what should change, and why)_"
	}
	doc := Doc{
		Status:       "INIT · rerun `nullius drive`",
		Intent:       intent,
		Contract:     "_(drafted by GATE; edit before RULE runs)_",
		Interview:    "_(no cards yet)_",
		Ratification: "_(populated at RATIFY)_",
	}
	if err := os.WriteFile(p.MandateMD, []byte(doc.Render()), 0o644); err != nil {
		return nil, fmt.Errorf("mandate: write mandate.md: %w", err)
	}
	if err := WriteChecklist(p.Checklist, nil); err != nil {
		return nil, err
	}
	if err := WriteLedger(p.Ledger, nil); err != nil {
		return nil, err
	}

	s := &State{
		Slug:         slug,
		Phase:        "INIT",
		Mode:         "",
		Head:         head,
		TerrainStamp: head,
		Files:        opts.Files,
		Budgets:      DefaultBudgets(),
		ReconMode:    opts.ReconMode,
		RulePatches:  opts.RulePatches,
		Headless:     opts.Headless,
	}
	if s.ReconMode == "" {
		s.ReconMode = "scouts"
	}
	if err := s.Save(root); err != nil {
		return nil, err
	}
	return s, nil
}

// InitOptions carries the pitted-arm choices fixed at init time.
type InitOptions struct {
	Intent      string
	Files       []string
	Headless    bool
	ReconMode   string // "scouts" | "ast" | "both"
	RulePatches bool
}
