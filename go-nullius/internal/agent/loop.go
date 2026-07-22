// Package agent runs the manual tool-use loop over /v1/messages.
// The system prompt (LEADER_RULES) is frozen with a cache_control
// breakpoint; volatile context follows it. Parallel tool_use blocks run
// concurrently and ALL results return in ONE user message.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/anthropics/anthropic-sdk-go"

	"go-nullius/internal/telemetry"
)

// StopContextWindowExceeded is not an SDK constant; the loop matches the
// wire string (see BUILD-STATE.md).
const StopContextWindowExceeded = "model_context_window_exceeded"

// Tool is one leader tool. Run returns the tool_result content and
// whether it is an error result. Run must be safe for concurrent use —
// parallel tool_use blocks execute concurrently.
type Tool interface {
	Name() string
	Def() anthropic.ToolUnionParam
	Run(ctx context.Context, input json.RawMessage) (content string, isError bool)
}

// Streamer is the transport seam (api.Client in production).
type Streamer interface {
	Stream(ctx context.Context, params anthropic.MessageNewParams, onEvent func(anthropic.MessageStreamEventUnion)) (*anthropic.Message, error)
}

type Config struct {
	Model     anthropic.Model
	MaxTokens int64  // per-response cap
	System    string // LEADER_RULES — frozen, cached
	MaxTurns  int    // API round-trips per Run; 0 → 50
}

// Editor evicts already-ruled tool results from history before a call
// (governor.Editor in production).
type Editor interface {
	Sweep(msgs []anthropic.MessageParam) int
}

// TailPrefix marks the recited-ledger block so stale renderings can be
// stripped — the ledger lives only at the context edge.
const TailPrefix = "≡NULLIUS-LEDGER≡\n"

// CompactPrefix heads the post-close compact record: after a successful
// close-out, the next Run starts from empty history carrying only the
// close ledger verbatim — post-close is the one point compaction is
// near-lossless, because the ledger IS the summary.
const CompactPrefix = "≡NULLIUS-COMPACT≡\n"

// Closer reports, consumably, that a close-out completed since the last
// check (leader.CloseTool in production).
type Closer interface {
	ConsumeClosed() bool
}

type Loop struct {
	cfg     Config
	s       Streamer
	tools   map[string]Tool
	defs    []anthropic.ToolUnionParam
	msgs    []anthropic.MessageParam
	stats   *telemetry.Stats                        // optional
	OnEvent func(anthropic.MessageStreamEventUnion) // optional live rendering hook
	Editor  Editor                                  // optional context editor
	Tail    func() string                           // optional ledger tail, recited at the context edge
	Closer  Closer                                  // optional close sentinel: arms post-close compaction

	pendingCompact string // close ledger awaiting compaction at the next Run
}

func New(cfg Config, s Streamer, tools []Tool, stats *telemetry.Stats) *Loop {
	if cfg.MaxTurns == 0 {
		cfg.MaxTurns = 50
	}
	l := &Loop{cfg: cfg, s: s, tools: map[string]Tool{}, stats: stats}
	for _, t := range tools {
		l.tools[t.Name()] = t
		l.defs = append(l.defs, t.Def())
	}
	return l
}

// Messages exposes the transcript (read-only use: context accounting,
// stage-6 eviction).
func (l *Loop) Messages() []anthropic.MessageParam { return l.msgs }

// Run feeds one user prompt through the loop until a terminal stop.
func (l *Loop) Run(ctx context.Context, prompt string) (string, error) {
	if l.pendingCompact != "" {
		prompt = l.compact(prompt)
	}
	l.msgs = append(l.msgs, anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)))

	for turn := 0; turn < l.cfg.MaxTurns; turn++ {
		l.editContext()
		params := anthropic.MessageNewParams{
			Model:     l.cfg.Model,
			MaxTokens: l.cfg.MaxTokens,
			System: []anthropic.TextBlockParam{{
				Text:         l.cfg.System,
				CacheControl: anthropic.NewCacheControlEphemeralParam(),
			}},
			Messages: l.msgs,
			Tools:    l.defs,
		}

		msg, err := l.s.Stream(ctx, params, l.OnEvent)
		if err != nil {
			l.drainClose()
			return "", err
		}
		l.record(msg)

		if string(msg.StopReason) == StopContextWindowExceeded {
			l.drainClose()
			return textOf(msg), fmt.Errorf("context window exceeded: evict or start a fresh session")
		}
		switch msg.StopReason {
		case anthropic.StopReasonRefusal:
			l.drainClose()
			return "", errors.New("model refused (stop_reason=refusal); rephrase or start a fresh session")
		case anthropic.StopReasonMaxTokens:
			l.drainClose()
			return textOf(msg), fmt.Errorf("response truncated at max_tokens=%d", l.cfg.MaxTokens)
		case anthropic.StopReasonPauseTurn:
			// Long server-side turn paused; resend with the partial
			// assistant turn appended to continue it.
			l.msgs = append(l.msgs, msg.ToParam())
			continue
		case anthropic.StopReasonToolUse:
			l.msgs = append(l.msgs, msg.ToParam())
			l.msgs = append(l.msgs, anthropic.NewUserMessage(l.runTools(ctx, msg)...))
			continue
		default: // end_turn, stop_sequence
			// Record the final assistant turn: without it, later Runs in
			// the same session never see the model's own prior answers.
			l.msgs = append(l.msgs, msg.ToParam())
			out := textOf(msg)
			// A clean end after a successful close arms compaction: the
			// final report carries the close ledger — it survives, the
			// rest of the transcript does not.
			if l.Closer != nil && l.Closer.ConsumeClosed() {
				l.pendingCompact = out
			}
			return out, nil
		}
	}
	l.drainClose()
	return "", fmt.Errorf("turn cap reached (%d API round-trips) without a terminal stop", l.cfg.MaxTurns)
}

