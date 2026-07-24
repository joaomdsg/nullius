package machine

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"go-nullius/internal/caller"
	"go-nullius/internal/enumerate"

	gts "github.com/odvcencio/gotreesitter"
)

// judgeRadius is how many lines of context around a candidate are offered to Judge as the
// evidence window. The decisive line must fall inside it — a skeleton stand-in for the
// design's "enclosing function text" (exact spans are a step-7 refinement).
const (
	judgeRadius     = 8
	judgeMaxTokens  = 1500
	refuteMaxTokens = 1200
)

// JudgeOut is the fast tier's forced-choice ruling on one candidate.
type JudgeOut struct {
	Answer       string `json:"answer"` // DEFECT | CORRECT | CANT_TELL
	DecisiveLine int    `json:"decisive_line"`
	Because      string `json:"because"`
}

// RefuteOut is the corroboration refuter's attempt to overturn a DEFECT.
type RefuteOut struct {
	Stands       bool `json:"stands"`
	RefutingLine *int `json:"refuting_line"`
}

// Confirmation is the full disposition of one candidate through Judge + Corroborate.
type Confirmation struct {
	Candidate enumerate.Candidate
	Judge     JudgeOut
	LineValid bool // decisive line fell inside the offered evidence (Corroborate filter 1)
	Refuted   bool // the refuter overturned the DEFECT
	Confirmed bool // survived all filters → a real defect
	Note      string
}

// judgeAndCorroborate runs one candidate through the discrimination pipeline on the fast
// tier: Judge (forced choice) → line-validity gate → refuter. It fails CLOSED toward NOT
// confirming: any model failure or an out-of-evidence decisive line yields an unconfirmed
// disposition (which surfaces as a RISK, never an auto-fix). Only a DEFECT whose decisive
// line is real AND that the refuter cannot overturn is Confirmed.
func (m *Machine) judgeAndCorroborate(ctx context.Context, task string, c enumerate.Candidate, log logf) Confirmation {
	conf := Confirmation{Candidate: c}
	lines, err := readLines(c.File)
	if err != nil {
		conf.Judge.Answer = "CANT_TELL"
		conf.Note = "unreadable file: " + err.Error()
		log(PhaseJudge, "%s:%d skip (%v) → CANT_TELL", c.File, c.Line, err)
		return conf
	}
	win, start, end := enclosingWindow(c.File, lines, c.Line)

	var j JudgeOut
	if err := m.Caller.Ask(ctx, m.Fast, judgePrompt(task, c, win, start, end), caller.GBNF(jsonGrammar), &j, caller.WithMaxTokens(judgeMaxTokens)); err != nil {
		conf.Judge = JudgeOut{Answer: "CANT_TELL"}
		conf.Note = "judge fallback: " + err.Error()
		log(PhaseJudge, "%s:%d FALLBACK (%v) → CANT_TELL", c.File, c.Line, err)
		return conf
	}
	conf.Judge = j
	log(PhaseJudge, "%s:%d [%s] → %s (line %d): %s", c.File, c.Line, c.Lens, strings.ToUpper(j.Answer), j.DecisiveLine, j.Because)
	if strings.ToUpper(strings.TrimSpace(j.Answer)) != "DEFECT" {
		return conf
	}

	// Corroborate filter 1 — the decisive line must be inside the evidence and non-blank.
	// A model that cites a line it was not shown has not proven anything.
	if !validLine(lines, start, end, j.DecisiveLine) {
		conf.Judge.Answer = "CANT_TELL"
		conf.Note = fmt.Sprintf("decisive line %d outside evidence [%d,%d]", j.DecisiveLine, start, end)
		log(PhaseCorroborate, "%s:%d decisive line %d out of evidence → CANT_TELL", c.File, c.Line, j.DecisiveLine)
		return conf
	}
	conf.LineValid = true

	// Corroborate filter 3 — the refuter. On failure we do LESS: leave it unconfirmed
	// (a reported RISK), never auto-confirm what we could not verify.
	var r RefuteOut
	if err := m.Caller.Ask(ctx, m.Fast, refutePrompt(task, c, win, start, end, j), caller.GBNF(jsonGrammar), &r, caller.WithMaxTokens(refuteMaxTokens)); err != nil {
		conf.Note = "refuter fallback (unverified): " + err.Error()
		log(PhaseCorroborate, "%s:%d refuter FALLBACK (%v) → UNVERIFIED (not confirmed)", c.File, c.Line, err)
		return conf
	}
	if !r.Stands {
		// A refutation is testimony too (nullius: a REFUTED ruling is never a bare word).
		// It only overturns a judge-affirmed DEFECT if it cites a VALID, DISTINCT line that
		// shows the safety mechanism. A "stands=false" with no line — or one pointing at the
		// flagged line itself — has quoted no mechanism, so the defect STANDS. (Measured live:
		// the weak fast refuter overturns genuine unreachable code with refuting_line=null or
		// =the flagged line; this gate is what stops a false clear.)
		if r.RefutingLine != nil && *r.RefutingLine != j.DecisiveLine && validLine(lines, start, end, *r.RefutingLine) {
			conf.Refuted = true
			conf.Note = fmt.Sprintf("refuted at line %d", *r.RefutingLine)
			log(PhaseCorroborate, "%s:%d REFUTED (line %d)", c.File, c.Line, *r.RefutingLine)
			return conf
		}
		conf.Note = "refutation unsupported (no valid distinct refuting line) → defect stands"
		log(PhaseCorroborate, "%s:%d refutation UNSUPPORTED (line %v) → defect STANDS", c.File, c.Line, deref(r.RefutingLine))
	}
	conf.Confirmed = true
	log(PhaseCorroborate, "%s:%d CONFIRMED", c.File, c.Line)
	return conf
}

