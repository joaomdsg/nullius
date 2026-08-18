package drive

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// runClose is the CLOSE phase: a verbatim record of build+vet+test (+lint
// if available) over the clean tree, never a self-reported "it compiles"
// (DESIGN-mandates.md §4 CLOSE fail-closed default: "no runnable suite →
// named in RISKS, never silent degrade").
// The bool result is the verdict: false on ANY failed step, and the drive
// loop refuses to ratify over it — RATIFY writing an unqualified DONE report
// on a failed suite is the doctrine violation this pins shut.
func (m *Machine) runClose(ctx context.Context, root string, buildCmd, testCmd []string) (string, bool) {
	var b strings.Builder
	b.WriteString("# close — verbatim verification record\n\n")

	pass := true
	record := func(label string, argv []string, blocking bool) bool {
		out, err := runCmd(ctx, root, argv)
		status := "OK"
		if err != nil {
			status = "FAILED"
			if blocking {
				pass = false
			} else {
				status = "FAILED (advisory, non-blocking)"
			}
		}
		fmt.Fprintf(&b, "## %s (%s): `%s`\n\n```\n%s\n```\n\n", label, status, strings.Join(argv, " "), strings.TrimSpace(out))
		return err == nil
	}

	record("build", buildCmd, true)
	record("vet", []string{"go", "vet", "./..."}, true)
	record("test", testCmd, true)

	// Lint sweeps the WHOLE repo — on an inherited codebase its pre-existing
	// noise is beyond the mandate, so it is recorded, never a ratify blocker.
	if _, err := exec.LookPath("golangci-lint"); err == nil {
		record("lint", []string{"golangci-lint", "run"}, false)
	} else {
		b.WriteString("## lint: SKIPPED — no `golangci-lint` on PATH (named here, not silently degraded)\n\n")
	}
	return b.String(), pass
}
