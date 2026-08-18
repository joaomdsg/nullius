package dispatch

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// ClaudeAdapter shells `claude -p <prompt>` for one-shot haiku scouts, sonnet
// craftsman rulings, or frontier dispatches (DESIGN-mandates.md §6).
type ClaudeAdapter struct {
	// Bin overrides the resolved binary path; defaults to "claude" on PATH.
	Bin string
}

func (a ClaudeAdapter) Name() string { return "claude" }

func (a ClaudeAdapter) Dispatch(ctx context.Context, req Request) (Response, error) {
	bin := a.Bin
	if bin == "" {
		bin = "claude"
	}
	start := time.Now()
	cmd := exec.CommandContext(ctx, bin, "-p", req.Prompt)
	out, err := cmd.CombinedOutput()
	ms := time.Since(start).Milliseconds()
	if err != nil {
		return Response{Text: string(out), Ms: ms}, fmt.Errorf("claude adapter: %w: %s", err, firstLine(string(out)))
	}
	return Response{Text: string(out), Ms: ms}, nil
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}
