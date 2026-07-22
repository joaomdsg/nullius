// Package scout spawns nested go-nullius subprocesses (haiku, read-only
// tool set) so bulk absorption happens in throwaway contexts. Hardening
// ported from pi: idle-kill, byte cap with truncation marker, exit code
// as verdict, retry on rate-limit/overload classes. The child's token
// usage is folded into the parent's scouts tier from the child's stats
// file — the stats file is truth, not the model's self-report.
package scout

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"go-nullius/internal/telemetry"
)

const (
	MaxReportBytes = 32 << 10
	MaxStderrBytes = 4 << 10
	DefaultIdle    = 90 * time.Second
	MaxAttempts    = 3
)

var retryableRe = regexp.MustCompile(`(?i)\b429\b|\b529\b|overloaded|rate.?limit`)

type Tool struct {
	Bin        string // path to the go-nullius binary (os.Executable())
	Dir        string // workspace dir for the child
	Model      string // e.g. "haiku"
	NulliusDir string // where stats files live (.nullius)
	Stats      *telemetry.Stats
	Idle       time.Duration // 0 → DefaultIdle
	Backoff    time.Duration // 0 → 1s (grows ×4 per attempt)
}

func (s *Tool) Name() string { return "scout" }

func (s *Tool) Def() anthropic.ToolUnionParam {
	return anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
		Name: "scout",
		Description: anthropic.String(
			"Dispatch a throwaway read-only scout (haiku) for ONE narrow objective: a codebase question, a heavy command rerun (builds/tests — report verbatim output + exit codes), a wide search, or the close-out record. The scout sees NONE of this conversation: the objective must carry exact paths, the question, boundaries, and the output format. Report is capped; ask for anchored quotes, not prose."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"objective":  map[string]any{"type": "string", "description": "the complete self-contained dispatch"},
				"timeout_ms": map[string]any{"type": "integer", "description": "overall cap, default 600000"},
			},
		},
	}}
}

func (s *Tool) Run(ctx context.Context, input json.RawMessage) (string, bool) {
	var in struct {
		Objective string `json:"objective"`
		TimeoutMs int    `json:"timeout_ms"`
	}
	if err := json.Unmarshal(input, &in); err != nil || in.Objective == "" {
		return "scout: invalid input: need {objective, timeout_ms?}", true
	}
	timeout := 10 * time.Minute
	if in.TimeoutMs > 0 {
		timeout = time.Duration(in.TimeoutMs) * time.Millisecond
	}
	backoff := s.Backoff
	if backoff == 0 {
		backoff = time.Second
	}

	var lastErr error
	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		report, retryable, err := s.runOnce(ctx, in.Objective, timeout)
		if err == nil {
			return report, false
		}
		lastErr = err
		if !retryable {
			return "scout: " + err.Error(), true
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return "scout: " + ctx.Err().Error(), true
		}
		backoff *= 4
	}
	return fmt.Sprintf("scout: %d attempts exhausted: %v", MaxAttempts, lastErr), true
}

func (s *Tool) runOnce(ctx context.Context, objective string, timeout time.Duration) (report string, retryable bool, err error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	childID := fmt.Sprintf("scout-%d-%d", os.Getpid(), time.Now().UnixNano())
	cmd := exec.CommandContext(cctx, s.Bin,
		"-p", objective, "--model", s.Model, "--scout", "--session", childID, "--dir", s.Dir)
	cmd.Dir = s.Dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }

	var errBuf cappedBuffer
	errBuf.cap = MaxStderrBytes
	cmd.Stderr = &errBuf

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", false, err
	}
	if err := cmd.Start(); err != nil {
		return "", false, err
	}

	// Idle watchdog: no stdout progress for Idle → kill the group.
	idle := s.Idle
	if idle == 0 {
		idle = DefaultIdle
	}
	var lastActivity atomic.Int64
	lastActivity.Store(time.Now().UnixNano())
	var idleKilled atomic.Bool
	watchdogDone := make(chan struct{})
	go func() {
		t := time.NewTicker(idle / 4)
		defer t.Stop()
		for {
			select {
			case <-watchdogDone:
				return
			case <-t.C:
				if time.Since(time.Unix(0, lastActivity.Load())) > idle {
					idleKilled.Store(true)
					_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
					return
				}
			}
		}
	}()

	var out bytes.Buffer
	truncated := false
	chunk := make([]byte, 4096)
	for {
		n, rerr := stdout.Read(chunk)
		if n > 0 {
			lastActivity.Store(time.Now().UnixNano())
			if out.Len() < MaxReportBytes {
				room := MaxReportBytes - out.Len()
				if n > room {
					out.Write(chunk[:room])
					truncated = true
				} else {
					out.Write(chunk[:n])
				}
			} else {
				truncated = true
			}
		}
		if rerr != nil {
			break // io.EOF or pipe closed on kill
		}
	}
	waitErr := cmd.Wait()
	close(watchdogDone)

	s.foldChildStats(childID)

	switch {
	case idleKilled.Load():
		return "", true, fmt.Errorf("idle-killed after %s of silence", idle)
	case cctx.Err() == context.DeadlineExceeded:
		return "", false, fmt.Errorf("timed out after %s", timeout)
	case waitErr != nil:
		combined := out.String() + errBuf.String()
		return "", retryableRe.MatchString(combined),
			fmt.Errorf("child exited nonzero: %v; stderr: %s", waitErr, errBuf.String())
	}
	if truncated {
		return out.String() + fmt.Sprintf("\n[scout report truncated at %d bytes]", MaxReportBytes), false, nil
	}
	return out.String(), false, nil
}

// foldChildStats adds the child's leader-tier usage (the scout's own
// calls) into the parent's scouts tier. Missing/corrupt stats files are
// tolerated — telemetry never kills the run.
func (s *Tool) foldChildStats(childID string) {
	if s.Stats == nil {
		return
	}
	var child struct {
		Leader telemetry.TierUsage `json:"leader"`
	}
	raw, err := os.ReadFile(filepath.Join(s.NulliusDir, "stats-"+childID+".json"))
	if err == nil {
		_ = json.Unmarshal(raw, &child)
	}
	_ = s.Stats.Update(func(st *telemetry.Stats) {
		st.ScoutRuns++
		st.Scouts.Requests += child.Leader.Requests
		st.Scouts.InputTokens += child.Leader.InputTokens
		st.Scouts.OutputTokens += child.Leader.OutputTokens
		st.Scouts.CacheReadTokens += child.Leader.CacheReadTokens
		st.Scouts.CacheCreationTokens += child.Leader.CacheCreationTokens
	})
}

// cappedBuffer keeps the first cap bytes and drops the rest.
type cappedBuffer struct {
	buf bytes.Buffer
	cap int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if room := c.cap - c.buf.Len(); room > 0 {
		if len(p) > room {
			c.buf.Write(p[:room])
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil
}

func (c *cappedBuffer) String() string { return c.buf.String() }

var _ io.Writer = (*cappedBuffer)(nil)
