package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go-nullius/internal/mandate"
)

func chdirTempRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	run := func(argv ...string) {
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", strings.Join(argv, " "), err, out)
		}
	}
	run("git", "init", "-q")
	run("git", "config", "user.email", "test@example.com")
	run("git", "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module sample\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	run("git", "add", "-A")
	run("git", "commit", "-q", "-m", "init")

	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	// resolve the same way repoRoot() will, so tests read back mandate
	// files at the exact path runInit/runStatus wrote them to even if the
	// temp dir involves a symlink (e.g. /tmp -> /private/tmp).
	resolved, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}
	return resolved
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	return string(bytes.TrimSpace(out))
}

func TestRunInitScaffoldsMandate(t *testing.T) {
	dir := chdirTempRepo(t)
	if err := runInit([]string{"-intent", "fix the race", "retry-fix"}); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	doc, err := mandate.ReadDoc(dir, "retry-fix")
	if err != nil {
		t.Fatalf("ReadDoc: %v", err)
	}
	if !strings.Contains(doc.Intent, "fix the race") {
		t.Fatalf("expected intent written, got %q", doc.Intent)
	}
}

func TestRunInitRejectsWrongArgCount(t *testing.T) {
	chdirTempRepo(t)
	if err := runInit(nil); err == nil {
		t.Fatal("expected error with no slug argument")
	}
	if err := runInit([]string{"slug-a", "slug-b"}); err == nil {
		t.Fatal("expected error with two slug arguments")
	}
}

func TestRunStatusListsScaffoldedMandate(t *testing.T) {
	chdirTempRepo(t)
	if err := runInit([]string{"status-check"}); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	out := captureStdout(t, func() {
		if err := runStatus(nil); err != nil {
			t.Fatalf("runStatus: %v", err)
		}
	})
	if !strings.Contains(out, "status-check") || !strings.Contains(out, "phase=INIT") {
		t.Fatalf("expected status line for scaffolded mandate, got %q", out)
	}
}

func TestRunStatusOnEmptyRepoReportsNothing(t *testing.T) {
	chdirTempRepo(t)
	out := captureStdout(t, func() {
		if err := runStatus(nil); err != nil {
			t.Fatalf("runStatus: %v", err)
		}
	})
	if out != "" {
		t.Fatalf("expected no output with no mandates, got %q", out)
	}
}

// The documented usage puts the slug BEFORE the flags (`nullius init <slug>
// -intent ...`); Go's flag package stops at the first positional, so the
// slug must be lifted out before Parse or trailing flags are silently
// swallowed (observed live: `init spin-1 -intent ...` → usage error, and
// `drive <slug> -adapter=...` would silently ignore the adapter).
func TestRunInitAcceptsSlugBeforeFlags(t *testing.T) {
	dir := chdirTempRepo(t)
	if err := runInit([]string{"slug-first", "-intent", "documented order"}); err != nil {
		t.Fatalf("runInit slug-first: %v", err)
	}
	doc, err := mandate.ReadDoc(dir, "slug-first")
	if err != nil {
		t.Fatalf("ReadDoc: %v", err)
	}
	if !strings.Contains(doc.Intent, "documented order") {
		t.Fatalf("expected trailing -intent honored, got %q", doc.Intent)
	}
}

func TestLiftSlug(t *testing.T) {
	if slug, rest := liftSlug([]string{"spin-1", "-intent", "x"}); slug != "spin-1" || len(rest) != 2 {
		t.Fatalf("slug-first: got %q %+v", slug, rest)
	}
	if slug, rest := liftSlug([]string{"-intent", "x", "spin-1"}); slug != "" || len(rest) != 3 {
		t.Fatalf("flag-first: got %q %+v", slug, rest)
	}
	if slug, rest := liftSlug(nil); slug != "" || len(rest) != 0 {
		t.Fatalf("empty: got %q %+v", slug, rest)
	}
}

func TestRecon2ModeDefaultsToScouts(t *testing.T) {
	if recon2mode("") != "scouts" || recon2mode("bogus") != "scouts" {
		t.Fatal("expected unrecognized recon mode to default to scouts")
	}
	if recon2mode("ast") != "ast" || recon2mode("both") != "both" {
		t.Fatal("expected ast/both to pass through")
	}
}
