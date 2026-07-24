package machine

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go-nullius/internal/caller"
	"go-nullius/internal/enumerate"
)

func osWrite(p, s string) error                   { return os.WriteFile(p, []byte(s), 0o644) }
func osRead(p string) (string, error)             { b, err := os.ReadFile(p); return string(b), err }
func execCommand(n string, a ...string) *exec.Cmd { return exec.Command(n, a...) }
func candidateAt(file string, line int, fn string) enumerate.Candidate {
	return enumerate.Candidate{File: file, Line: line, Fn: fn}
}

var noLog logf = func(Phase, string, ...any) {}

// fakeCaller answers each phase from a canned JSON payload, selected by a marker in the
// prompt. custom (if set and it returns ok) overrides — used to drive per-candidate Judge
// and Refute replies. An empty payload simulates ErrExhausted (the fallback path).
type fakeCaller struct {
	orient, gate, recon string
	custom              func(prompt string) (string, bool)
	// customTier sees the tier too — used to drive per-tier replies (e.g. pair-discrimination
	// fast vs smart escalation). Checked before custom; ok=false falls through.
	customTier func(tier caller.Tier, prompt string) (string, bool)
}

func (f fakeCaller) Ask(ctx context.Context, tier caller.Tier, prompt string, grammar caller.GBNF, out any, opts ...caller.AskOption) error {
	var payload string
	if f.customTier != nil {
		if p, ok := f.customTier(tier, prompt); ok {
			payload = p
		}
	}
	if payload == "" && f.custom != nil {
		if p, ok := f.custom(prompt); ok {
			payload = p
		}
	}
	if payload == "" {
		switch {
		case strings.Contains(prompt, "ORIENT phase"):
			payload = f.orient
		case strings.Contains(prompt, "GATE phase"):
			payload = f.gate
		case strings.Contains(prompt, "RECON phase"):
			payload = f.recon
		}
	}
	if payload == "" {
		return caller.ErrExhausted
	}
	dec := json.NewDecoder(strings.NewReader(payload))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}

// fixFake returns a fake wired for FIX mode (gate=FIX, recon flags helper() calls) with a
// custom Judge/Refute responder.
func fixFake(custom func(string) (string, bool)) fakeCaller {
	return fakeCaller{
		orient: `{"intent_summary":"x","focus_pkgs":[],"risk_note":"y"}`,
		gate:   `{"mode":"FIX","has_inscope_code":true,"justification":"code"}`,
		recon: `{"lenses":[{"id":"calls-helper","template":"call-to","params":{"fn_regex":"helper"},` +
			`"free_scm":"","mechanism":"call","anchor":"call",` +
			`"positive":"package p\nfunc a(){ helper() }","negative":"package p\nfunc a(){ other() }"}]}`,
		custom: custom,
	}
}

func countConfirmed(res *Result) int { return len(res.Confirmed()) }

func TestJudgeConfirmsDefectClearsCorrect(t *testing.T) {
	// The two unreachable statements (stmt-after-return) are judged DEFECT and confirmed;
	// the reachable helper() call (calls-helper) is judged CORRECT and dropped.
	f := writeGo(t, "a.go", brownfieldSrc)
	fc := fixFake(func(p string) (string, bool) {
		switch {
		case strings.Contains(p, "JUDGE phase"):
			if strings.Contains(p, "stmt-after-return") {
				return `{"answer":"DEFECT","decisive_line":6,"because":"unreachable after return"}`, true
			}
			return `{"answer":"CORRECT","decisive_line":4,"because":"reachable call"}`, true
		case strings.Contains(p, "REFUTE phase"):
			return `{"stands":true,"refuting_line":null}`, true
		}
		return "", false
	})
	res := run(t, fc, []string{f}, "review f")

	if got := countConfirmed(res); got != 2 {
		t.Fatalf("confirmed=%d, want 2 unreachable stmts: %+v", got, res.Judged)
	}
	for _, c := range res.Judged {
		if c.Candidate.Lens == "calls-helper" && c.Confirmed {
			t.Errorf("reachable call wrongly confirmed")
		}
	}
}