// drainClose discards an armed close sentinel on error exits: the Run's
// final output is not a close ledger, so compacting on it would replace
// the transcript with garbage.
func (l *Loop) drainClose() {
	if l.Closer != nil {
		l.Closer.ConsumeClosed()
	}
}

// compact drops the whole transcript, resets editor residency (the
// evicted content is gone — dup-reads must hit disk again), and folds the
// surviving close ledger into the new mandate's prompt.
func (l *Loop) compact(prompt string) string {
	l.msgs = nil
	if r, ok := l.Editor.(interface{ Reset() }); ok {
		r.Reset()
	}
	record := l.pendingCompact
	l.pendingCompact = ""
	if l.stats != nil {
		_ = l.stats.Update(func(st *telemetry.Stats) { st.Compactions++ })
	}
	return CompactPrefix +
		"Prior mandate CLOSED. The transcript before this point was compacted away; " +
		"the close ledger below is the only surviving record (verbatim). " +
		"Nothing else is resident — re-read or re-scout anything you need.\n\n" +
		record + "\n\n=== NEW MANDATE ===\n" + prompt
}

// runTools executes every tool_use block concurrently, preserving block
// order in the results. A missing tool becomes an is_error result — the
// model sees it and corrects; the loop never dies on a bad tool name.
func (l *Loop) runTools(ctx context.Context, msg *anthropic.Message) []anthropic.ContentBlockParamUnion {
	type call struct {
		id, name string
		input    json.RawMessage
	}
	var calls []call
	for _, b := range msg.Content {
		if tu, ok := b.AsAny().(anthropic.ToolUseBlock); ok {
			calls = append(calls, call{tu.ID, tu.Name, json.RawMessage(tu.JSON.Input.Raw())})
		}
	}
	results := make([]anthropic.ContentBlockParamUnion, len(calls))
	var wg sync.WaitGroup
	for i, cl := range calls {
		wg.Add(1)
		go func(i int, cl call) {
			defer wg.Done()
			t, ok := l.tools[cl.name]
			if !ok {
				results[i] = anthropic.NewToolResultBlock(cl.id, "unknown tool: "+cl.name, true)
				return
			}
			content, isErr := t.Run(ctx, cl.input)
			results[i] = anthropic.NewToolResultBlock(cl.id, content, isErr)
		}(i, cl)
	}
	wg.Wait()
	return results
}

// editContext runs the pre-call surgery: evict ruled tool results, strip
// stale ledger renderings, recite the current ledger at the tail.
func (l *Loop) editContext() {
	if l.Editor != nil {
		if n := l.Editor.Sweep(l.msgs); n > 0 && l.stats != nil {
			_ = l.stats.Update(func(st *telemetry.Stats) { st.Evictions += n })
		}
	}
	if l.Tail == nil {
		return
	}
	// Strip every prior rendering, wherever it sits.
	for i := range l.msgs {
		kept := l.msgs[i].Content[:0]
		for _, b := range l.msgs[i].Content {
			if b.OfText != nil && strings.HasPrefix(b.OfText.Text, TailPrefix) {
				continue
			}
			kept = append(kept, b)
		}
		l.msgs[i].Content = kept
	}
	render := l.Tail()
	if render == "" || len(l.msgs) == 0 {
		return
	}
	// Recite at the edge: append to the final user message (tool results
	// ride user messages, so the last message before a call is user).
	last := &l.msgs[len(l.msgs)-1]
	if last.Role != anthropic.MessageParamRoleUser {
		return
	}
	last.Content = append(last.Content, anthropic.ContentBlockParamUnion{
		OfText: &anthropic.TextBlockParam{Text: TailPrefix + render},
	})
}

func (l *Loop) record(msg *anthropic.Message) {
	if l.stats == nil {
		return
	}
	_ = l.stats.Update(func(st *telemetry.Stats) {
		st.Turns++
		st.Leader.Requests++
		st.Leader.InputTokens += msg.Usage.InputTokens
		st.Leader.OutputTokens += msg.Usage.OutputTokens
		st.Leader.CacheReadTokens += msg.Usage.CacheReadInputTokens
		st.Leader.CacheCreationTokens += msg.Usage.CacheCreationInputTokens
	})
}

func textOf(msg *anthropic.Message) string {
	var sb strings.Builder
	for _, b := range msg.Content {
		if tb, ok := b.AsAny().(anthropic.TextBlock); ok {
			sb.WriteString(tb.Text)
		}
	}
	return sb.String()
}
