package leader

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"go-nullius/internal/ledger"
)

// DrainTool is the mechanical implementation phase. Once the leader has
// planned its fixes (PlanTool), one call to drain runs a fast craftsman
// over every pending step — each in its own throwaway context — collects
// each craftsman's verbatim RESULT, marks the step, and hands the leader
// ONE summary to audit. The leader never absorbs the implementation bulk;
// it plans, drains, and audits.
//
// Steps drain SEQUENTIALLY: craftsmen share the one checkout, so running
// two that touch the same file concurrently would be a lost-update — the
// very defect nullius hunts. Per-step context isolation is preserved
// regardless (each step gets a fresh craftsman); only the writes serialize.
//
// The drain does not take a craftsman's word for it (nullius in verba,
// tier-boundary edition). With Dir set to a git worktree, each step is
// bracketed mechanically: snapshot before, diff after — a DONE claim with
// an empty diff fails (unless already-satisfied), a DONE with a diff gets
// the affected package's tests run for real, a failed step's partial edits
// are reverted, and one retry (failure fed back to a fresh craftsman)
// happens before anything reaches the leader.
type DrainTool struct {
	Ledger    *ledger.Ledger
	Craftsman subTool // fast write+test subprocess (scout.Tool{Mode:"craftsman"})
	Dir       string  // workspace root; "" or non-git degrades to trust-the-RESULT
	// TestCmd runs the mechanical verification for a step (default: go
	// test on the target's package). Overridable for tests.
	TestCmd func(ctx context.Context, dir, pkgRel string) (string, error)
}

func (d *DrainTool) Name() string { return "drain" }

func (d *DrainTool) Def() anthropic.ToolUnionParam {
	return anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
		Name: "drain",
		Description: anthropic.String(
			"Run the fast craftsman over EVERY pending plan step (see plan), each in a throwaway context that writes the fix + its pinning test and runs the affected package's tests. Each DONE claim is verified MECHANICALLY (git diff must be non-empty, the affected package's tests are re-run for real); failed steps are reverted and retried once before surfacing. Returns ONE summary with per-step diffstat evidence for you to AUDIT. Takes no arguments — it drains whatever you have planned. Steps run sequentially (shared checkout). After a drain, audit the summary, hunt for holes/regressions, plan any follow-ups, and drain again until an audit adds nothing."),
		InputSchema: anthropic.ToolInputSchemaParam{Properties: map[string]any{}},
	}}
}

func (d *DrainTool) Run(ctx context.Context, _ json.RawMessage) (string, bool) {
	pending := d.Ledger.PendingSteps()
	if len(pending) == 0 {
		return "drain: no pending steps — plan the fixes first", true
	}

	var done, failed int
	var sb strings.Builder
	for _, s := range pending {
		status, result, evidence := d.drainStep(ctx, s)
		if status == ledger.StepDone {
			done++
		} else {
			failed++
		}
		if err := d.Ledger.MarkStep(s.ID, status, result); err != nil {
			result += " [LEDGER MARK FAILED: " + err.Error() + "]"
		}
		fmt.Fprintf(&sb, "[%s] %s %s — %s\n", s.ID[:8], status, s.Target, firstLine(result))
		if evidence != "" {
			fmt.Fprintf(&sb, "    evidence: %s\n", strings.ReplaceAll(evidence, "\n", "\n    "))
		}
	}
	if err := d.Ledger.Save(); err != nil {
		return "drain: ledger save failed: " + err.Error(), true
	}

	head := fmt.Sprintf("drain summary — %d step(s): %d done, %d failed\n", len(pending), done, failed)
	tail := "\nAUDIT: the diffstat/test evidence above is mechanical; audit the CONTENT of the diffs, hunt for holes/regressions/security gaps, and plan follow-ups (then drain again). Failed steps are yours to re-plan or fix directly."
	return head + strings.TrimRight(sb.String(), "\n") + tail, false
}

// drainStep runs one step through craftsman → mechanical verification,
// with revert + one retry on failure. evidence is the diffstat (and test
// tail) that backs the disposition.
func (d *DrainTool) drainStep(ctx context.Context, s ledger.Step) (status, result, evidence string) {
	var snap *gitSnap
	if d.Dir != "" {
		if sn, err := gitSnapshot(ctx, d.Dir); err == nil {
			snap = sn
		} else {
			evidence = "git unavailable (" + firstLine(err.Error()) + ") — mechanical verification skipped"
		}
	}

	prevFailure := ""
	for attempt := 0; attempt < 2; attempt++ {
		report, isErr := d.Craftsman.Run(ctx, craftObjective(s, prevFailure))
		status, result = classifyCraft(report, isErr)

		if snap != nil {
			var stat string
			status, result, stat = d.verify(ctx, s, snap, status, result)
			if stat != "" {
				evidence = stat
			}
		}
		if status == ledger.StepDone {
			return status, result, evidence
		}
		if snap != nil {
			if err := snap.revert(ctx); err != nil {
				result += " [REVERT FAILED — workspace may be dirty: " + firstLine(err.Error()) + "]"
				return status, result, evidence
			}
		}
		prevFailure = result
	}
	return status, result, evidence
}

