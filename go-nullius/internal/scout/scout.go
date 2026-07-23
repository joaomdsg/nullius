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
	DefaultIdle    = 5 * time.Minute // local models under load are slow to first token; watch stderr heartbeat, not just stdout
	MaxAttempts    = 3
)

var retryableRe = regexp.MustCompile(`(?i)\b429\b|\b529\b|overloaded|rate.?limit`)

type Tool struct {
	Bin        string // path to the go-nullius binary (os.Executable())
	Dir        string // workspace dir for the child
	Mode       string // child subprocess mode flag: "scout" (default, read-only) or "craftsman" (write+test)
	Model      string // default (fast) tier for bulk dispatches
	SmartModel string // optional smart tier; dispatches may escalate to it
	NulliusDir string // where stats files live (.nullius)
	Stats      *telemetry.Stats
	Idle       time.Duration // 0 → DefaultIdle
	Backoff    time.Duration // 0 → 1s (grows ×4 per attempt)
	Sem        chan struct{} // optional fast-tier concurrency limiter; nil = unbounded
}

// acquire takes a concurrency slot (no-op when unbounded), honoring ctx
// cancellation so a blocked dispatch does not hang a shutting-down run.
func (s *Tool) acquire(ctx context.Context) error {
	if s.Sem == nil {
		return nil
	}
	select {
	case s.Sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// release frees a slot taken by acquire (no-op when unbounded).
func (s *Tool) release() {
	if s.Sem != nil {
		<-s.Sem
	}
}

// modelFor resolves a dispatch tier to a model alias. Bulk scouts default
// to the fast tier; a dispatch may escalate to smart when SmartModel is
// configured. Unknown/empty tier → the default (fast) model, and a smart
// escalation with no smart tier configured degrades to fast — a dispatch
// must never fail to resolve a model.
func (s *Tool) modelFor(tier string) string {
	if tier == "smart" && s.SmartModel != "" {
		return s.SmartModel
	}
	return s.Model
}

func (s *Tool) Name() string { return "scout" }

func (s *Tool) Def() anthropic.ToolUnionParam {
	return anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
		Name: "scout",
		Description: anthropic.String(
			"Dispatch a throwaway read-only scout (fast tier) for ONE narrow objective: a codebase question, a heavy command rerun (builds/tests — report verbatim output + exit codes), a wide search, or the close-out record. The scout sees NONE of this conversation: the objective must carry exact paths, the question, boundaries, and the output format. Report is capped; ask for anchored quotes, not prose. tier defaults to fast; set tier=smart ONLY to escalate a genuinely hard distillation to the smart model."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"objective":  map[string]any{"type": "string", "description": "the complete self-contained dispatch"},
				"timeout_ms": map[string]any{"type": "integer", "description": "overall cap, default 600000"},
				"tier":       map[string]any{"type": "string", "enum": []string{"fast", "smart"}, "description": "dispatch tier; default fast (bulk). smart escalates a hard distillation."},
			},
		},
	}}
}

func (s *Tool) Run(ctx context.Context, input json.RawMessage) (string, bool) {
	var in struct {
		Objective string `json:"objective"`
		TimeoutMs int    `json:"timeout_ms"`
		Tier      string `json:"tier"`
	}
	if err := json.Unmarshal(input, &in); err != nil || in.Objective == "" {
		return "scout: invalid input: need {objective, timeout_ms?, tier?}", true
	}
	timeout := 10 * time.Minute
	if in.TimeoutMs > 0 {
		timeout = time.Duration(in.TimeoutMs) * time.Millisecond
	}
	backoff := s.Backoff
	if backoff == 0 {
		backoff = time.Second
	}
	model := s.modelFor(in.Tier)

	var lastErr error
	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		report, retryable, err := s.runOnce(ctx, in.Objective, model, timeout)
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

func (s *Tool) runOnce(ctx context.Context, objective, model string, timeout time.Duration) (report string, retryable bool, err error) {
	// Bound concurrent fast-tier instances. Acquire against the parent ctx
	// (a full pool while shutting down must abort, not hang); a blocked
	// acquire is retryable so the dispatch is re-tried when a slot frees.
	if err := s.acquire(ctx); err != nil {
		return "", true, err
	}
	defer s.release()

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	mode := s.Mode
	if mode == "" {
		mode = "scout"
	}
	childID := fmt.Sprintf("%s-%d-%d", mode, os.Getpid(), time.Now().UnixNano())
	cmd := exec.CommandContext(cctx, s.Bin,
		"-p", objective, "--model", model, "--"+mode, "--session", childID, "--dir", s.Dir)
	cmd.Dir = s.Dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	// Force the child's per-turn trace heartbeat on regardless of the
	// parent's setting — the watchdog treats that stderr stream as the
	// liveness signal (a hunter is stdout-silent until its final report).
	cmd.Env = append(os.Environ(), "NULLIUS_TRACE=1")

	// The watchdog fires on silence across BOTH pipes: a working scout
	// streams nothing to stdout until its report, but traces every turn to
	// stderr, so stderr activity must reset the timer too (the dispatch
	// failures were legitimate scouts killed for stdout silence under load).
	var lastActivity atomic.Int64
	lastActivity.Store(time.Now().UnixNano())
	poke := func() { lastActivity.Store(time.Now().UnixNano()) }

	var errBuf cappedBuffer
	errBuf.cap = MaxStderrBytes
	cmd.Stderr = &activityWriter{w: &errBuf, poke: poke}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", false, err
	}
	if err := cmd.Start(); err != nil {
		return "", false, err
	}

	// Idle watchdog: no progress on either pipe for Idle → kill the group.
	idle := s.Idle
	if idle == 0 {
		idle = DefaultIdle
	}
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
			poke()
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

// activityWriter forwards writes to an underlying writer and pokes a
// liveness callback on each one, so stderr traffic (the child's per-turn
// trace heartbeat) counts as progress for the idle watchdog. It never
// drops bytes itself — capping is the wrapped writer's job.
type activityWriter struct {
	w    io.Writer
	poke func()
}

func (a *activityWriter) Write(p []byte) (int, error) {
	if len(p) > 0 {
		a.poke()
	}
	return a.w.Write(p)
}

var _ io.Writer = (*activityWriter)(nil)

var _ io.Writer = (*cappedBuffer)(nil)
