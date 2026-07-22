package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"go-nullius/internal/governor"
	"go-nullius/internal/telemetry"
)

const (
	MaxBashOutput  = 48 << 10
	DefaultTimeout = 2 * time.Minute
	MaxTimeout     = 10 * time.Minute
)

// BashTool runs a shell command. The governor gates it BEFORE dispatch;
// bounding (timeout, output cap) happens in the RESULT builder — the
// command itself is never rewritten (the boundCommand lesson inverted).
type BashTool struct {
	Dir     string
	Stats   *telemetry.Stats // optional
	Ungated bool             // scouts run the heavy commands; only the leader is gated
}

func (b *BashTool) Name() string { return "bash" }

func (b *BashTool) Def() anthropic.ToolUnionParam {
	return anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
		Name: "bash",
		Description: anthropic.String(fmt.Sprintf(
			"Run a bash command in the workspace. Output is capped at %dKB with a truncation marker; the real exit code is always reported (a nonzero exit is data, not a tool error). Heavy unbounded commands (builds, test suites, wide searches) are denied with a routing reason — dispatch those to a scout instead.",
			MaxBashOutput>>10)),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"command":    map[string]any{"type": "string"},
				"timeout_ms": map[string]any{"type": "integer", "description": "default 120000, max 600000"},
			},
		},
	}}
}

func (b *BashTool) Run(ctx context.Context, input json.RawMessage) (string, bool) {
	var in struct {
		Command   string `json:"command"`
		TimeoutMs int    `json:"timeout_ms"`
	}
	if err := json.Unmarshal(input, &in); err != nil || in.Command == "" {
		return "bash: invalid input: need {command, timeout_ms?}", true
	}

	if v := governor.Gate(in.Command); !b.Ungated && !v.Allow {
		if b.Stats != nil {
			_ = b.Stats.Update(func(st *telemetry.Stats) {
				if v.Route != "" {
					st.Routes++
				} else {
					st.Denies++
				}
			})
		}
		return "governor: " + v.Reason, true
	}

	timeout := DefaultTimeout
	if in.TimeoutMs > 0 {
		timeout = time.Duration(in.TimeoutMs) * time.Millisecond
		if timeout > MaxTimeout {
			timeout = MaxTimeout
		}
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, "bash", "-c", in.Command)
	cmd.Dir = b.Dir
	// Own process group so a timeout kills the whole tree, not just bash.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()

	if cctx.Err() == context.DeadlineExceeded {
		return fmt.Sprintf("bash: command timed out after %s\n%s", timeout, capped(out.Bytes())), true
	}
	exit := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			return "bash: failed to run: " + err.Error(), true
		}
	}
	return fmt.Sprintf("exit_code: %d\n%s", exit, capped(out.Bytes())), false
}

func capped(raw []byte) string {
	if len(raw) <= MaxBashOutput {
		return string(raw)
	}
	return fmt.Sprintf("%s\n[output truncated: %d of %d bytes shown]", raw[:MaxBashOutput], MaxBashOutput, len(raw))
}
