package leader

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
	for k, ru := range c.Ledger.Rulings {
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
		cmds = DefaultCloseCommands
	}
	objective := fmt.Sprintf(`Close-out verification run. Execute exactly these, in order, from the current workspace, and record each with its FULL verbatim output and REAL exit code (a build/test that does not run is a FAIL, never a skip):
%s

Then record, verbatim:
- git diff (the full surface diff)
- git status --short (untracked new files are part of the record)
- any 0-byte or missing-package-declaration source files (a blank file is a broken build, not a stub): find . -name '*.go' -size 0

Report: per-command block with exit code, then the git records. No interpretation — the record is the deliverable.`,
		"- "+strings.Join(cmds, "\n- "))

	raw, _ := json.Marshal(map[string]any{"objective": objective})
	report, isErr := c.Scout.Run(ctx, raw)
	if isErr {
		return "close: verification scout failed (fix and dispatch a fresh close): " + report, true
	}

	return report + `

--- CLOSE SKELETON (you rule on the record above; nonzero exits and unexplained surface changes re-enter the loop, never RISKS) ---
STATUS: <one line, never unqualified success>
FACTS: <verbatim-record-backed only>
RISKS: <only what could not be confirmed>
UNKNOWN: <open questions>
ASSUMED: <self-answered decisions, one line each, with reversal cost>`, false
}
