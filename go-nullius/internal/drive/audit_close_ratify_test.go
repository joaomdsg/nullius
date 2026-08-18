package drive

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go-nullius/internal/mandate"
)

func TestRunAuditSkipsAlreadySeenSuspects(t *testing.T) {
	fa := &fakeAdapter{responses: map[string]string{
		"audit-hunt/serialization": "V|foo.go|ABSENT|foo.go:1|`no lock`\n",
	}}
	m := newMachine(fa)
	prior := []mandate.ChecklistEntry{
		{Lens: "serialization", Target: "foo.go", Verdict: mandate.Absent, Disposition: mandate.DispDismissed},
	}
	s := &mandate.State{Budgets: mandate.DefaultBudgets()}
	entries := m.runAudit(context.Background(), s, prior, []string{"foo.go"})
	if len(entries) != 1 {
		t.Fatalf("expected the already-seen suspect not to be re-added, got %+v", entries)
	}
}

func TestRunAuditRulesFreshSuspects(t *testing.T) {
	fa := &fakeAdapter{responses: map[string]string{
		"audit-hunt/serialization": "V|foo.go|ABSENT|foo.go:1|`no lock`\n",
		"rule":                     `{"suspects":[{"target":"foo.go","disposition":"RISK"}]}`,
	}}
	m := newMachine(fa)
	s := &mandate.State{Budgets: mandate.DefaultBudgets()}
	entries := m.runAudit(context.Background(), s, nil, []string{"foo.go"})
	found := false
	for _, e := range entries {
		if e.Target == "foo.go" && e.Disposition == mandate.DispRisk {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected fresh suspect ruled RISK, got %+v", entries)
	}
	if s.Budgets.MicroRulings != mandate.DefaultBudgets().MicroRulings-1 {
		t.Fatalf("expected micro-ruling budget decremented, got %d", s.Budgets.MicroRulings)
	}
}

func TestRunAuditExhaustedBudgetForcesRisk(t *testing.T) {
	fa := &fakeAdapter{responses: map[string]string{
		"audit-hunt/serialization": "V|foo.go|ABSENT|foo.go:1|`no lock`\n",
	}}
	m := newMachine(fa)
	s := &mandate.State{Budgets: mandate.Budgets{AuditRounds: 1, MicroRulings: 0}}
	entries := m.runAudit(context.Background(), s, nil, []string{"foo.go"})
	found := false
	for _, e := range entries {
		if e.Target == "foo.go" && e.Disposition == mandate.DispRisk && strings.Contains(e.Note, "budget exhausted") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected budget-exhausted RISK entry, got %+v", entries)
	}
}

func TestRunCloseRecordsVerbatimOutput(t *testing.T) {
	dir := initGitRepo(t)
	m := New(Config{})
	out, ok := m.runClose(context.Background(), dir, goBuildTest, goTest)
	if !ok {
		t.Fatalf("expected clean close to report ok, record: %s", out)
	}
	if !strings.Contains(out, "## build (OK)") {
		t.Fatalf("expected build OK section, got: %s", out)
	}
	if !strings.Contains(out, "## test (OK)") {
		t.Fatalf("expected test OK section, got: %s", out)
	}
}

func TestRunCloseReportsFailure(t *testing.T) {
	dir := initGitRepo(t)
	m := New(Config{})
	out, ok := m.runClose(context.Background(), dir, []string{"false"}, goTest)
	if ok {
		t.Fatal("expected close to report failure when build fails")
	}
	if !strings.Contains(out, "## build (FAILED)") {
		t.Fatalf("expected build FAILED section, got: %s", out)
	}
}

func TestRenderReportCountsDispositions(t *testing.T) {
	in := RatifyInput{
		Mode: "FIX",
		Entries: []mandate.ChecklistEntry{
			{Target: "a", Lens: "serialization", Disposition: mandate.DispConfirmed, Quote: "no lock"},
			{Target: "b", Lens: "wake-predicates", Disposition: mandate.DispDismissed},
			{Target: "c", Lens: "lost-updates", Disposition: mandate.DispRisk, Note: "undecidable"},
		},
		Ledger: []mandate.LedgerEntry{{CardID: "Q1", Kind: "ASSUMED", Text: "recommendation assumed"}},
	}
	report := renderReport("retry-ownership", in)
	if !strings.Contains(report, "confirmed: 1 · dismissed: 1 · risk: 1") {
		t.Fatalf("expected disposition counts, got: %s", report)
	}
	if !strings.Contains(report, "CONFIRMED a") {
		t.Fatalf("expected FACTS entry, got: %s", report)
	}
	if !strings.Contains(report, "undecidable") {
		t.Fatalf("expected RISKS entry, got: %s", report)
	}
	if !strings.Contains(report, "ASSUMED] Q1") {
		t.Fatalf("expected ASSUMED ledger entry, got: %s", report)
	}
}

func TestHasObjectionDetectsMarker(t *testing.T) {
	if hasObjection("Layer-2 decisions stand.") {
		t.Fatal("expected no objection")
	}
	if !hasObjection("Layer-2 decisions stand.\nOBJECTION: sweep should not own retry state.") {
		t.Fatal("expected objection to be detected")
	}
}

func TestRunCloseLintIsAdvisoryNeverBlocks(t *testing.T) {
	// vulnBuggy's package-level `mu` is unused — lint (if installed) fails on
	// it, but pre-existing whole-repo lint noise must never block a close.
	dir, _ := seededDefectRepo(t)
	m := New(Config{})
	out, ok := m.runClose(context.Background(), dir, goBuildTest, goTest)
	if !ok {
		t.Fatalf("close must pass when build/vet/test pass, regardless of lint; record: %s", out)
	}
	if _, err := exec.LookPath("golangci-lint"); err == nil {
		if strings.Contains(out, "## lint (FAILED)") {
			t.Fatalf("a failed lint must be recorded as advisory, got: %s", out)
		}
	}
}

func TestChangedFilesSinceExcludesNulliusAndNonSource(t *testing.T) {
	// Spin-5 measured failure: AUDIT swept the machine's own untracked
	// .nullius/ ledger (state.json, close.md, ...) — 120 nullius-internal
	// AMBIGUOUS entries joined the checklist. The ledger is never terrain,
	// and non-source files have nothing a lens can bite on.
	dir, vulnPath := seededDefectRepo(t)
	if err := os.WriteFile(vulnPath, []byte(vulnFixed), 0o644); err != nil {
		t.Fatalf("modify vuln.go: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".nullius", "mandates", "x"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".nullius", "mandates", "x", "state.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write ledger: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}
	files, err := changedFilesSince(context.Background(), dir)
	if err != nil {
		t.Fatalf("changedFilesSince: %v", err)
	}
	if len(files) != 1 || files[0] != "vuln.go" {
		t.Fatalf("expected exactly [vuln.go], got %v", files)
	}
}

func TestRunAuditForceDisposesUnruledFresh(t *testing.T) {
	// The audit micro-ruling can leave fresh suspects undisposed (weak ruler,
	// parse failure) — they must exit AUDIT as RISK, never ride undisposed
	// into CLOSE/RATIFY (there is no AllDisposed gate after AUDIT).
	fa := &fakeAdapter{responses: map[string]string{
		"audit-hunt/serialization": "V|foo.go|AMBIGUOUS|needs a decisive read\n",
		// no "rule" response → fakeAdapter default {"targets":[]} → no dispositions
	}}
	m := newMachine(fa)
	s := &mandate.State{Budgets: mandate.DefaultBudgets()}
	entries := m.runAudit(context.Background(), s, nil, []string{"foo.go"})
	for _, e := range entries {
		if e.Disposition == mandate.DispUndisposed {
			t.Fatalf("undisposed entry escaped AUDIT: %+v", e)
		}
	}
}