// maxSmartEscalations bounds how many pair-discriminations may escalate to the smart tier
// per run (design: smart escalation ≤3/run) — the smart tier is the flaky/expensive one, so
// its use is rationed; exhausting the budget leaves the remaining ties unconfirmed (a RISK).
const maxSmartEscalations = 3

const pairMaxTokens = 1500

// PairOut is the contrastive discrimination verdict over two structurally-identical sites A
// and B that per-candidate Judge could not split (D2 vs FP). Exactly one is expected UNSAFE;
// each verdict must cite the line where its decisive (scope-carrying) value originates.
type PairOut struct {
	A            string `json:"a"` // SAFE | UNSAFE
	B            string `json:"b"`
	AScopeSource int    `json:"a_scope_source_line"`
	BScopeSource int    `json:"b_scope_source_line"`
}

// pairDiscriminate is Corroborate filter 2. Per-candidate Judge is blind to context: two sites
// with an identical shape (e.g. two calls that differ only in which arg carries scope) both
// come back CANT_TELL. This pass groups the still-unresolved CANT_TELL candidates by lens and,
// for each same-lens pair, asks ONE contrastive question showing BOTH windows: which is safe,
// and cite where each site's decisive value originates. A differentiated answer (one SAFE, one
// UNSAFE) whose UNSAFE side cites a valid origin line CONFIRMS the UNSAFE site and clears the
// other. An undifferentiated answer escalates the pair to the smart tier (budget ≤3/run); a
// still-undifferentiated smart answer leaves both unconfirmed (fail-closed → RISK).
func (m *Machine) pairDiscriminate(ctx context.Context, task string, res *Result, log logf) {
	byLens := map[string][]int{}
	for i := range res.Judged {
		c := &res.Judged[i]
		if c.Confirmed || c.Refuted {
			continue
		}
		if strings.ToUpper(strings.TrimSpace(c.Judge.Answer)) != "CANT_TELL" {
			continue
		}
		byLens[c.Candidate.Lens] = append(byLens[c.Candidate.Lens], i)
	}
	lenses := make([]string, 0, len(byLens))
	for l := range byLens {
		lenses = append(lenses, l)
	}
	sort.Strings(lenses) // deterministic escalation-budget consumption
	escalations := 0
	for _, lens := range lenses {
		idxs := byLens[lens]
		for k := 0; k+1 < len(idxs); k += 2 {
			m.discriminatePair(ctx, task, res, idxs[k], idxs[k+1], &escalations, log)
		}
	}
}

