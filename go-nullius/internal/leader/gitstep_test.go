package leader

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitstep is the drain's mechanical evidence layer: snapshot before a
// craftsman, diff after it, revert on failure. These tests exercise it
// against a real throwaway git repo — the helper shells out to git, so
// faking it would pin nothing.

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("init", "-q")
	run("add", "-A")
	run("commit", "-q", "-m", "base")
	return dir
}

func TestSnapshotDiffDetectsChange(t *testing.T) {
	dir := gitRepo(t)
	snap, err := gitSnapshot(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	changed, _, err := snap.changed(context.Background())
	if err != nil || changed {
		t.Fatalf("clean tree reported changed=%v err=%v", changed, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, stat, err := snap.changed(context.Background())
	if err != nil || !changed {
		t.Fatalf("edit not detected: changed=%v err=%v", changed, err)
	}
	if !strings.Contains(stat, "a.txt") {
		t.Errorf("diffstat missing a.txt: %q", stat)
	}
}

func TestSnapshotDetectsNewUntrackedFile(t *testing.T) {
	dir := gitRepo(t)
	snap, err := gitSnapshot(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new_test.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, stat, err := snap.changed(context.Background())
	if err != nil || !changed {
		t.Fatalf("new untracked file not detected: changed=%v err=%v", changed, err)
	}
	if !strings.Contains(stat, "new_test.go") {
		t.Errorf("diffstat missing new file: %q", stat)
	}
}

// A failed craftsman's partial edits must be revertible so the next
// serial step starts from a clean tree — including edits layered on top
// of PRIOR uncommitted work, which must survive the revert.
func TestRevertRestoresSnapshotStateOnly(t *testing.T) {
	dir := gitRepo(t)
	// Pre-existing uncommitted work (a prior step's successful edit).
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("prior-step\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := gitSnapshot(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	// Failed step: edits tracked file AND adds a new one.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "junk.go"), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := snap.revert(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(got) != "prior-step\n" {
		t.Errorf("revert lost prior uncommitted work: got %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "junk.go")); !os.IsNotExist(err) {
		t.Error("revert left the failed step's new untracked file behind")
	}
}

// A craftsman that DELETES a tracked file mid-failure must get it back on
// revert — `git checkout <snap> -- .` restores every path in the snapshot
// tree, deletions included. Pinned because losing a source file silently
// is the worst dirty-tree outcome.
func TestRevertRestoresDeletedTrackedFile(t *testing.T) {
	dir := gitRepo(t)
	snap, err := gitSnapshot(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatal(err)
	}
	if err := snap.revert(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "a.txt"))
	if err != nil || string(got) != "one\n" {
		t.Errorf("deleted tracked file not restored: %v %q", err, got)
	}
}

func TestSnapshotOnNonRepoErrors(t *testing.T) {
	if _, err := gitSnapshot(context.Background(), t.TempDir()); err == nil {
		t.Error("snapshot of a non-git dir must error so drain can degrade gracefully")
	}
}
