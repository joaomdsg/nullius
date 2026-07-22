// go-nullius: a starved-orchestrator coding agent over the Claude
// Messages API. Leader pays only for judgment; scouts (nested haiku
// subprocesses) absorb bulk. See DESIGN.md.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"go-nullius/internal/agent"
	"go-nullius/internal/api"
	"go-nullius/internal/auth"
	"go-nullius/internal/governor"
	"go-nullius/internal/leader"
	"go-nullius/internal/ledger"
	"go-nullius/internal/scout"
	"go-nullius/internal/telemetry"
	"go-nullius/internal/tools"
)

const leaderRules = `You are go-nullius, a starved-orchestrator coding agent. Nullius in verba — take nobody's word for it — and your context is the bill, twice: every absorbed token is re-paid every later turn, and residency dilutes the attention your judgment runs on.

Division of labor:
- YOU are the judgment tier. You read decisive lines (bounded ranges), rule on findings, and write fixes yourself, each with the test that pins the changed behavior.
- SCOUTS absorb bulk. ALL builds, test suites, wide searches, file distillation, and the close-out verification run in scout dispatches. A scout sees none of this conversation: every dispatch carries the objective, exact paths, boundaries, and the output format (anchored path:line quotes, verbatim command output with REAL exit codes).

Rules:
1. The bash tool denies heavy unbounded commands with a routing reason — obey it, dispatch a scout. Never fight a denial.
2. Read files in ranges; whole reads are capped. Absorb only decisive lines.
3. Evidence discipline: no unanchored claim drives an action. Quotes carry path:line anchors. Tests are testimony, not verdicts — for each test, name the change that flips it red.
4. Batch every independent action into one turn (parallel tool calls). Turns are the other bill.
5. Close-out: the final verification (full suite + vet + linters + surface diff) runs in a FRESH scout dispatch and its verbatim record is the truth. Never self-report "compiling" or "tests pass".
6. Report STATUS / FACTS / RISKS / UNKNOWN / ASSUMED. Never unqualified success.`

const scoutRules = `You are a read-only scout: a throwaway context that answers ONE narrow dispatch, then ceases to exist. You absorb bulk so the orchestrator never has to.

Rules:
1. Answer ONLY the dispatched objective. No scope creep, no advice beyond it.
2. Evidence: quoted mechanisms with path:line anchors; command output verbatim with the REAL exit code. Never summarize a build/test result — record it.
3. You cannot edit files. You run commands (builds, tests, searches) and read code.
4. Cap your report: findings only, no narration, no file dumps beyond decisive quoted lines. Declare gaps explicitly ("not found in <scope searched>") rather than padding.
5. Your final message IS the report the orchestrator absorbs — make every line load-bearing.`

var modelAlias = map[string]anthropic.Model{
	"haiku":  "claude-haiku-4-5",
	"sonnet": "claude-sonnet-5",
	"opus":   "claude-opus-4-8",
	"fable":  "claude-fable-5",
}

func main() {
	var (
		prompt    = flag.String("p", "", "headless: run one prompt, print the final report to stdout, exit")
		modelF    = flag.String("model", envOr("NULLIUS_MODEL", "opus"), "model alias (haiku|sonnet|opus|fable) or full model id")
		scoutMode = flag.Bool("scout", false, "scout mode: read-only tool set, scout rules")
		dirF      = flag.String("dir", ".", "workspace directory")
		sessionF  = flag.String("session", "", "session id for the stats file (default: derived)")
		maxTurns  = flag.Int("max-turns", 50, "API round-trip cap per prompt")
	)
	flag.Parse()

	if err := run(*prompt, *modelF, *scoutMode, *dirF, *sessionF, *maxTurns); err != nil {
		fmt.Fprintln(os.Stderr, "go-nullius:", err)
		os.Exit(1)
	}
}

func run(prompt, modelF string, scoutMode bool, dirF, session string, maxTurns int) error {
	dir, err := filepath.Abs(dirF)
	if err != nil {
		return err
	}
	if session == "" {
		session = fmt.Sprintf("%d-%d", time.Now().Unix(), os.Getpid())
	}
	nulliusDir := filepath.Join(dir, ".nullius")
	stats := telemetry.New(nulliusDir, session)

	client, err := api.New(&auth.Resolver{})
	if err != nil {
		return err
	}
	model, ok := modelAlias[modelF]
	if !ok {
		model = anthropic.Model(modelF)
	}

	tracker := tools.NewTracker()
	var (
		toolset []agent.Tool
		system  string
		maxTok  int64
		editor  *governor.Editor
		tail    func() string
	)
	if scoutMode {
		system = scoutRules
		maxTok = 8192
		toolset = []agent.Tool{
			&tools.ReadTool{Tr: tracker},
			&tools.BashTool{Dir: dir, Stats: stats, Ungated: true},
		}
	} else {
		system = leaderRules
		maxTok = 16384
		bin, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve own binary for scout spawning: %w", err)
		}
		led, err := ledger.Load(filepath.Join(nulliusDir, "hunt.json"))
		if err != nil {
			return err
		}
		editor = governor.NewEditor()
		tail = leader.TailRender(led)
		sct := &scout.Tool{Bin: bin, Dir: dir, Model: "haiku", NulliusDir: nulliusDir, Stats: stats}
		toolset = []agent.Tool{
			&tools.ReadTool{Tr: tracker, Ed: editor},
			&tools.EditTool{Tr: tracker},
			&tools.BashTool{Dir: dir, Stats: stats},
			sct,
			&leader.HuntTool{Ledger: led, Scout: sct},
			&leader.RuleTool{Ledger: led, Ed: editor},
			&leader.CloseTool{Ledger: led, Scout: sct},
		}
	}

	loop := agent.New(agent.Config{
		Model: model, MaxTokens: maxTok, System: system, MaxTurns: maxTurns,
	}, client, toolset, stats)
	if editor != nil {
		loop.Editor = editor
	}
	loop.Tail = tail

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if prompt != "" {
		out, err := loop.Run(ctx, prompt)
		if err != nil {
			return err
		}
		fmt.Println(out)
		return nil
	}

	// Interactive REPL.
	fmt.Fprintf(os.Stderr, "go-nullius %s session=%s dir=%s (exit/quit to leave)\n", model, session, dir)
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for {
		fmt.Fprint(os.Stderr, "nullius> ")
		if !sc.Scan() {
			return sc.Err()
		}
		line := sc.Text()
		if line == "exit" || line == "quit" {
			return nil
		}
		if line == "" {
			continue
		}
		out, err := loop.Run(ctx, line)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			continue
		}
		fmt.Println(out)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