func (m *Machine) discriminatePair(ctx context.Context, task string, res *Result, ia, ib int, escalations *int, log logf) {
	ca, cb := res.Judged[ia].Candidate, res.Judged[ib].Candidate
	la, err := readLines(ca.File)
	if err != nil {
		return
	}
	lb, err := readLines(cb.File)
	if err != nil {
		return
	}
	winA, sa, ea := enclosingWindow(ca.File, la, ca.Line)
	winB, sb, eb := enclosingWindow(cb.File, lb, cb.Line)
	prompt := pairPrompt(task, ca, winA, sa, ea, cb, winB, sb, eb)

	var p PairOut
	if err := m.Caller.Ask(ctx, m.Fast, prompt, caller.GBNF(jsonGrammar), &p, caller.WithMaxTokens(pairMaxTokens)); err == nil {
		if m.applyPair(res, ia, ib, la, sa, ea, lb, sb, eb, p, "fast", log) {
			return
		}
	} else {
		log(PhaseCorroborate, "pair-discrimination fast FALLBACK (%v)", err)
	}

	// Undifferentiated (or fast failure) → escalate to the smart tier, budget permitting.
	if *escalations >= maxSmartEscalations {
		log(PhaseCorroborate, "pair %s:%d/%s:%d unresolved; smart-escalation budget (%d) exhausted → both stay unconfirmed",
			ca.File, ca.Line, cb.File, cb.Line, maxSmartEscalations)
		return
	}
	*escalations++
	var ps PairOut
	if err := m.Caller.Ask(ctx, m.Smart, prompt, caller.GBNF(jsonGrammar), &ps, caller.WithMaxTokens(pairMaxTokens)); err != nil {
		log(PhaseCorroborate, "pair-discrimination smart FALLBACK (%v) → both stay unconfirmed", err)
		return
	}
	if !m.applyPair(res, ia, ib, la, sa, ea, lb, sb, eb, ps, "smart", log) {
		log(PhaseCorroborate, "pair %s:%d/%s:%d still undifferentiated after smart → both stay unconfirmed", ca.File, ca.Line, cb.File, cb.Line)
	}
}

// applyPair resolves a differentiated pair verdict: exactly one UNSAFE (whose origin line is
// valid) is CONFIRMED, the other cleared. It returns false (no mutation) when the verdict is
// undifferentiated (equal, or a non-SAFE/UNSAFE token) or the UNSAFE side cites no valid line
// — a bare "this one is unsafe" without a cited origin has discriminated nothing.
func (m *Machine) applyPair(res *Result, ia, ib int, la []string, sa, ea int, lb []string, sb, eb int, p PairOut, tier string, log logf) bool {
	a := strings.ToUpper(strings.TrimSpace(p.A))
	b := strings.ToUpper(strings.TrimSpace(p.B))
	safeOK := func(v string) bool { return v == "SAFE" || v == "UNSAFE" }
	if !safeOK(a) || !safeOK(b) || a == b {
		return false
	}
	var ui, si, uSrc, us, ue int
	var uLines []string
	if a == "UNSAFE" {
		ui, si, uSrc, uLines, us, ue = ia, ib, p.AScopeSource, la, sa, ea
	} else {
		ui, si, uSrc, uLines, us, ue = ib, ia, p.BScopeSource, lb, sb, eb
	}
	if !validLine(uLines, us, ue, uSrc) {
		return false // no cited origin for the unsafe call → nothing proven
	}
	res.Judged[ui].Confirmed = true
	res.Judged[ui].LineValid = true
	res.Judged[ui].Note = fmt.Sprintf("confirmed by pair-discrimination (%s tier), scope source line %d", tier, uSrc)
	res.Judged[si].Note = "cleared by pair-discrimination (" + tier + " tier)"
	log(PhaseCorroborate, "pair-discrimination (%s): %s:%d UNSAFE→CONFIRMED, %s:%d SAFE→cleared",
		tier, res.Judged[ui].Candidate.File, res.Judged[ui].Candidate.Line,
		res.Judged[si].Candidate.File, res.Judged[si].Candidate.Line)
	return true
}

