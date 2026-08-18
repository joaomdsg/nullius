package drive

import (
	"fmt"
	"strings"

	"go-nullius/internal/mandate"
)

// InterviewOutcome is one INTERVIEW-phase pass over the cards currently in
// mandate.md.
type InterviewOutcome struct {
	Blocked       bool
	BlockingIDs   []string
	Answered      []string
	Open          []string
	LedgerEntries []mandate.LedgerEntry
}

// cardLayer infers a parsed card's escape layer from its rendered `blocks:`
// text (mandate.md doesn't separately persist the Layer int — the blocks:
// header IS the escape-analysis verdict, DESIGN-mandates.md §5). Anything
// not explicitly "nothing (...)" is layer-3 — unclassifiable defaults to
// blocking, fail closed.
func cardLayer(c mandate.Card) mandate.Layer {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(c.Blocks)), "nothing") {
		return mandate.Layer2
	}
	return mandate.Layer3
}

// runInterview appends any newly-drafted GATE cards to mandate.md's
// INTERVIEW section (capped, malformed cards rejected before ever being
// written — mandate.ValidateRound), then classifies every card currently in
// the section: answered, PROVISIONAL (layer-2, unanswered), or blocking
// (layer-3, unanswered). Headless mode auto-fills every card's
// recommendation as ASSUMED and never blocks (DESIGN-mandates.md §4/§5).
func (m *Machine) runInterview(s *mandate.State, doc *mandate.Doc, newCards []mandate.Card) (InterviewOutcome, error) {
	if len(newCards) > 0 {
		appended, err := mandate.AppendCards(doc.Interview, newCards)
		if err != nil {
			return InterviewOutcome{}, fmt.Errorf("drive: INTERVIEW: %w", err)
		}
		doc.Interview = appended
	}

	var out InterviewOutcome
	for _, c := range mandate.ParseCards(doc.Interview) {
		if s.Headless {
			out.LedgerEntries = append(out.LedgerEntries, mandate.LedgerEntry{
				CardID: c.ID, Kind: "ASSUMED",
				Text: fmt.Sprintf("%s → %s (headless: recommendation assumed)", c.Question, describeOption(c, c.Recommended())),
			})
			out.Answered = append(out.Answered, c.ID)
			continue
		}
		if c.Answered() {
			out.Answered = append(out.Answered, c.ID)
			continue
		}
		out.Open = append(out.Open, c.ID)
		if cardLayer(c) == mandate.Layer3 {
			out.Blocked = true
			out.BlockingIDs = append(out.BlockingIDs, c.ID)
			continue
		}
		out.LedgerEntries = append(out.LedgerEntries, mandate.LedgerEntry{
			CardID: c.ID, Kind: "PROVISIONAL",
			Text: fmt.Sprintf("%s → %s (unanswered layer-2, proceeding on recommendation)", c.Question, describeOption(c, c.Recommended())),
		})
	}
	return out, nil
}

func describeOption(c mandate.Card, letter string) string {
	for _, o := range c.Options {
		if o.Letter == letter {
			return fmt.Sprintf("%s. %s", o.Letter, o.Text)
		}
	}
	return letter
}

// statusBanner renders the driver-owned mandate.md line 1.
func statusBanner(phase string, open, blocking int) string {
	if blocking > 0 {
		return fmt.Sprintf("%s · %d question(s) open (%d blocking) · answer the blocking card(s) above, then rerun `nullius drive`", phase, open, blocking)
	}
	if open > 0 {
		return fmt.Sprintf("%s · %d question(s) open (0 blocking) · rerun `nullius drive`", phase, open)
	}
	return fmt.Sprintf("%s · rerun `nullius drive`", phase)
}