func TestRefuterDropsDefect(t *testing.T) {
	f := writeGo(t, "a.go", brownfieldSrc)
	fc := fixFake(func(p string) (string, bool) {
		switch {
		case strings.Contains(p, "JUDGE phase"):
			return `{"answer":"DEFECT","decisive_line":6,"because":"looks unreachable"}`, true
		case strings.Contains(p, "REFUTE phase"):
			return `{"stands":false,"refuting_line":5}`, true
		}
		return "", false
	})
	res := run(t, fc, []string{f}, "review f")
	if got := countConfirmed(res); got != 0 {
		t.Fatalf("refuted defects must not confirm, got %d", got)
	}
	var sawRefuted bool
	for _, c := range res.Judged {
		if c.Refuted {
			sawRefuted = true
		}
	}
	if !sawRefuted {
		t.Error("expected at least one Refuted disposition")
	}
}

func TestUnsupportedRefutationDefectStands(t *testing.T) {
	// The refuter says stands=false but cites no valid distinct line (null, and the
	// flagged line itself). A bare refutation must NOT clear a judge-affirmed defect.
	for _, tc := range []struct {
		name, refute string
	}{
		{"null-line", `{"stands":false,"refuting_line":null}`},
		{"self-line", `{"stands":false,"refuting_line":6}`}, // == decisive line
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := writeGo(t, "a.go", brownfieldSrc)
			fc := fixFake(func(p string) (string, bool) {
				switch {
				case strings.Contains(p, "JUDGE phase"):
					if strings.Contains(p, "stmt-after-return") {
						return `{"answer":"DEFECT","decisive_line":6,"because":"unreachable"}`, true
					}
					return `{"answer":"CORRECT","decisive_line":4,"because":"reachable"}`, true
				case strings.Contains(p, "REFUTE phase"):
					return tc.refute, true
				}
				return "", false
			})
			res := run(t, fc, []string{f}, "review f")
			if got := countConfirmed(res); got != 2 {
				t.Fatalf("unsupported refutation must leave the 2 defects standing, confirmed=%d", got)
			}
		})
	}
}

func TestDecisiveLineOutOfEvidenceBecomesCantTell(t *testing.T) {
	f := writeGo(t, "a.go", brownfieldSrc)
	fc := fixFake(func(p string) (string, bool) {
		if strings.Contains(p, "JUDGE phase") {
			return `{"answer":"DEFECT","decisive_line":9999,"because":"cites a line it was not shown"}`, true
		}
		if strings.Contains(p, "REFUTE phase") {
			t.Error("refuter must NOT run when the decisive line is invalid")
			return `{"stands":true,"refuting_line":null}`, true
		}
		return "", false
	})
	res := run(t, fc, []string{f}, "review f")
	if got := countConfirmed(res); got != 0 {
		t.Fatalf("out-of-evidence line must not confirm, got %d", got)
	}
	for _, c := range res.Judged {
		if strings.ToUpper(c.Judge.Answer) == "DEFECT" {
			t.Errorf("invalid-line DEFECT should have been downgraded, got %+v", c.Judge)
		}
	}
}

func TestJudgeFallbackWhenExhausted(t *testing.T) {
	// custom returns not-ok for JUDGE → Ask ErrExhausts → CANT_TELL, nothing confirmed.
	f := writeGo(t, "a.go", brownfieldSrc)
	fc := fixFake(func(p string) (string, bool) { return "", false })
	res := run(t, fc, []string{f}, "review f")
	if got := countConfirmed(res); got != 0 {
		t.Fatalf("judge fallback must confirm nothing, got %d", got)
	}
	if len(res.Judged) == 0 {
		t.Fatal("expected judged dispositions even on fallback")
	}
	for _, c := range res.Judged {
		if strings.ToUpper(c.Judge.Answer) != "CANT_TELL" {
			t.Errorf("want CANT_TELL on fallback, got %q", c.Judge.Answer)
		}
	}
}

