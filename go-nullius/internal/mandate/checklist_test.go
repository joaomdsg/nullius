package mandate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteChecklistRewritesWholesale(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checklist.md")
	if err := WriteChecklist(path, []ChecklistEntry{
		{Lens: "serialization", Target: "foo.go:Bar", Verdict: Absent, Location: "foo.go:12", Quote: "no lock", Disposition: DispConfirmed},
	}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := WriteChecklist(path, []ChecklistEntry{
		{Lens: "swallowed-errors", Target: "baz.go:Qux", Verdict: Present},
	}); err != nil {
		t.Fatalf("second write: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(b)
	if strings.Contains(content, "serialization") {
		t.Fatalf("expected rewrite (not append) to drop prior entries: %s", content)
	}
	if !strings.Contains(content, "swallowed-errors") {
		t.Fatalf("expected new entry present: %s", content)
	}
}

func TestAllDisposedGatesOnUndisposed(t *testing.T) {
	entries := []ChecklistEntry{
		{Target: "a", Disposition: DispConfirmed},
		{Target: "b", Disposition: DispUndisposed},
	}
	ok, undisposed := AllDisposed(entries)
	if ok {
		t.Fatal("expected AllDisposed=false with one undisposed suspect")
	}
	if len(undisposed) != 1 || undisposed[0].Target != "b" {
		t.Fatalf("unexpected undisposed set: %+v", undisposed)
	}

	entries[1].Disposition = DispRisk
	ok, undisposed = AllDisposed(entries)
	if !ok || len(undisposed) != 0 {
		t.Fatalf("expected all disposed once RISK assigned: ok=%v undisposed=%+v", ok, undisposed)
	}
}

func TestWriteLedgerRewritesWholesale(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.md")
	if err := WriteLedger(path, []LedgerEntry{{Kind: "ASSUMED", Text: "old"}}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := WriteLedger(path, []LedgerEntry{{CardID: "Q1", Kind: "PROVISIONAL", Text: "sweep owns retry state"}}); err != nil {
		t.Fatalf("second write: %v", err)
	}
	b, _ := os.ReadFile(path)
	content := string(b)
	if strings.Contains(content, "old") {
		t.Fatalf("expected rewrite to drop prior entries: %s", content)
	}
	if !strings.Contains(content, "Q1") || !strings.Contains(content, "PROVISIONAL") {
		t.Fatalf("expected new entry present: %s", content)
	}
}
