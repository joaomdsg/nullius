package drive

import (
	"context"
	"fmt"
	"os"
	"strings"

	"go-nullius/internal/mandate"
)

// runExecute drives plans through the craftsman via the drain safety net
// (executeObjective), and --rule-patches diffs through mechanical
// application (applyPatch). No craftsman configured means plans are
// reported, never applied (DESIGN-mandates.md §7).
func (m *Machine) runExecute(ctx context.Context, s *mandate.State, plans []Plan, patches []Patch) []ExecResult {
	var out []ExecResult
	for _, p := range plans {
		if m.cfg.Craftsman == nil {
			out = append(out, ExecResult{Target: p.Target, Status: execFailed, Detail: "no craftsman configured; plan reported, not applied"})
			continue
		}
		out = append(out, executeObjective(ctx, m.cfg.Root, p.Target, planObjective(p), m.cfg.Craftsman, m.cfg.buildCmd(), m.cfg.testCmd()))
	}
	for _, p := range patches {
		out = append(out, applyPatch(ctx, m.cfg.Root, p, m.cfg.buildCmd(), m.cfg.testCmd()))
	}
	return out
}

func planObjective(p Plan) string {
	return fmt.Sprintf("Target: %s\nIntent: %s\nTest: %s — %s\nBlast radius: %s",
		p.Target, p.Intent, p.TestName, p.TestSketch, p.BlastRadius)
}

// applyPatch mechanically applies a --rule-patches unified diff via `git
// apply`, then runs the same build → touched-pkg test → revert gate as the
// craftsman path (DESIGN-mandates.md §7 — the driver applies it, the
// frontier never opens an editing session).
func applyPatch(ctx context.Context, dir string, p Patch, buildCmd, testCmd []string) ExecResult {
	res := ExecResult{Target: p.Target}
	snap, err := gitSnapshot(ctx, dir)
	if err != nil {
		res.Status, res.Detail = execFailed, "snapshot: "+err.Error()
		return res
	}

	f, err := os.CreateTemp("", "nullius-patch-*.diff")
	if err != nil {
		res.Status, res.Detail = execFailed, "tempfile: "+err.Error()
		return res
	}
	defer os.Remove(f.Name())
	if _, werr := f.WriteString(p.Diff); werr != nil {
		f.Close()
		res.Status, res.Detail = execFailed, "write patch: "+werr.Error()
		return res
	}
	f.Close()

	if out, err := runCmd(ctx, dir, []string{"git", "apply", f.Name()}); err != nil {
		res.Status, res.Detail = execFailed, "patch apply failed: "+firstLineOf(out)
		return res
	}

	changed, stat, _ := snap.changed(ctx)
	if !changed {
		res.Status, res.Detail = execFailed, "patch applied but produced an empty diff"
		return res
	}
	res.Diffstat = stat

	if out, err := runCmd(ctx, dir, buildCmd); err != nil {
		_ = snap.revert(ctx)
		res.Status, res.Detail = execFailed, "build failed: "+firstLineOf(out)
		return res
	}
	pkgs := snap.changedPkgs(ctx)
	tcmd := append(append([]string{}, testCmd...), pkgs...)
	if out, err := runCmd(ctx, dir, tcmd); err != nil {
		_ = snap.revert(ctx)
		res.Status, res.Detail = execFailed, "tests failed: "+firstLineOf(out)
		return res
	}
	res.Status, res.Detail = execDone, "patch applied: verified build + "+strings.Join(pkgs, " ")
	return res
}