// verify applies the mechanical checks to a craftsman disposition: an
// empty-diff DONE (not already-satisfied) is a lie; a DONE with a diff
// must survive a real test run.
func (d *DrainTool) verify(ctx context.Context, s ledger.Step, snap *gitSnap, status, result string) (string, string, string) {
	changed, stat, err := snap.changed(ctx)
	if err != nil {
		return status, result + " [diff check failed: " + firstLine(err.Error()) + "]", ""
	}
	if status != ledger.StepDone {
		return status, result, stat
	}
	if strings.Contains(result, "already-satisfied") {
		return status, result, stat
	}
	if !changed {
		return ledger.StepFailed, "claimed DONE but wrote nothing (empty diff): " + firstLine(result), stat
	}
	testCmd := d.TestCmd
	if testCmd == nil {
		testCmd = goTestStep
	}
	if out, err := testCmd(ctx, d.Dir, stepPkg(s.Target)); err != nil {
		return ledger.StepFailed,
			"claimed DONE but mechanical test run is RED: " + firstLine(err.Error()),
			stat + "\ntest tail: " + lastLine(strings.TrimSpace(out))
	}
	return ledger.StepDone, result + " [verified: diff present, tests green]", stat
}

// stepPkg derives the package path a step's target lives in ("a/b/c.go:Fn"
// → "a/b"); "" when underivable.
func stepPkg(target string) string {
	file := target
	if i := strings.IndexByte(file, ':'); i >= 0 {
		file = file[:i]
	}
	if !strings.HasSuffix(file, ".go") {
		return ""
	}
	dir := filepath.Dir(file)
	if dir == "." || dir == "/" {
		return "."
	}
	return dir
}

// goTestStep is the default mechanical verification: build the WHOLE
// module, then run the affected package's tests with -race. The build is
// module-wide on purpose — a step whose edit breaks a package it does not
// test in (an import cycle, a renamed symbol) must fail ITS OWN drain
// step, not surface later at close. Skips (green) when the workspace is
// not a Go module — the close-out still covers it.
func goTestStep(ctx context.Context, dir, pkgRel string) (string, error) {
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return "no go.mod — mechanical verification skipped (close-out covers it)", nil
	}
	// Bounded: a hung build/test must never stall the whole drain — the
	// step fails with the timeout as evidence and the leader rules on it.
	ctx, cancel := context.WithTimeout(ctx, stepTestTimeout)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "./...")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return string(out), fmt.Errorf("module-wide build timed out after %s", stepTestTimeout)
		}
		return string(out), fmt.Errorf("module-wide build RED (go build ./...): %v", err)
	}
	if pkgRel == "" {
		return "module builds; no .go target — package test skipped (close-out covers it)", nil
	}
	cmd := exec.CommandContext(ctx, "go", "test", "./"+filepath.ToSlash(pkgRel)+"/...", "-race")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("mechanical test run timed out after %s", stepTestTimeout)
	}
	return string(out), err
}

// stepTestTimeout bounds one step's mechanical verification run.
var stepTestTimeout = 3 * time.Minute

// craftObjective builds the self-contained brief a craftsman receives. It
// carries the leader's intent (the judgment) but no conversation context.
// prevFailure, when set, is the prior attempt's failure fed back so the
// retry does not repeat it blind.
func craftObjective(s ledger.Step, prevFailure string) json.RawMessage {
	obj := fmt.Sprintf(`Implement EXACTLY this one change, then prove it. Nothing else.

TARGET: %s
INTENT: %s
PINNING TEST: %s

Rules:
- Write the pinning test FIRST, then the minimal fix that makes it pass.
- Run ONLY the affected package's tests (go test ./<pkg>/ -race) and paste the VERBATIM output with the REAL exit code.
- Minimal diff — do not touch unrelated code.
- End your report with exactly one line: "RESULT: DONE <one-line what+proof>" or "RESULT: FAILED <why>".`,
		s.Target, s.Intent, orDefault(s.Test, "the changed behavior; name what flips it red"))
	if prevFailure != "" {
		obj += "\n\nPRIOR ATTEMPT FAILED (workspace was reverted — start fresh, do not repeat this): " + prevFailure
	}
	raw, _ := json.Marshal(map[string]any{"objective": obj})
	return raw
}

// classifyCraft turns a craftsman report into a step disposition. A fix is
// DONE only on an explicit proven RESULT: DONE line — a dispatch error or a
// missing/failed result is FAILED, never silently done (tests are
// testimony: no proof, no pass).
func classifyCraft(report string, isErr bool) (status, result string) {
	if isErr {
		return ledger.StepFailed, "craftsman dispatch failed: " + firstLine(report)
	}
	for _, line := range strings.Split(report, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "RESULT: DONE") {
			return ledger.StepDone, line
		}
		if strings.HasPrefix(line, "RESULT: FAILED") {
			return ledger.StepFailed, line
		}
	}
	return ledger.StepFailed, "no RESULT line — fix unproven; raw tail: " + lastLine(report)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func lastLine(s string) string {
	s = strings.TrimRight(s, "\n")
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}
