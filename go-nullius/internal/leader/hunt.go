// Package leader implements the orchestration tools that make go-nullius
// nullius: hunt (lensed haiku hunters over named terrain), rule
// (mechanical quote-verified rulings), close (scout-verified close-out).
package leader

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"

	"go-nullius/internal/ledger"
)

// Dispatcher runs one scout dispatch (scout.Tool in production).
type Dispatcher interface {
	Run(ctx context.Context, input json.RawMessage) (string, bool)
}

// Lenses and their hunting instructions (prose-lens v0; the measured 6/6
// arc came from lensed hunters + the quoted-mechanism grammar).
var Lenses = map[string]string{
	"serialization":     "Is there a lock/serialization mechanism in the entrypoint's OWN body for the shared state it mutates? A lock elsewhere does not count.",
	"fault-survival":    "Is anything cleared, overwritten, or dequeued BEFORE its write/send/flush is confirmed — queues, buffers, retry state, pending maps? The confirm-before-clear is the protective mechanism; clearing first means it is ABSENT.",
	"scope-confinement": "At every fan-out/broadcast site: is the scope argument actually applied to the recipient set, or does the fan-out reach beyond its scope?",
	"wake-predicates":   "For every wait/wake predicate: can it be false at wake? Is it read under the same lock its writer holds?",
	"lost-updates":      "Read-modify-write on shared state without holding a lock across the full cycle (count++, map rebuild, load-then-store).",
	"lifecycle-races":   "Background sweeps/TTL expiry racing live use; shutdown/dispose racing in-flight work; use-after-close.",
	"swallowed-errors":  "Errors discarded (_ =, ignored returns, empty catch), logged-and-continued where the caller needed the failure, or masked by a later success.",
	"resource-release":  "Every acquire (file, conn, lock, goroutine, timer, temp file) with a path that skips its release — early returns, error paths, panics.",
}

// findingGrammar is the cc-nullius V| hunter contract, ported verbatim in
// spirit: the verdict names the PROTECTIVE MECHANISM the lens demands.
// PRESENT = protection verified (SAFE); ABSENT = protection missing or
// vacuous (the absence IS the defect). This polarity is load-bearing: the
// refuter attacks ABSENTs, so with ABSENT=defect every defect claim is
// gated before the leader sees it. path:line is mandatory and mechanically
// byte-checked against disk (the quote gate) — free prose cannot fabricate
// its way onto the checklist.
const findingGrammar = "Per target, decide from quoted code only whether the protective mechanism the lens demands is:\n" +
	"- PRESENT — you can quote it inside the target's OWN body (the lock in the entrypoint itself, the scope arg at the fan-out, the confirm before the clear). A mutex field, doc comment, or sibling's lock is NOT it.\n" +
	"- ABSENT — you can quote the line proving it missing or vacuous (an unlocked mutating body, a nil scope at broadcast, an always-true predicate like `len(x) >= 0`). The absence IS the finding.\n" +
	"- AMBIGUOUS — undecidable from what you read; say what would decide it. Honest AMBIGUOUS beats a guess.\n" +
	"Never report on unopened files. Never write. Cap 40 lines; cover targets in dispatch order, end `OVERFLOW: <n> unexamined` if cut.\n\n" +
	"Output — one line per target, nothing else:\n" +
	"V|<target>|PRESENT|path:line|`quote`\n" +
	"V|<target>|ABSENT|path:line|`quote`\n" +
	"V|<target>|AMBIGUOUS|<what would decide it>\n" +
	"path:line is MANDATORY on PRESENT/ABSENT and is checked mechanically against the file on disk — a quote that is not really there is discarded."

type HuntTool struct {
	Ledger *ledger.Ledger
	Scout  Dispatcher
	Dir    string // workspace root; quote-gate resolves relative finding paths against it
}

// maxDefectsPerHunt caps ABSENT (defect) verdicts filed per hunter
// dispatch. An unbounded weak hunter flags everything; the first N gated
// verdicts (dispatch order = target order) are filed, the rest are
// reported un-filed so the leader can re-hunt narrower targets.
const maxDefectsPerHunt = 3

func (h *HuntTool) Name() string { return "hunt" }

