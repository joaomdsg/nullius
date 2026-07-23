package leader

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// gitstep gives the drain mechanical evidence and a safety net around each
// craftsman: snapshot the worktree before the step, diff it after (a DONE
// claim with an empty diff is a lie), and revert a failed step so the next
// serial craftsman starts from a clean tree. Tracked state is captured via
// `git stash create` (a dangling commit — the worktree is never touched);
// untracked files are tracked by set difference.
type gitSnap struct {
	dir       string
	commit    string          // worktree tracked state at snapshot time
	untracked map[string]bool // untracked set at snapshot time
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, firstLine(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func gitSnapshot(ctx context.Context, dir string) (*gitSnap, error) {
	// `git stash create` emits a commit only when tracked changes exist;
	// on a clean tree the tracked state IS HEAD.
	commit, err := git(ctx, dir, "stash", "create")
	if err != nil {
		return nil, err
	}
	if commit == "" {
		if commit, err = git(ctx, dir, "rev-parse", "HEAD"); err != nil {
			return nil, err
		}
	}
	unt, err := gitUntracked(ctx, dir)
	if err != nil {
		return nil, err
	}
	return &gitSnap{dir: dir, commit: commit, untracked: unt}, nil
}

func gitUntracked(ctx context.Context, dir string) (map[string]bool, error) {
	out, err := git(ctx, dir, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, f := range strings.Split(out, "\n") {
		if f = strings.TrimSpace(f); f != "" {
			set[f] = true
		}
	}
	return set, nil
}

// changed reports whether the worktree differs from the snapshot, with a
// human-readable stat of what moved (tracked diffstat + new files).
func (s *gitSnap) changed(ctx context.Context) (bool, string, error) {
	stat, err := git(ctx, s.dir, "diff", "--stat", s.commit)
	if err != nil {
		return false, "", err
	}
	now, err := gitUntracked(ctx, s.dir)
	if err != nil {
		return false, "", err
	}
	var fresh []string
	for f := range now {
		if !s.untracked[f] {
			fresh = append(fresh, f)
		}
	}
	if len(fresh) > 0 {
		stat = strings.TrimSpace(stat + "\n new: " + strings.Join(fresh, ", "))
	}
	return stat != "", stat, nil
}

// revert restores the worktree to the snapshot: tracked files back to the
// snapshot commit, new untracked files removed. Prior uncommitted work is
// IN the snapshot commit, so it survives.
func (s *gitSnap) revert(ctx context.Context) error {
	if _, err := git(ctx, s.dir, "checkout", s.commit, "--", "."); err != nil {
		return err
	}
	now, err := gitUntracked(ctx, s.dir)
	if err != nil {
		return err
	}
	for f := range now {
		if !s.untracked[f] {
			if err := os.Remove(filepath.Join(s.dir, f)); err != nil {
				return err
			}
		}
	}
	return nil
}
