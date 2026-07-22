package rule

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "fixture.go")
	var b strings.Builder
	for i := 1; i <= 100; i++ {
		if i == 50 {
			b.WriteString("\tmu.Lock() // serializes the entrypoint body\n")
			continue
		}
		b.WriteString("// filler line\n")
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestVerify(t *testing.T) {
	p := writeFixture(t)

	if err := Verify(p+":50", "mu.Lock() // serializes the entrypoint body"); err != nil {
		t.Errorf("valid evidence rejected: %v", err)
	}
	if err := Verify(p+":50", "short quote"); err == nil {
		t.Error("sub-minimum evidence accepted")
	}
	if err := Verify(p+":50", "this mechanism does not exist anywhere here"); err == nil {
		t.Error("fabricated evidence accepted")
	}
	if err := Verify(p+":5", "mu.Lock() // serializes the entrypoint body"); err == nil {
		t.Error("evidence far from claimed anchor accepted")
	}
	if err := Verify(filepath.Join(t.TempDir(), "missing.go"), "mu.Lock() // serializes the entrypoint body"); err == nil {
		t.Error("missing file accepted")
	}
	// Whitespace drift must not defeat an honest quote.
	if err := Verify(p+":50", "mu.Lock()   //  serializes the entrypoint body"); err != nil {
		t.Errorf("whitespace-normalized match failed: %v", err)
	}
}