func (h *HuntTool) Def() anthropic.ToolUnionParam {
	names := make([]string, 0, len(Lenses))
	for k := range Lenses {
		names = append(names, k)
	}
	sort.Strings(names)
	return anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
		Name: "hunt",
		Description: anthropic.String(
			"Dispatch one lensed hunter (haiku scout) over named targets. Verdict polarity: PRESENT = protective mechanism verified (safe); ABSENT = mechanism missing (the DEFECT). Findings are filtered against the durable ledger (.nullius/hunt.json): already-ruled items are dropped, previously-fixed items that reappear come back as REGRESSED, fresh items join the open checklist. Every PRESENT/ABSENT quote is byte-checked against disk (fabrications downgraded to AMBIGUOUS), and every ABSENT (defect claim) is attacked by a batched refuter dispatch. Lenses: " + strings.Join(names, ", ")),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"lens":    map[string]any{"type": "string", "enum": names},
				"targets": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "path:symbol target list from the terrain map"},
			},
		},
	}}
}

// Run is the standalone entrypoint: hunt, then refute the ABSENTs inline
// (one extra dispatch). Batch contexts (DispatchTool) call RunDeferred
// instead and refute ALL hunts' ABSENTs in one shared dispatch.
func (h *HuntTool) Run(ctx context.Context, input json.RawMessage) (string, bool) {
	out, absents, isErr := h.RunDeferred(ctx, input)
	if isErr || len(absents) == 0 {
		return out, isErr
	}
	refReport, refErr := h.dispatch(ctx, refuteObjective(absents))
	if refErr {
		return out + "refuter dispatch FAILED (rule the ABSENTs yourself from decisive lines): " + refReport + "\n", false
	}
	return out + "REFUTER REPORT (testimony — you rule):\n" + refReport + "\n", false
}

// RunDeferred hunts and folds findings into the ledger but does NOT
// dispatch the refuter — it returns the ABSENTs for the caller to refute
// (batched across hunts by DispatchTool: one refuter round-trip per batch
// instead of one per hunt).
func (h *HuntTool) RunDeferred(ctx context.Context, input json.RawMessage) (string, []ledger.Finding, bool) {
	var in struct {
		Lens    string   `json:"lens"`
		Targets []string `json:"targets"`
	}
	if err := json.Unmarshal(input, &in); err != nil || in.Lens == "" || len(in.Targets) == 0 {
		return "hunt: invalid input: need {lens, targets[]}", nil, true
	}
	instr, ok := Lenses[in.Lens]
	if !ok {
		return "hunt: unknown lens " + in.Lens, nil, true
	}

	objective := fmt.Sprintf(`You are a lens hunter. Apply EXACTLY ONE lens over the named targets — nothing else.

LENS %q: %s

TARGETS (read each; hunt only these):
%s

%s`, in.Lens, instr, "- "+strings.Join(in.Targets, "\n- "), findingGrammar)

	report, isErr := h.dispatch(ctx, objective)
	if isErr {
		return "hunt: hunter dispatch failed: " + report, nil, true
	}

	findings := parseFindings(report, in.Lens)
	if len(findings) == 0 {
		return "hunt: hunter returned no parseable findings; raw report:\n" + report, nil, false
	}

	// Quote gate (pure orchestrator code, zero LLM turns): every
	// PRESENT/ABSENT quote is checked against the file on disk. Verified
	// quotes are replaced with the REAL disk snippet so hunter prose never
	// propagates; not-found quotes are downgraded to AMBIGUOUS and counted
	// as fabrications.
	var verified, relocated, rejected int
	for i := range findings {
		switch gateQuote(h.Dir, &findings[i]) {
		case gateVerified:
			verified++
		case gateRelocated:
			relocated++
		case gateRejected:
			rejected++
		}
	}

	fresh, regressed := h.Ledger.Filter(findings)
	dropped := len(findings) - len(fresh) - len(regressed)

	var sb strings.Builder
	fmt.Fprintf(&sb, "hunt %s: %d findings — %d fresh, %d REGRESSED, %d already-ruled (dropped, not re-billed)\n",
		in.Lens, len(findings), len(fresh), len(regressed), dropped)
	if verified+relocated+rejected > 0 {
		fmt.Fprintf(&sb, "  quote gate: %d verified on disk, %d relocated (anchor drift), %d REJECTED (fabricated quote → AMBIGUOUS)\n",
			verified, relocated, rejected)
	}

	var absents, overCap []ledger.Finding
	for _, f := range fresh {
		if f.Verdict == "ABSENT" && len(absents) >= maxDefectsPerHunt {
			overCap = append(overCap, f)
			continue
		}
		h.Ledger.Rule(f, ledger.StatusOpen, "on checklist")
		fmt.Fprintf(&sb, "  [%s] %s %s %s — %s\n", f.Key()[:8], f.Verdict, f.File, f.Fn, f.Detail)
		if f.Verdict == "ABSENT" {
			absents = append(absents, f)
		}
	}
	for _, f := range overCap {
		fmt.Fprintf(&sb, "  OVER-CAP (not filed): ABSENT %s %s — defect cap %d/dispatch; re-hunt narrower targets to file it\n",
			f.File, f.Fn, maxDefectsPerHunt)
	}
	for _, f := range regressed {
		h.Ledger.Rule(f, ledger.StatusOpen, "REGRESSED: previously ruled fixed, reappeared")
		fmt.Fprintf(&sb, "  [%s] REGRESSED %s %s — was fixed, reappeared\n", f.Key()[:8], f.File, f.Fn)
	}
	if err := h.Ledger.Save(); err != nil {
		return "hunt: ledger save failed: " + err.Error(), nil, true
	}
	return sb.String(), absents, false
}