func pairPrompt(task string, a enumerate.Candidate, winA string, sa, ea int, b enumerate.Candidate, winB string, sb, eb int) string {
	return `You are the PAIR-DISCRIMINATION phase. Two sites matched the SAME lens and are structurally
identical, so judging them in isolation was ambiguous. Typically exactly one is a real defect and the
other is safe, distinguished by WHICH value/argument carries the relevant scope and WHERE it originates.
Compare A and B and decide each "SAFE" or "UNSAFE". For each, cite the line where its decisive value
originates (A's line in [` + itoa(sa) + `,` + itoa(ea) + `], B's in [` + itoa(sb) + `,` + itoa(eb) + `]).
Reply with ONLY a JSON object:
{"a":"SAFE"|"UNSAFE","b":"SAFE"|"UNSAFE","a_scope_source_line":<int>,"b_scope_source_line":<int>}

TASK:
` + task + `

LENS: ` + a.Lens + ` (mechanism: ` + a.Mechanism + `)

SITE A: ` + a.File + `:` + itoa(a.Line) + ` in function ` + a.Fn + `
CODE A (line: text):
` + winA + `
SITE B: ` + b.File + `:` + itoa(b.Line) + ` in function ` + b.Fn + `
CODE B (line: text):
` + winB
}

func judgePrompt(task string, c enumerate.Candidate, window string, start, end int) string {
	return `You are the JUDGE phase. A mechanical lens flagged the site below. Decide, for THIS task, whether
the site is a real "DEFECT", is "CORRECT" (safe as written), or "CANT_TELL". Pick the single decisive
line number (in [` + itoa(start) + `,` + itoa(end) + `]) that proves your call. Reply with ONLY a JSON object:
{"answer": "DEFECT"|"CORRECT"|"CANT_TELL", "decisive_line": <int>, "because": "<=160 chars"}

TASK:
` + task + `

LENS: ` + c.Lens + ` (mechanism: ` + c.Mechanism + `)
SITE: ` + c.File + `:` + itoa(c.Line) + ` in function ` + c.Fn + `

CODE (line: text):
` + window
}

func refutePrompt(task string, c enumerate.Candidate, window string, start, end int, j JudgeOut) string {
	return `You are the REFUTE phase. A prior judge called the site below a DEFECT. Try HARD to REFUTE that:
is there a mechanism in the shown code that makes it actually safe/correct? Answer stands=true ONLY if
you cannot refute it. If you refute it, give the line that proves safety. Reply with ONLY a JSON object:
{"stands": true|false, "refuting_line": <int or null>}

TASK:
` + task + `

SITE: ` + c.File + `:` + itoa(c.Line) + ` in function ` + c.Fn + `
PRIOR JUDGE said DEFECT because: ` + j.Because + `

CODE (line: text):
` + window
}

func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return strings.Split(string(data), "\n"), nil
}

func renderRange(lines []string, start, end int) string {
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	var b strings.Builder
	for i := start; i <= end; i++ {
		fmt.Fprintf(&b, "%d: %s\n", i, lines[i-1])
	}
	return b.String()
}

func renderWindow(lines []string, line, radius int) (text string, start, end int) {
	start, end = line-radius, line+radius
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	return renderRange(lines, start, end), start, end
}

// enclosingWindow returns the source of the smallest function/method enclosing the
// candidate line, with its line range — so Judge and the refuter reason within ONE function
// and cannot cite a line from a sibling function as evidence (measured: a fixed ±radius
// window let the refuter clear a real missing-Unlock by pointing at a DIFFERENT function's
// defer Unlock). Falls back to a ±radius window when no enclosing function is found.
func enclosingWindow(file string, lines []string, line int) (text string, start, end int) {
	f, err := enumerate.ParseFile(file)
	if err != nil || f.Profile == nil {
		return renderWindow(lines, line, judgeRadius)
	}
	bs, be, span := 0, 0, 1<<30
	var walk func(n *gts.Node)
	walk = func(n *gts.Node) {
		if n == nil {
			return
		}
		if f.Profile.Is(enumerate.RoleFunction, n.Type(f.Lang)) {
			s, e := int(n.StartPoint().Row)+1, int(n.EndPoint().Row)+1
			if s <= line && line <= e && e-s < span {
				span, bs, be = e-s, s, e
			}
		}
		for i := 0; i < n.NamedChildCount(); i++ {
			walk(n.NamedChild(i))
		}
	}
	walk(f.Tree.RootNode())
	if span == 1<<30 {
		return renderWindow(lines, line, judgeRadius)
	}
	return renderRange(lines, bs, be), bs, be
}

// validLine reports whether n is a real, non-blank line inside the offered window.
func validLine(lines []string, start, end, n int) bool {
	if n < start || n > end || n < 1 || n > len(lines) {
		return false
	}
	return strings.TrimSpace(lines[n-1]) != ""
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

func deref(p *int) string {
	if p == nil {
		return "nil"
	}
	return itoa(*p)
}