func writeGo(t *testing.T, name, src string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// A file with functions (in-scope code) and two statements after a return.
const brownfieldSrc = `package p

func f() {
	helper()
	return
	x := 1
	_ = x
}

func helper() {}
`

func run(t *testing.T, fc fakeCaller, files []string, task string) *Result {
	t.Helper()
	m := New(fc)
	res, err := m.Run(context.Background(), Mandate{Task: task, Files: files})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

func TestFixFlowBaselinePlusDerived(t *testing.T) {
	f := writeGo(t, "a.go", brownfieldSrc)
	fc := fakeCaller{
		orient: `{"intent_summary":"audit f","focus_pkgs":["p"],"risk_note":"dead code / bad calls"}`,
		gate:   `{"mode":"FIX","has_inscope_code":true,"justification":"pre-existing funcs"}`,
		recon: `{"lenses":[{"id":"calls-helper","template":"call-to","params":{"fn_regex":"helper"},` +
			`"free_scm":"","mechanism":"call","anchor":"call",` +
			`"positive":"package p\nfunc a(){ helper() }","negative":"package p\nfunc a(){ other() }"}]}`,
	}
	res := run(t, fc, []string{f}, "review f for defects")

	if res.Mode != ModeFix {
		t.Fatalf("mode=%s, want FIX", res.Mode)
	}
	// baseline stmt-after-return flags the 2 unreachable stmts.
	var unreachable, calls int
	for _, c := range res.Candidates {
		switch c.Lens {
		case "stmt-after-return":
			unreachable++
		case "calls-helper":
			calls++
		}
	}
	if unreachable != 2 {
		t.Errorf("baseline unreachable candidates=%d, want 2", unreachable)
	}
	if calls != 1 {
		t.Errorf("derived calls-helper candidates=%d, want 1", calls)
	}
	// the derived lens must have passed the witness gate.
	var accepted bool
	for _, s := range res.LensStatuses {
		if s.ID == "calls-helper" && s.Accepted {
			accepted = true
		}
	}
	if !accepted {
		t.Errorf("derived lens not accepted: %+v", res.LensStatuses)
	}
}

func TestGateFailClosedToFixWhenInscopeCode(t *testing.T) {
	// Model wrongly says ANSWER / no in-scope code, but the terrain has functions.
	// The mechanical backstop must override to FIX — misclassifying a brownfield fix
	// as ANSWER would skip the whole hunt.
	f := writeGo(t, "a.go", brownfieldSrc)
	fc := fakeCaller{
		orient: `{"intent_summary":"x","focus_pkgs":[],"risk_note":"y"}`,
		gate:   `{"mode":"ANSWER","has_inscope_code":false,"justification":"looks like a question"}`,
		recon:  `{"lenses":[]}`,
	}
	res := run(t, fc, []string{f}, "what does f do?")
	if res.Mode != ModeFix {
		t.Fatalf("mode=%s, want FIX (mechanical in-scope-code backstop)", res.Mode)
	}
}

func TestAnswerModeWhenNoInscopeCode(t *testing.T) {
	// No functions/methods in the terrain and the model says ANSWER → ANSWER, and
	// Enumerate is skipped (no candidates).
	f := writeGo(t, "a.go", "package p\n\nvar X = 1\n")
	fc := fakeCaller{
		orient: `{"intent_summary":"q","focus_pkgs":[],"risk_note":"none"}`,
		gate:   `{"mode":"ANSWER","has_inscope_code":false,"justification":"pure question"}`,
	}
	res := run(t, fc, []string{f}, "what is X?")
	if res.Mode != ModeAnswer {
		t.Fatalf("mode=%s, want ANSWER", res.Mode)
	}
	if len(res.Candidates) != 0 {
		t.Fatalf("ANSWER must skip Enumerate, got %d candidates", len(res.Candidates))
	}
}

func TestReconFallbackDegradesToBaselineOnly(t *testing.T) {
	// Recon exhausts (empty payload). The coverage floor holds: baseline still runs.
	f := writeGo(t, "a.go", brownfieldSrc)
	fc := fakeCaller{
		orient: `{"intent_summary":"x","focus_pkgs":[],"risk_note":"y"}`,
		gate:   `{"mode":"FIX","has_inscope_code":true,"justification":"code present"}`,
		// recon empty → ErrExhausted
	}
	res := run(t, fc, []string{f}, "review f")
	if res.Mode != ModeFix {
		t.Fatalf("mode=%s, want FIX", res.Mode)
	}
	if len(res.LensStatuses) != 0 {
		t.Errorf("no derived lenses expected, got %+v", res.LensStatuses)
	}
	var unreachable int
	for _, c := range res.Candidates {
		if c.Lens == "stmt-after-return" {
			unreachable++
		}
	}
	if unreachable != 2 {
		t.Errorf("baseline must survive Recon failure, unreachable=%d want 2", unreachable)
	}
}

func TestBadDerivedLensRejectedByWitness(t *testing.T) {
	// A derived lens whose positive witness does NOT match is dropped (DERIVE_FAILED),
	// and the baseline is untouched.
	f := writeGo(t, "a.go", brownfieldSrc)
	fc := fakeCaller{
		orient: `{"intent_summary":"x","focus_pkgs":[],"risk_note":"y"}`,
		gate:   `{"mode":"FIX","has_inscope_code":true,"justification":"code"}`,
		recon: `{"lenses":[{"id":"bad","template":"call-to","params":{"fn_regex":"helper"},` +
			`"free_scm":"","mechanism":"call","anchor":"call",` +
			`"positive":"package p\nfunc a(){ nomatch() }","negative":"package p\nfunc a(){ nomatch() }"}]}`,
	}
	res := run(t, fc, []string{f}, "review f")
	var rejected bool
	for _, s := range res.LensStatuses {
		if s.ID == "bad" && !s.Accepted {
			rejected = true
		}
	}
	if !rejected {
		t.Fatalf("witness-failing lens must be DERIVE_FAILED: %+v", res.LensStatuses)
	}
}

func TestPlanProducesFixPlan(t *testing.T) {
	f := writeGo(t, "a.go", brownfieldSrc)
	fc := fixFake(func(p string) (string, bool) {
		switch {
		case strings.Contains(p, "JUDGE phase"):
			if strings.Contains(p, "stmt-after-return") {
				return `{"answer":"DEFECT","decisive_line":6,"because":"unreachable"}`, true
			}
			return `{"answer":"CORRECT","decisive_line":4,"because":"reachable"}`, true
		case strings.Contains(p, "REFUTE phase"):
			return `{"stands":true,"refuting_line":null}`, true
		case strings.Contains(p, "PLAN phase"):
			return `{"target":"a.go","intent":"remove dead code after return","test_name":"TestNoDeadCode","test_sketch":"call f, assert result","blast_radius":"process only"}`, true
		}
		return "", false
	})
	res := run(t, fc, []string{f}, "review f")
	// brownfieldSrc's two unreachable stmts are the same lens in the same function → they
	// dedupe to ONE plan target (one craftsman fixes the function).
	if len(res.Plans) != 1 {
		t.Fatalf("plans=%d, want 1 (deduped), from %d confirmed", len(res.Plans), len(res.Confirmed()))
	}
	for _, p := range res.Plans {
		if p.Fallback {
			t.Errorf("plan unexpectedly fell back: %+v", p)
		}
		if p.Plan.Intent == "" || p.Plan.TestName == "" {
			t.Errorf("plan missing fields: %+v", p.Plan)
		}
	}
}

func TestPlanFallbackWhenExhausted(t *testing.T) {
	f := writeGo(t, "a.go", brownfieldSrc)
	fc := fixFake(func(p string) (string, bool) {
		switch {
		case strings.Contains(p, "JUDGE phase"):
			return `{"answer":"DEFECT","decisive_line":6,"because":"unreachable"}`, true
		case strings.Contains(p, "REFUTE phase"):
			return `{"stands":true,"refuting_line":null}`, true
		}
		return "", false // PLAN phase → ErrExhausted → fallback
	})
	res := run(t, fc, []string{f}, "review f")
	if len(res.Plans) == 0 {
		t.Fatal("expected fallback plans for confirmed defects")
	}
	for _, p := range res.Plans {
		if !p.Fallback {
			t.Errorf("plan should be a fallback when Plan exhausts: %+v", p)
		}
		if p.Plan.Intent == "" {
			t.Errorf("even a fallback plan must carry an intent: %+v", p.Plan)
		}
	}
}

// --- Drain safety-net tests (real git temp dir + fake writers) ---

func gitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	gitT(t, dir, "config", "user.email", "t@t")
	gitT(t, dir, "config", "user.name", "t")
	for name, body := range files {
		if err := osWrite(filepath.Join(dir, name), body); err != nil {
			t.Fatal(err)
		}
	}
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := execCommand("git", args...)
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

const drainModFiles = "module sample\n\ngo 1.26\n"

const drainDirtySrc = `package sample

func f() int {
	x := 1
	return x
	y := 2
	_ = y
}
`

const drainCleanSrc = `package sample

func f() int {
	x := 1
	return x
}
`

const drainBrokenSrc = `package sample

func f() int {
	this is not valid go
}
`

// fakeWriter runs a scripted mutation instead of spawning a craftsman.
type fakeWriter struct{ fn func(dir string) error }

func (w fakeWriter) Write(ctx context.Context, dir, objective string) (string, error) {
	return "RESULT: DONE", w.fn(dir)
}

func drainPlan(dir string) FixPlan {
	return FixPlan{Confirmation: Confirmation{Candidate: candidateAt(filepath.Join(dir, "sample.go"), 6, "f")}}
}

func TestDrainOneWritesAndVerifies(t *testing.T) {
	dir := gitRepo(t, map[string]string{"go.mod": drainModFiles, "sample.go": drainDirtySrc})
	m := &Machine{Craftsman: fakeWriter{fn: func(dir string) error {
		return osWrite(filepath.Join(dir, "sample.go"), drainCleanSrc)
	}}}
	dr := m.drainOne(context.Background(), dir, drainPlan(dir), noLog)
	if dr.Status != DrainDone {
		t.Fatalf("status=%s detail=%q, want DONE", dr.Status, dr.Detail)
	}
	if dr.Diffstat == "" {
		t.Error("DONE must carry a non-empty diffstat")
	}
}

func TestDrainOneEmptyDiffFails(t *testing.T) {
	dir := gitRepo(t, map[string]string{"go.mod": drainModFiles, "sample.go": drainDirtySrc})
	m := &Machine{Craftsman: fakeWriter{fn: func(dir string) error { return nil }}} // writes nothing
	dr := m.drainOne(context.Background(), dir, drainPlan(dir), noLog)
	if dr.Status != DrainFailed {
		t.Fatalf("empty diff must FAIL, got %s", dr.Status)
	}
}

func TestDrainOneRevertsOnBuildFailure(t *testing.T) {
	dir := gitRepo(t, map[string]string{"go.mod": drainModFiles, "sample.go": drainDirtySrc})
	m := &Machine{Craftsman: fakeWriter{fn: func(dir string) error {
		return osWrite(filepath.Join(dir, "sample.go"), drainBrokenSrc) // breaks the build every time
	}}}
	dr := m.drainOne(context.Background(), dir, drainPlan(dir), noLog)
	if dr.Status != DrainFailed {
		t.Fatalf("build-breaking write must FAIL after retry, got %s", dr.Status)
	}
	if dr.Attempts != 2 {
		t.Errorf("expected 2 attempts (write + 1 retry), got %d", dr.Attempts)
	}
	// the failed write must be reverted — the tree is left clean.
	got, err := osRead(filepath.Join(dir, "sample.go"))
	if err != nil {
		t.Fatal(err)
	}
	if got != drainDirtySrc {
		t.Fatalf("failed drain not reverted:\n%s", got)
	}
}

func TestDrainThroughRunAndAudit(t *testing.T) {
	// Full pipeline over a git dir: confirm the unreachable code, plan, drain (writer removes
	// it), and audit re-hunts the now-clean file → the terrain is gone.
	dir := gitRepo(t, map[string]string{"go.mod": drainModFiles, "sample.go": drainDirtySrc})
	file := filepath.Join(dir, "sample.go")
	fc := fixFake(func(p string) (string, bool) {
		switch {
		case strings.Contains(p, "JUDGE phase"):
			return `{"answer":"DEFECT","decisive_line":6,"because":"unreachable after return"}`, true
		case strings.Contains(p, "REFUTE phase"):
			return `{"stands":true,"refuting_line":null}`, true
		case strings.Contains(p, "PLAN phase"):
			return `{"target":"sample.go","intent":"delete code after return","test_name":"TestF","test_sketch":"assert f()==1","blast_radius":"f"}`, true
		}
		return "", false
	})
	m := New(fc)
	m.Craftsman = fakeWriter{fn: func(dir string) error {
		return osWrite(filepath.Join(dir, "sample.go"), drainCleanSrc)
	}}
	res, err := m.Run(context.Background(), Mandate{Task: "review", Files: []string{file}, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Drained) == 0 || countDone(res.Drained) < 1 {
		t.Fatalf("expected at least one DONE drain, got %+v", res.Drained)
	}
	got, _ := osRead(file)
	if got != drainCleanSrc {
		t.Fatalf("drain did not persist the fix:\n%s", got)
	}
	// audit re-hunt: the fixed file has no unreachable code left.
	var auditRan bool
	for _, tr := range res.Trace {
		if tr.Phase == PhaseAudit && strings.Contains(tr.Msg, "0 candidate(s) remain") {
			auditRan = true
		}
	}
	if !auditRan {
		t.Errorf("audit should report 0 residual candidates after the fix; trace: %v", res.Trace)
	}
}

const lockPairSrc = `package store

import "sync"

type Store struct {
	mu sync.Mutex
	m  map[string]int
}

func (s *Store) Get(k string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[k]
}

func (s *Store) Set(k string, v int) {
	s.mu.Lock()
	s.m[k] = v
}
`

func TestEnclosingWindowScopesToOneFunction(t *testing.T) {
	// The window for Set's Lock() must NOT include Get's `defer Unlock()` — that is the
	// cross-function line the refuter used to falsely clear the real defect.
	f := writeGo(t, "store.go", lockPairSrc)
	lines, err := readLines(f)
	if err != nil {
		t.Fatal(err)
	}
	var lock2, deferLine, seen int
	for i, ln := range lines {
		if strings.Contains(ln, "s.mu.Lock()") {
			if seen++; seen == 2 {
				lock2 = i + 1
			}
		}
		if strings.Contains(ln, "defer s.mu.Unlock()") {
			deferLine = i + 1
		}
	}
	_, start, end := enclosingWindow(f, lines, lock2)
	if deferLine >= start && deferLine <= end {
		t.Fatalf("Set window [%d,%d] wrongly includes Get's defer unlock at %d", start, end, deferLine)
	}
	if lock2 < start || lock2 > end {
		t.Fatalf("window [%d,%d] missing the candidate line %d", start, end, lock2)
	}
}

func TestDedupeConfirmed(t *testing.T) {
	// Two same-lens defects in the same function collapse to one plan target (they are one
	// fix); a different lens or function stays separate.
	cs := []Confirmation{
		{Candidate: candidateAt("a.go", 6, "f")},
		{Candidate: candidateAt("a.go", 7, "f")}, // same file+fn+lens as above → collapse
		{Candidate: enumerate.Candidate{File: "a.go", Line: 20, Fn: "g", Lens: "other"}},
	}
	cs[0].Candidate.Lens = "stmt-after-return"
	cs[1].Candidate.Lens = "stmt-after-return"
	got := dedupeConfirmed(cs)
	if len(got) != 2 {
		t.Fatalf("want 2 distinct plan targets (f/stmt-after-return, g/other), got %d", len(got))
	}
}

func TestCountDone(t *testing.T) {
	ds := []DrainResult{{Status: DrainDone}, {Status: DrainFailed}, {Status: DrainDone}}
	if got := countDone(ds); got != 2 {
		t.Fatalf("countDone=%d, want 2", got)
	}
}

func TestReconPromptRequestsWitnessAndParamsObject(t *testing.T) {
	// The Recon prompt must ask for the witness snippets (the gate depends on them) and must
	// state that params is an object, not an array — the measured weak-model failure mode.
	p := reconPrompt("task", "digest", "templates", OrientOut{RiskNote: "r"})
	for _, want := range []string{"positive", "negative", "params", "OBJECT"} {
		if !strings.Contains(p, want) {
			t.Errorf("reconPrompt missing %q", want)
		}
	}
}

func TestSanitizeIdent(t *testing.T) {
	if got := sanitizeIdent("l.mu.Lock"); got != "lmuLock" {
		t.Errorf("sanitizeIdent stripped wrong: %q", got)
	}
	if got := sanitizeIdent("()!@#"); got != "Defect" {
		t.Errorf("empty ident must default to Defect, got %q", got)
	}
}

func TestResultConfirmedFilters(t *testing.T) {
	res := &Result{Judged: []Confirmation{
		{Confirmed: true}, {Refuted: true}, {Confirmed: true}, {},
	}}
	if got := len(res.Confirmed()); got != 2 {
		t.Fatalf("Confirmed()=%d, want 2", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("a\nb", 80); got != "a\\nb" {
		t.Errorf("newlines not escaped: %q", got)
	}
	if got := truncate(strings.Repeat("x", 100), 10); len(got) != 10+len("…") {
		t.Errorf("not truncated to 10: len=%d", len(got))
	}
}

func TestReconSpecsMapping(t *testing.T) {
	// The model's ReconOut must map field-for-field onto enumerate.LensSpec, including the
	// witness snippets — dropping any of these silently would defeat the gate.
	r := ReconOut{Lenses: []ReconLens{{
		ID: "x", Template: "call-to", Params: map[string]string{"fn_regex": "y"},
		FreeSCM: "", Mechanism: "call", Anchor: "call", Positive: "p", Negative: "n",
	}}}
	specs := r.specs()
	if len(specs) != 1 {
		t.Fatalf("want 1 spec, got %d", len(specs))
	}
	s := specs[0]
	if s.ID != "x" || s.Template != "call-to" || s.Params["fn_regex"] != "y" ||
		s.Mechanism != "call" || s.Anchor != "call" || s.Positive != "p" || s.Negative != "n" {
		t.Fatalf("spec mapping lost a field: %+v", s)
	}
}

func TestBuildTerrain(t *testing.T) {
	f := writeGo(t, "a.go", brownfieldSrc)
	ter, err := BuildTerrain([]string{f})
	if err != nil {
		t.Fatal(err)
	}
	if ter.Lang != "go" {
		t.Errorf("lang=%q, want go", ter.Lang)
	}
	if ter.NodeKinds["function_declaration"] != 2 {
		t.Errorf("function_declaration count=%d, want 2", ter.NodeKinds["function_declaration"])
	}
	got := strings.Join(ter.Funcs, ",")
	if !strings.Contains(got, "f") || !strings.Contains(got, "helper") {
		t.Errorf("funcs=%v, want f and helper", ter.Funcs)
	}
}