// refuteObjective builds one refuter dispatch over ABSENT (defect) claims —
// possibly from several lenses (each item names its own). Polarity note:
// ABSENT = "the protective mechanism is missing" = a DEFECT claim; the
// refuter's job is to find the protection the hunter missed, so false
// defect claims die here instead of on the leader's checklist.
func refuteObjective(absents []ledger.Finding) string {
	var items strings.Builder
	for _, f := range absents {
		fmt.Fprintf(&items, "- [%s] (lens %s: %s) %s %s: claimed defect because: %s (quoted evidence: %q)\n",
			f.Key()[:8], f.Lens, Lenses[f.Lens], f.File, f.Fn, f.Detail, f.SnippetHead)
	}
	return fmt.Sprintf(`You are a refuter. Each item below was claimed a DEFECT (verdict ABSENT — the protective mechanism the lens demands is missing).
Attack each claim: read the cited code and try to find the protecting mechanism the hunter missed (a lock taken by the caller, a confirm on another path, a scope applied upstream). Per item output one line:
[<key>] UPHELD <path:line quote confirming the mechanism really is missing> | REFUTED <path:line quote of the covering mechanism>

ITEMS:
%s`, items.String())
}

func (h *HuntTool) dispatch(ctx context.Context, objective string) (string, bool) {
	raw, _ := json.Marshal(map[string]any{"objective": objective})
	return h.Scout.Run(ctx, raw)
}

var locatorRe = regexp.MustCompile(`^(.+?):(\d+)$`)

// parseFindings scans hunter output for V| grammar lines
// (V|<target>|VERDICT|path:line|`quote` / V|<target>|AMBIGUOUS|<reason>),
// skipping anything unparseable — hunters are testimony, not protocol.
// PRESENT/ABSENT without a parseable path:line locator is dropped: an
// unanchored verdict cannot be quote-gated and is therefore worthless.
func parseFindings(report, lens string) []ledger.Finding {
	var out []ledger.Finding
	for _, line := range strings.Split(report, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "V|") {
			continue
		}
		parts := strings.SplitN(line[2:], "|", 4)
		if len(parts) < 3 {
			continue
		}
		target, verdict := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		f := ledger.Finding{Lens: lens, Fn: target}
		// target is usually path:symbol; keep the symbol as Fn and the
		// path as a fallback File for AMBIGUOUS lines.
		tPath, tSym := splitTarget(target)
		if tSym != "" {
			f.Fn = tSym
		}
		switch verdict {
		case "PRESENT", "ABSENT":
			if len(parts) != 4 {
				continue
			}
			m := locatorRe.FindStringSubmatch(strings.TrimSpace(parts[2]))
			if m == nil {
				continue // path:line mandatory
			}
			f.Verdict = verdict
			f.File = m[1]
			f.Line, _ = strconv.Atoi(m[2])
			f.SnippetHead = strings.Trim(strings.TrimSpace(parts[3]), "`")
			f.Detail = fmt.Sprintf("%s @ %s:%d", verdict, f.File, f.Line)
		case "AMBIGUOUS":
			f.Verdict = verdict
			f.File = tPath
			f.Detail = strings.TrimSpace(strings.Join(parts[2:], "|"))
		default:
			continue
		}
		if f.File == "" {
			continue
		}
		out = append(out, f)
	}
	return out
}

