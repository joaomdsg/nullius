package drive

import (
	"fmt"
	"strings"

	"go-nullius/internal/mandate"
)

// RatifyInput is everything RATIFY folds into report.md.
type RatifyInput struct {
	Mode        string
	Entries     []mandate.ChecklistEntry
	ExecResults []ExecResult
	Notes       []string
	Ledger      []mandate.LedgerEntry
}

// renderReport builds report.md: STATUS/FACTS/RISKS/UNKNOWN/ASSUMED — it
// doubles as the ratification record (DESIGN-mandates.md §3/§9).
func renderReport(slug string, in RatifyInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# report — %s\n\n", slug)

	var confirmed, dismissed, risk int
	for _, e := range in.Entries {
		switch e.Disposition {
		case mandate.DispConfirmed:
			confirmed++
		case mandate.DispDismissed:
			dismissed++
		case mandate.DispRisk:
			risk++
		}
	}
	b.WriteString("## STATUS\n\n")
	fmt.Fprintf(&b, "Mode: %s · confirmed: %d · dismissed: %d · risk: %d\n\n", in.Mode, confirmed, dismissed, risk)

	b.WriteString("## FACTS\n\n")
	for _, e := range in.Entries {
		if e.Disposition == mandate.DispConfirmed {
			fmt.Fprintf(&b, "- CONFIRMED %s (%s): %s\n", e.Target, e.Lens, e.Quote)
		}
	}
	for _, r := range in.ExecResults {
		fmt.Fprintf(&b, "- EXECUTE %s: %s (%s)\n", r.Target, r.Status, r.Detail)
	}

	b.WriteString("\n## RISKS\n\n")
	any := false
	for _, e := range in.Entries {
		if e.Disposition == mandate.DispRisk {
			fmt.Fprintf(&b, "- %s (%s): %s\n", e.Target, e.Lens, e.Note)
			any = true
		}
	}
	for _, n := range in.Notes {
		fmt.Fprintf(&b, "- %s\n", n)
		any = true
	}
	if !any {
		b.WriteString("_(none)_\n")
	}

	b.WriteString("\n## UNKNOWN\n\n")
	any = false
	for _, e := range in.Entries {
		if e.Verdict == mandate.Unhunted {
			fmt.Fprintf(&b, "- %s (%s): unhunted\n", e.Target, e.Lens)
			any = true
		}
	}
	if !any {
		b.WriteString("_(none)_\n")
	}

	b.WriteString("\n## ASSUMED\n\n")
	if len(in.Ledger) == 0 {
		b.WriteString("_(none)_\n")
	}
	for _, l := range in.Ledger {
		fmt.Fprintf(&b, "- [%s] %s: %s\n", l.Kind, l.CardID, l.Text)
	}
	return b.String()
}

// ratificationBanner is written into mandate.md's RATIFICATION section:
// layer-2 decisions stand unless objected (DESIGN-mandates.md §4 RATIFY).
const ratificationBanner = "Layer-2 decisions above STAND UNLESS OBJECTED. To object, add a line starting with `OBJECTION:` below and rerun `nullius drive`."

// hasObjection reports whether the user added an OBJECTION: line to the
// RATIFICATION section — objection at any time evaporates consent
// (DESIGN-mandates.md §4).
func hasObjection(ratification string) bool {
	for _, line := range strings.Split(ratification, "\n") {
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(line)), "OBJECTION:") {
			return true
		}
	}
	return false
}
