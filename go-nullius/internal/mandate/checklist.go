package mandate

import (
	"fmt"
	"os"
	"strings"
)

// Verdict is a hunter's PRESENT/ABSENT/AMBIGUOUS call on one target
// (nullius-lens-hunter.md grammar), carried through HUNT → RULE →
// checklist.md.
type Verdict string

const (
	Present   Verdict = "PRESENT"
	Absent    Verdict = "ABSENT"
	Ambiguous Verdict = "AMBIGUOUS"
	Unhunted  Verdict = "UNHUNTED" // hunter fan-out failed for this target
)

// Disposition is RULE's per-suspect call: every suspect ends with one
// (DESIGN-mandates.md §4 RULE fail-closed default — "no line left unruled").
type Disposition string

const (
	DispConfirmed  Disposition = "CONFIRMED"
	DispDismissed  Disposition = "DISMISSED"
	DispRisk       Disposition = "RISK" // undecidable but not swept under the rug
	DispUndisposed Disposition = ""     // mechanically blocks phase advance past RULE
)

// ChecklistEntry is one line of checklist.md: a lens hunt over one target,
// plus RULE's disposition once RULE has run.
type ChecklistEntry struct {
	Lens        string
	Target      string
	Verdict     Verdict
	Location    string // path:line
	Quote       string
	Disposition Disposition
	Note        string
}

// WriteChecklist rewrites checklist.md wholesale (never appended — recitation
// is the measured drift counter, DESIGN-mandates.md §3).
func WriteChecklist(path string, entries []ChecklistEntry) error {
	var b strings.Builder
	b.WriteString("# checklist — lens verdicts and dispositions\n\n")
	if len(entries) == 0 {
		b.WriteString("_(no targets hunted yet)_\n")
	}
	for _, e := range entries {
		disp := string(e.Disposition)
		if disp == "" {
			disp = "UNDISPOSED"
		}
		fmt.Fprintf(&b, "- [%s] %s :: %s :: %s", e.Lens, e.Target, e.Verdict, disp)
		if e.Location != "" {
			fmt.Fprintf(&b, " :: %s", e.Location)
		}
		b.WriteString("\n")
		if e.Quote != "" {
			fmt.Fprintf(&b, "  > %s\n", e.Quote)
		}
		if e.Note != "" {
			fmt.Fprintf(&b, "  (%s)\n", e.Note)
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// AllDisposed reports whether every entry has a non-empty disposition — the
// mechanical "no line left unruled" gate that blocks phase advance past
// RULE.
func AllDisposed(entries []ChecklistEntry) (ok bool, undisposed []ChecklistEntry) {
	for _, e := range entries {
		if e.Disposition == DispUndisposed {
			undisposed = append(undisposed, e)
		}
	}
	return len(undisposed) == 0, undisposed
}

// LedgerEntry is one ASSUMED/PROVISIONAL gap, flushed once at RATIFY
// (DESIGN-mandates.md §5: "later gaps join ledger.md, never a dribble").
type LedgerEntry struct {
	CardID string // "" for non-card-derived entries
	Kind   string // "ASSUMED" | "PROVISIONAL"
	Text   string
}

// WriteLedger rewrites ledger.md wholesale.
func WriteLedger(path string, entries []LedgerEntry) error {
	var b strings.Builder
	b.WriteString("# ledger — assumed/provisional gaps\n\n")
	if len(entries) == 0 {
		b.WriteString("_(no gaps recorded)_\n")
	}
	for _, e := range entries {
		if e.CardID != "" {
			fmt.Fprintf(&b, "- [%s] %s: %s\n", e.Kind, e.CardID, e.Text)
		} else {
			fmt.Fprintf(&b, "- [%s] %s\n", e.Kind, e.Text)
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