// splitTarget splits a path:symbol target ("a/b.go:Fn" → "a/b.go", "Fn").
func splitTarget(target string) (path, sym string) {
	if i := strings.LastIndexByte(target, ':'); i > 0 {
		return target[:i], target[i+1:]
	}
	return target, ""
}

// Quote-gate outcomes.
const (
	gateVerified  = "verified"  // quote found within ±3 lines of the claimed anchor
	gateRelocated = "relocated" // quote real, anchor drifted; line corrected
	gateRejected  = "rejected"  // quote not on disk — downgraded to AMBIGUOUS
	gateSkipped   = "skipped"   // AMBIGUOUS / nothing to check
)

const gateFuzz = 3 // ± lines of anchor drift tolerated before a full-file search

// gateQuote byte-checks a finding's quoted snippet against the file on
// disk (whitespace-normalized, ±gateFuzz line fuzz around the claimed
// anchor). Verified/relocated quotes are REPLACED with the real disk line
// so hunter prose never reaches the leader; a quote not found anywhere in
// the file (or an unreadable file — "never report on unopened files")
// downgrades the verdict to AMBIGUOUS. Pure orchestrator code: no model in
// the loop.
func gateQuote(dir string, f *ledger.Finding) string {
	if f.Verdict != "PRESENT" && f.Verdict != "ABSENT" {
		return gateSkipped
	}
	reject := func(why string) string {
		f.Detail = fmt.Sprintf("quote gate REJECTED %s claim: %s (was: %s)", f.Verdict, why, f.Detail)
		f.Verdict = "AMBIGUOUS"
		return gateRejected
	}
	path := f.File
	if dir != "" && !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return reject("cannot read " + f.File + " — hunter reported on an unopened file")
	}
	want := normalizeWS(f.SnippetHead)
	if want == "" {
		return reject("empty quote")
	}
	lines := strings.Split(string(raw), "\n")
	if i := findQuote(lines, want, f.Line-1-gateFuzz, f.Line+gateFuzz); i >= 0 {
		f.Line, f.SnippetHead = i+1, strings.TrimSpace(lines[i])
		return gateVerified
	}
	if i := findQuote(lines, want, 0, len(lines)); i >= 0 {
		f.Line, f.SnippetHead = i+1, strings.TrimSpace(lines[i])
		f.Detail = fmt.Sprintf("%s @ %s:%d (anchor corrected by quote gate)", f.Verdict, f.File, i+1)
		return gateRelocated
	}
	return reject("quoted snippet not found in " + f.File)
}

// findQuote returns the first line index in [lo,hi) whose normalized text
// contains the (normalized) quote, or where the quote spans that line and
// its successors; -1 if absent. Bounds are clamped.
func findQuote(lines []string, want string, lo, hi int) int {
	lo, hi = max(0, lo), min(len(lines), hi)
	for i := lo; i < hi; i++ {
		ln := normalizeWS(lines[i])
		if ln == "" {
			continue
		}
		if strings.Contains(ln, want) {
			return i
		}
		// Multi-line quote: must start on this line and continue verbatim.
		if strings.HasPrefix(want, ln) {
			joined := ln
			for j := i + 1; j < len(lines) && len(joined) < len(want); j++ {
				joined += " " + normalizeWS(lines[j])
			}
			if strings.Contains(joined, want) {
				return i
			}
		}
	}
	return -1
}

var wsRe = regexp.MustCompile(`\s+`)

func normalizeWS(s string) string { return wsRe.ReplaceAllString(strings.TrimSpace(s), " ") }
