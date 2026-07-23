package leader

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/anthropics/anthropic-sdk-go"

	"go-nullius/internal/ledger"
)

// DefaultCloseCommands is the Go-project close suite; override per
// project via the tool input.
var DefaultCloseCommands = []string{
	"gofmt -l .",
	"go vet ./...",
	"go test ./... -race",
}

type CloseTool struct {
	Ledger *ledger.Ledger
	Scout  Dispatcher
	Dir    string // workspace root, for project-type command detection

	closed atomic.Bool // set on a successful close record; consumed by the loop
}

// DetectCloseCommands picks the verification suite from the project's
// manifest files so the leader never burns a turn re-calling close with
// the right commands. Unknown terrain falls back to the Go defaults.
func DetectCloseCommands(dir string) []string {
	has := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	switch {
	case has("go.mod"):
		return DefaultCloseCommands
	case has("Cargo.toml"):
		return []string{"cargo fmt --check", "cargo clippy -- -D warnings", "cargo test"}
	case has("package.json"):
		return []string{"npm test --silent"}
	case has("pyproject.toml"), has("setup.py"):
		return []string{"python -m pytest"}
	}
	return DefaultCloseCommands
}

// ConsumeClosed reports (once) that a close record came back clean since
// the last check — the agent loop's post-close compaction sentinel.
func (c *CloseTool) ConsumeClosed() bool { return c.closed.Swap(false) }

// HasClosed peeks (without consuming) whether a close-out came back clean
// this session — the mandate-completion signal Unfinished keys on.
func (c *CloseTool) HasClosed() bool { return c.closed.Load() }

// Unfinished returns a nudge when ending the turn now would abandon the
// mandate: no close-out has run. It fires in BOTH bail-out gaps — pre-hunt
// (terrain mapped, zero findings) and post-hunt (open checklist items) —
// and falls silent only once a close-out has actually succeeded. The agent
// loop calls it on end_turn and re-prompts (bounded by nudgeCap) instead
// of returning. nil closer (never wired) => always incomplete.
func Unfinished(led *ledger.Ledger, closer *CloseTool) string {
	if closer != nil && closer.HasClosed() {
		return ""
	}
	if n := led.CountOpen(); n > 0 {
		return fmt.Sprintf("%d checklist item(s) remain UNRULED — you may not end here. Rule each open item now (fixed WITH its pinning test / refuted WITH the quoted protecting mechanism / out-of-mandate WITH the excluding mandate text), fix every confirmed defect, then run the close-out scout. Nullius in verba.", n)
	}
	return "The mandate is NOT complete: no close-out has run. Do NOT end here. If the terrain is mapped, proceed to the hunt now — dispatch a lens hunter for each applicable lens, rule every finding, fix confirmed defects with a pinning test — then run the close-out scout (the close tool). Nullius in verba."
}

func (c *CloseTool) Name() string { return "close" }

func (c *CloseTool) Def() anthropic.ToolUnionParam {
	return anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
		Name: "close",
		Description: anthropic.String(
			"Scout-verified close-out. REFUSES while open checklist items remain. Otherwise ONE fresh scout runs the verification suite from clean plus `git diff` and `git status --short` (untracked new test files are part of the record), flags 0-byte source files, and the verbatim record comes back with the STATUS/FACTS/RISKS/UNKNOWN/ASSUMED skeleton. Failures re-enter the loop — never into RISKS. Never self-report green."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"commands": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
					"description": "project verification commands (default: gofmt -l, go vet, go test -race)"},
			},
		},
	}}
}

func (c *CloseTool) Run(ctx context.Context, input json.RawMessage) (string, bool) {
	var in struct {
		Commands []string `json:"commands"`
	}
	_ = json.Unmarshal(input, &in)

	var open []string
	for k, ru := range c.Ledger.Snapshot() {
		if ru.Status == ledger.StatusOpen {
			open = append(open, fmt.Sprintf("[%s] %s %s", k[:8], ru.Finding.File, ru.Finding.Fn))
		}
	}
	if len(open) > 0 {
		return fmt.Sprintf("close REFUSED: %d checklist items unruled — every one ends in rule() first:\n%s",
			len(open), strings.Join(open, "\n")), true
	}

	cmds := in.Commands
	if len(cmds) == 0 {
		if c.Dir != "" {
			cmds = DetectCloseCommands(c.Dir)
		} else {
			cmds = DefaultCloseCommands
		}
	}
	objective := fmt.Sprintf(`Close-out verification run. Execute exactly these, in order, from the current workspace, and record each with its FULL verbatim output and REAL exit code (a build/test that does not run is a FAIL, never a skip):
%s

Then record, verbatim:
- git diff (the full surface diff)
- git status --short (untracked new files are part of the record)
- SURFACE: from the diff, list every removed or signature-changed exported/public symbol, verbatim; "none" only after actually scanning the diff
- any 0-byte or missing-package-declaration source files (a blank file is a broken build, not a stub): find . -name '*.go' -size 0

If a test command finds NO runnable test suite, record that FACT explicitly ("no runnable test suite: <evidence>") — never silently fall back to build+vet alone.

Report: per-command block with exit code, then the git records. No interpretation — the record is the deliverable.`,
		"- "+strings.Join(cmds, "\n- "))

	raw, _ := json.Marshal(map[string]any{"objective": objective})
	report, isErr := c.Scout.Run(ctx, raw)
	if isErr {
		return "close: verification scout failed (fix and dispatch a fresh close): " + report, true
	}

	c.closed.Store(true)
	return report + `

--- CLOSE SKELETON (you rule on the record above; nonzero exits and unexplained surface changes re-enter the loop, never RISKS. Every removed/signature-changed exported symbol in SURFACE must be one you decided by name. If the record says the project has NO runnable test suite, that goes in RISKS by name — an unpinned fix is how regressions ship) ---
STATUS: <one line, never unqualified success>
FACTS: <verbatim-record-backed only>
RISKS: <only what could not be confirmed; a missing test suite is named here, never silently degraded past>
UNKNOWN: <open questions>
ASSUMED: <self-answered decisions, one line each, with reversal cost>`, false
}
