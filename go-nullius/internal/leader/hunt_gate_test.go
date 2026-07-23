package leader

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-nullius/internal/ledger"
)

// A fabricated quote (not on disk anywhere) must never reach the checklist
// as a defect: the gate downgrades it to AMBIGUOUS mechanically — zero LLM
// turns — and the hunt output names the rejection.
func TestQuoteGateRejectsFabrication(t *testing.T) {
	l := newLedger(t)
	dir := huntWorkspace(t)
	report := "V|q.go:retry|ABSENT|q.go:9|`this line was never written by anyone`"
	fs := &fakeScout{replies: []string{report, "n/a"}}
	h := &HuntTool{Ledger: l, Scout: fs, Dir: dir}

	out, isErr := h.Run(context.Background(), huntInput(t))
	if isErr {
		t.Fatal(out)
	}
	if !strings.Contains(out, "1 REJECTED") || !strings.Contains(out, "AMBIGUOUS") {
		t.Errorf("fabricated quote must be rejected and downgraded:\n%s", out)
	}
	if len(fs.objectives) != 1 {
		t.Error("downgraded finding is no longer ABSENT — no refuter dispatch")
	}
	for _, ru := range l.Snapshot() {
		if ru.Finding.Verdict == "ABSENT" {
			t.Errorf("fabrication filed as defect: %+v", ru.Finding)
		}
	}
}

// A verdict on an unopened (unreadable) file is testimony about nothing:
// downgraded, never filed as a defect.
func TestQuoteGateRejectsUnopenedFile(t *testing.T) {
	l := newLedger(t)
	report := "V|ghost.go:F|ABSENT|ghost.go:3|`count++`"
	fs := &fakeScout{replies: []string{report}}
	h := &HuntTool{Ledger: l, Scout: fs, Dir: t.TempDir()}

	out, _ := h.Run(context.Background(), huntInput(t))
	if !strings.Contains(out, "1 REJECTED") || !strings.Contains(out, "unopened") {
		t.Errorf("verdict on unreadable file must be rejected:\n%s", out)
	}
}

// A real quote with a drifted anchor is relocated, not punished: the line
// is corrected and the snippet replaced with the disk text.
func TestQuoteGateRelocatesDriftedAnchor(t *testing.T) {
	l := newLedger(t)
	dir := huntWorkspace(t)
	report := "V|q.go:retry|ABSENT|q.go:42|`pending := p // read without lock`"
	fs := &fakeScout{replies: []string{report, "refuter says fine"}}
	h := &HuntTool{Ledger: l, Scout: fs, Dir: dir}

	out, _ := h.Run(context.Background(), huntInput(t))
	if !strings.Contains(out, "1 relocated") {
		t.Errorf("drifted anchor must relocate, not reject:\n%s", out)
	}
	found := false
	for _, ru := range l.Snapshot() {
		if ru.Finding.Verdict == "ABSENT" && ru.Finding.Line == 9 {
			found = true
		}
	}
	if !found {
		t.Error("relocated finding must carry the corrected line (9)")
	}
}

// The gate replaces hunter prose with the REAL disk snippet so fabricated
// paraphrase never propagates to the leader: whitespace-normalized match,
// snippet rewritten to the trimmed disk line.
func TestQuoteGateGroundsSnippetToDisk(t *testing.T) {
	l := newLedger(t)
	dir := huntWorkspace(t)
	// Hunter quotes with mangled whitespace — still the same bytes normalized.
	report := "V|q.go:retry|ABSENT|q.go:9|`pending  :=  p   // read without lock`"
	fs := &fakeScout{replies: []string{report, "n/a"}}
	h := &HuntTool{Ledger: l, Scout: fs, Dir: dir}
	h.Run(context.Background(), huntInput(t))

	for _, ru := range l.Snapshot() {
		if ru.Finding.Verdict == "ABSENT" && ru.Finding.SnippetHead != "pending := p // read without lock" {
			t.Errorf("snippet must be the disk line, got %q", ru.Finding.SnippetHead)
		}
	}
}

// An unbounded weak hunter flags everything: ABSENT verdicts are capped
// per dispatch; the overflow is reported un-filed for a narrower re-hunt.
func TestHuntCapsDefectVerdicts(t *testing.T) {
	l := newLedger(t)
	dir := t.TempDir()
	var report strings.Builder
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("f%d.go", i)
		src := fmt.Sprintf("package p\n\nfunc F%d() {\n\tcount%d++ // no lock\n}\n", i, i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&report, "V|%s:F%d|ABSENT|%s:4|`count%d++ // no lock`\n", name, i, name, i)
	}
	fs := &fakeScout{replies: []string{report.String(), "refuter report"}}
	h := &HuntTool{Ledger: l, Scout: fs, Dir: dir}

	out, _ := h.Run(context.Background(), huntInput(t))
	if got := l.CountOpen(); got != maxDefectsPerHunt {
		t.Errorf("open items = %d, want cap %d", got, maxDefectsPerHunt)
	}
	if !strings.Contains(out, "OVER-CAP") {
		t.Errorf("overflow must be reported un-filed:\n%s", out)
	}
}

// PRESENT/ABSENT without a parseable path:line locator cannot be gated —
// dropped as unanchored testimony. AMBIGUOUS needs no locator.
func TestParseFindingsRequiresAnchor(t *testing.T) {
	report := "V|q.go:flush|ABSENT|no anchor here|`quote`\n" +
		"V|q.go:flush|ABSENT|`quote-in-locator-slot`\n" +
		"V|q.go:retry|AMBIGUOUS|caller lock unknown; read callers of retry\n" +
		"prose noise"
	fs := parseFindings(report, "serialization")
	if len(fs) != 1 || fs[0].Verdict != "AMBIGUOUS" || fs[0].File != "q.go" || fs[0].Fn != "retry" {
		t.Errorf("want only the AMBIGUOUS finding, got %+v", fs)
	}
}

// Gate unit coverage for the pure function itself: verified within fuzz.
func TestGateQuoteVerifiedWithinFuzz(t *testing.T) {
	dir := huntWorkspace(t)
	f := ledger.Finding{File: "q.go", Line: 8, Verdict: "ABSENT",
		SnippetHead: "pending := p // read without lock"} // real line is 9 → within ±3
	if got := gateQuote(dir, &f); got != gateVerified {
		t.Fatalf("want verified, got %s (%+v)", got, f)
	}
	if f.Line != 9 {
		t.Errorf("anchor must snap to the real line, got %d", f.Line)
	}
}
