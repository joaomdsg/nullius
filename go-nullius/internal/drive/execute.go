package drive

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Writer performs ONE code change described by an objective, inside dir, and
// returns its output. Structurally identical to machine.Writer — any
// go-nullius Writer (e.g. machine.SubprocessCraftsman) satisfies this too.
type Writer interface {
	Write(ctx context.Context, dir, objective string) (string, error)
}

// ExecResult is one EXECUTE-phase change's outcome.
type ExecResult struct {
	Target   string
	Status   string // "DONE" | "FAILED"
	Detail   string
	Diffstat string
	Attempts int
}

const (
	execDone   = "DONE"
	execFailed = "FAILED"
)

const execStepTimeout = 10 * time.Minute

// executeObjective is the write safety net (DESIGN-mandates.md §4 EXECUTE
// fail-closed default: "drain safety net per change"), ported from
// go-nullius/internal/machine/drain.go's drainOne: snapshot → write →
// non-empty-diff gate → build → touched-pkg test → revert + retry(1).
//
// internal/machine's drainOne is unexported and coupled to its own
// FixPlan/Confirmation types, so this re-implements the same algorithm
// against the mandate CLI's own objective-string/Writer shape rather than
// exporting/refactoring internal/machine (ASSUMED: safer than touching a
// package with no local build/test verification available in this
// environment).
func executeObjective(ctx context.Context, dir, target, objective string, w Writer, buildCmd, testCmd []string) ExecResult {
	res := ExecResult{Target: target}
	feedback := ""
	for attempt := 1; attempt <= 2; attempt++ {
		res.Attempts = attempt
		snap, err := gitSnapshot(ctx, dir)
		if err != nil {
			res.Status, res.Detail = execFailed, "snapshot: "+err.Error()
			return res
		}

		stepCtx, cancel := context.WithTimeout(ctx, execStepTimeout)
		_, werr := w.Write(stepCtx, dir, withFeedback(objective, feedback))
		cancel()
		if werr != nil {
			feedback = "the previous attempt errored: " + firstLineOf(werr.Error())
			_ = snap.revert(ctx)
			res.Detail = feedback
			continue
		}

		changed, stat, _ := snap.changed(ctx)
		if !changed {
			feedback = "your previous attempt wrote NOTHING; make the actual edit"
			if res.Detail == "" {
				res.Detail = "empty diff (wrote nothing)"
			}
			continue // nothing to revert
		}
		res.Diffstat = stat

		if out, err := runCmd(ctx, dir, buildCmd); err != nil {
			feedback = "the build failed: " + firstLineOf(out)
			_ = snap.revert(ctx)
			res.Detail = "build failed: " + firstLineOf(out)
			continue
		}
		pkgs := snap.changedPkgs(ctx)
		tcmd := append(append([]string{}, testCmd...), pkgs...)
		if out, err := runCmd(ctx, dir, tcmd); err != nil {
			feedback = "the tests failed: " + firstLineOf(out)
			_ = snap.revert(ctx)
			res.Detail = "tests failed: " + firstLineOf(out)
			continue
		}

		res.Status = execDone
		res.Detail = "verified: build + " + strings.Join(pkgs, " ")
		return res
	}
	res.Status = execFailed
	return res
}

func withFeedback(base, feedback string) string {
	if feedback == "" {
		return base
	}
	return base + "\n\nNote: " + feedback
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func runCmd(ctx context.Context, dir string, argv []string) (string, error) {
	if len(argv) == 0 {
		return "", fmt.Errorf("drive: empty command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// gitSnap is a point-in-time git checkpoint used to gate and revert one
// EXECUTE attempt.
type gitSnap struct {
	dir       string
	commit    string
	untracked map[string]bool
}

func gitSnapshot(ctx context.Context, dir string) (*gitSnap, error) {
	out, err := runCmd(ctx, dir, []string{"git", "rev-parse", "HEAD"})
	if err != nil {
		return nil, fmt.Errorf("git rev-parse: %w: %s", err, out)
	}
	untracked, err := gitUntracked(ctx, dir)
	if err != nil {
		return nil, err
	}
	return &gitSnap{dir: dir, commit: strings.TrimSpace(out), untracked: untracked}, nil
}

func gitUntracked(ctx context.Context, dir string) (map[string]bool, error) {
	out, err := runCmd(ctx, dir, []string{"git", "ls-files", "--others", "--exclude-standard"})
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w: %s", err, out)
	}
	set := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			set[line] = true
		}
	}
	return set, nil
}

// changed reports whether anything changed since the snapshot: tracked diff
// stat, or any newly appeared untracked file.
func (s *gitSnap) changed(ctx context.Context) (bool, string, error) {
	stat, err := runCmd(ctx, s.dir, []string{"git", "diff", "--stat"})
	if err != nil {
		return false, "", fmt.Errorf("git diff --stat: %w", err)
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
	if strings.TrimSpace(stat) == "" && len(fresh) == 0 {
		return false, "", nil
	}
	if len(fresh) > 0 {
		stat = strings.TrimSpace(stat) + "\n+ new: " + strings.Join(fresh, ", ")
	}
	return true, strings.TrimSpace(stat), nil
}

// changedPkgs maps touched .go files to their "./dir/..." package patterns
// for a targeted test run; empty (non-Go-only change) falls back to "./...".
func (s *gitSnap) changedPkgs(ctx context.Context) []string {
	out, err := runCmd(ctx, s.dir, []string{"git", "diff", "--name-only"})
	pkgs := map[string]bool{}
	if err == nil {
		for _, f := range strings.Split(out, "\n") {
			f = strings.TrimSpace(f)
			if strings.HasSuffix(f, ".go") {
				d := f
				if i := strings.LastIndexByte(f, '/'); i >= 0 {
					d = f[:i]
				} else {
					d = "."
				}
				pkgs["./"+d+"/..."] = true
			}
		}
	}
	if len(pkgs) == 0 {
		return []string{"./..."}
	}
	out2 := make([]string, 0, len(pkgs))
	for p := range pkgs {
		out2 = append(out2, p)
	}
	return out2
}

// revert discards tracked changes back to the snapshot commit and removes
// any untracked file that appeared since.
func (s *gitSnap) revert(ctx context.Context) error {
	if _, err := runCmd(ctx, s.dir, []string{"git", "checkout", s.commit, "--", "."}); err != nil {
		return err
	}
	now, err := gitUntracked(ctx, s.dir)
	if err != nil {
		return err
	}
	for f := range now {
		if !s.untracked[f] {
			_, _ = runCmd(ctx, s.dir, []string{"rm", "-f", s.dir + "/" + f})
		}
	}
	return nil
}
