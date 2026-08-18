package drive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-nullius/internal/dispatch"
	"go-nullius/internal/mandate"
)

// fakeAdapter answers each dispatch by exact Objective match, recording every
// call so tests can assert what was (or wasn't) dispatched.
type fakeAdapter struct {
	responses map[string]string
	err       error
	calls     []dispatch.Request
}

func (f *fakeAdapter) Name() string { return "fake" }

func (f *fakeAdapter) Dispatch(ctx context.Context, req dispatch.Request) (dispatch.Response, error) {
	f.calls = append(f.calls, req)
	if f.err != nil {
		return dispatch.Response{}, f.err
	}
	if resp, ok := f.responses[req.Objective]; ok {
		return dispatch.Response{Text: resp}, nil
	}
	return dispatch.Response{Text: `{"targets":[]}`}, nil
}

func newMachine(a dispatch.Adapter) *Machine {
	return New(Config{Adapter: a})
}

func newMachineAt(a dispatch.Adapter, root string) *Machine {
	return New(Config{Adapter: a, Root: root})
}

func TestRunReconDispatchesFixedPanelPlusEntrypointMap(t *testing.T) {
	dir := t.TempDir()
	writeGoFileWithFunc(t, dir)
	fa := &fakeAdapter{responses: map[string]string{
		"recon/serialization":  `{"targets":["foo.go:Bar"]}`,
		"recon/fault-survival": `{"targets":[],"absent_basis":"no queues/buffers in scope: grep counts 0/0"}`,
	}}
	m := newMachineAt(fa, dir)
	s := &mandate.State{Files: []string{"foo.go"}}
	res := m.runRecon(context.Background(), s)

	if len(res.Targets["serialization"]) != 1 || res.Targets["serialization"][0] != "foo.go:Bar" {
		t.Fatalf("serialization targets = %+v", res.Targets["serialization"])
	}
	foundAbsentNote := false
	for _, n := range res.Notes {
		if strings.Contains(n, "fault-survival") && strings.Contains(n, "grep counts") {
			foundAbsentNote = true
		}
	}
	if !foundAbsentNote {
		t.Fatalf("expected quoted absence basis note, got %+v", res.Notes)
	}
	if _, ok := res.Targets["entrypoint-state-map"]; !ok {
		t.Fatal("expected an entrypoint-state-map panel dispatch")
	}
	// one call per lens + the entrypoint map
	if len(fa.calls) != len(Lenses)+1 {
		t.Fatalf("expected %d dispatches, got %d", len(Lenses)+1, len(fa.calls))
	}
	if len(s.Receipts) != len(fa.calls) {
		t.Fatalf("expected one receipt per dispatch, got %d", len(s.Receipts))
	}
	// The scout cannot read the repo; the driver must inline the terrain.
	for _, c := range fa.calls {
		if !strings.Contains(c.Prompt, "func Bar()") {
			t.Fatalf("recon prompt %q missing inlined terrain declarations:\n%s", c.Objective, c.Prompt)
		}
	}
}

func TestRunReconDropsTargetsNamingFilesNotInRepo(t *testing.T) {
	dir := t.TempDir()
	writeGoFileWithFunc(t, dir)
	fa := &fakeAdapter{responses: map[string]string{
		"recon/serialization": `{"targets":["foo.go:Bar","transaction_manager.c:handle_lost_updates"]}`,
	}}
	m := newMachineAt(fa, dir)
	s := &mandate.State{Files: []string{"foo.go"}}
	res := m.runRecon(context.Background(), s)
	if got := res.Targets["serialization"]; len(got) != 1 || got[0] != "foo.go:Bar" {
		t.Fatalf("expected the confabulated target dropped, got %+v", got)
	}
	found := false
	for _, n := range res.Notes {
		if strings.Contains(n, "transaction_manager.c") && strings.Contains(n, "dropped") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a dropped-target note, got %+v", res.Notes)
	}
}

func TestRunReconPanelMemberFailureRecordsUnknownNeverSilentEmpty(t *testing.T) {
	fa := &fakeAdapter{err: errors.New("boom")}
	m := newMachine(fa)
	s := &mandate.State{Files: []string{"foo.go"}}
	res := m.runRecon(context.Background(), s)
	if res.Targets["serialization"] != nil {
		t.Fatalf("expected nil targets on failure, got %+v", res.Targets["serialization"])
	}
	found := false
	for _, n := range res.Notes {
		if strings.Contains(n, "serialization") && strings.Contains(n, "UNKNOWN") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected UNKNOWN note for failed panel member, got %+v", res.Notes)
	}
}

func writeGoFileWithFunc(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "foo.go")
	if err := os.WriteFile(path, []byte("package foo\n\nfunc Bar() int { return 1 }\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestRunGateMechanicalBackstopForcesFix(t *testing.T) {
	dir := t.TempDir()
	path := writeGoFileWithFunc(t, dir)
	fa := &fakeAdapter{responses: map[string]string{
		"gate": `{"mode":"BUILD","contract":"drafted"}`,
	}}
	m := newMachine(fa)
	s := &mandate.State{Files: []string{path}}
	res, err := m.runGate(context.Background(), s, ReconResult{Targets: map[string][]string{}}, mandate.Doc{Intent: "fix it"})
	if err != nil {
		t.Fatalf("runGate: %v", err)
	}
	if res.Mode != "FIX" {
		t.Fatalf("expected mechanical backstop to force FIX, got %s", res.Mode)
	}
}

func TestRunGateTransportFailureHaltsWithError(t *testing.T) {
	fa := &fakeAdapter{err: errors.New("dial tcp: no route to host")}
	m := newMachine(fa)
	_, err := m.runGate(context.Background(), &mandate.State{}, ReconResult{}, mandate.Doc{})
	if err == nil {
		t.Fatal("expected GATE transport failure to return an error (halt), got nil")
	}
	if !strings.Contains(err.Error(), "GATE dispatch failed") {
		t.Fatalf("error should name the failed dispatch: %v", err)
	}
}

func TestRunGateBuildsValidCardsAndRejectsMalformed(t *testing.T) {
	fa := &fakeAdapter{responses: map[string]string{
		"gate": `{"mode":"FIX","contract":"c","cards":[
			{"question":"Who owns retry state?","blocks":"nothing (proceeding on B)","layer":2,"found":"x","why_you":"y","options":[{"text":"A opt"},{"text":"B opt","recommended":true}]},
			{"question":"","blocks":"nothing","layer":2,"found":"","why_you":"","options":[]}
		]}`,
	}}
	m := newMachine(fa)
	s := &mandate.State{Files: nil}
	res, err := m.runGate(context.Background(), s, ReconResult{}, mandate.Doc{})
	if err != nil {
		t.Fatalf("runGate: %v", err)
	}
	if len(res.Cards) != 1 {
		t.Fatalf("expected 1 valid card kept, got %d: %+v", len(res.Cards), res.Cards)
	}
	if res.Cards[0].ID != "Q1" {
		t.Fatalf("expected card ID Q1, got %s", res.Cards[0].ID)
	}
	foundRejectNote := false
	for _, n := range res.Notes {
		if strings.Contains(n, "rejected malformed card") {
			foundRejectNote = true
		}
	}
	if !foundRejectNote {
		t.Fatalf("expected a rejection note for the malformed card, got %+v", res.Notes)
	}
}

func TestRunGateCapsCardsAtRoundLimit(t *testing.T) {
	cardsJSON := ""
	for i := 0; i < 6; i++ {
		if i > 0 {
			cardsJSON += ","
		}
		cardsJSON += `{"question":"q","blocks":"nothing","layer":2,"found":"f","why_you":"w","options":[{"text":"a"},{"text":"b","recommended":true}]}`
	}
	fa := &fakeAdapter{responses: map[string]string{
		"gate": `{"mode":"FIX","contract":"c","cards":[` + cardsJSON + `]}`,
	}}
	m := newMachine(fa)
	res, err := m.runGate(context.Background(), &mandate.State{}, ReconResult{}, mandate.Doc{})
	if err != nil {
		t.Fatalf("runGate: %v", err)
	}
	if len(res.Cards) != mandate.MaxCardsPerRound {
		t.Fatalf("expected cap of %d cards, got %d", mandate.MaxCardsPerRound, len(res.Cards))
	}
}

func TestRunInterviewHeadlessAssumesRecommendations(t *testing.T) {
	c := mandate.Card{
		ID: "Q1", Question: "q", Blocks: "exported API surface", Layer: mandate.Layer3,
		Found: "f", WhyYou: "w",
		Options: []mandate.Option{{Letter: "A", Text: "a"}, {Letter: "B", Text: "b", Recommended: true}},
	}
	doc := mandate.Doc{Interview: "_(no cards yet)_"}
	appended, err := mandate.AppendCards(doc.Interview, []mandate.Card{c})
	if err != nil {
		t.Fatalf("AppendCards: %v", err)
	}
	doc.Interview = appended

	m := newMachine(&fakeAdapter{})
	s := &mandate.State{Headless: true}
	out, err := m.runInterview(s, &doc, nil)
	if err != nil {
		t.Fatalf("runInterview: %v", err)
	}
	if out.Blocked {
		t.Fatal("headless mode must never block, even on a layer-3 card")
	}
	if len(out.LedgerEntries) != 1 || out.LedgerEntries[0].Kind != "ASSUMED" {
		t.Fatalf("expected 1 ASSUMED ledger entry, got %+v", out.LedgerEntries)
	}
	if len(out.Answered) != 1 {
		t.Fatalf("expected card counted as answered, got %+v", out.Answered)
	}
}

func TestRunInterviewInteractiveBlocksOnLayer3(t *testing.T) {
	c := mandate.Card{
		ID: "Q1", Question: "q", Blocks: "exported API surface", Layer: mandate.Layer3,
		Found: "f", WhyYou: "w",
		Options: []mandate.Option{{Letter: "A", Text: "a"}, {Letter: "B", Text: "b", Recommended: true}},
	}
	doc := mandate.Doc{Interview: "_(no cards yet)_"}
	appended, _ := mandate.AppendCards(doc.Interview, []mandate.Card{c})
	doc.Interview = appended

	m := newMachine(&fakeAdapter{})
	out, err := m.runInterview(&mandate.State{Headless: false}, &doc, nil)
	if err != nil {
		t.Fatalf("runInterview: %v", err)
	}
	if !out.Blocked || len(out.BlockingIDs) != 1 || out.BlockingIDs[0] != "Q1" {
		t.Fatalf("expected Q1 to block, got %+v", out)
	}
}

func TestRunInterviewInteractiveProvisionalOnUnansweredLayer2(t *testing.T) {
	c := mandate.Card{
		ID: "Q1", Question: "q", Blocks: "nothing (proceeding on B)", Layer: mandate.Layer2,
		Found: "f", WhyYou: "w",
		Options: []mandate.Option{{Letter: "A", Text: "a"}, {Letter: "B", Text: "b", Recommended: true}},
	}
	doc := mandate.Doc{Interview: "_(no cards yet)_"}
	appended, _ := mandate.AppendCards(doc.Interview, []mandate.Card{c})
	doc.Interview = appended

	m := newMachine(&fakeAdapter{})
	out, err := m.runInterview(&mandate.State{Headless: false}, &doc, nil)
	if err != nil {
		t.Fatalf("runInterview: %v", err)
	}
	if out.Blocked {
		t.Fatal("layer-2 unanswered must not block")
	}
	if len(out.LedgerEntries) != 1 || out.LedgerEntries[0].Kind != "PROVISIONAL" {
		t.Fatalf("expected PROVISIONAL ledger entry, got %+v", out.LedgerEntries)
	}
}

func TestRunInterviewRejectsMalformedCardNeverWritesIt(t *testing.T) {
	bad := mandate.Card{ID: "Q1", Question: "", Blocks: "nothing", Layer: mandate.Layer2, Found: "f", WhyYou: "w",
		Options: []mandate.Option{{Letter: "A", Text: "a"}, {Letter: "B", Text: "b", Recommended: true}}}
	doc := mandate.Doc{Interview: "_(no cards yet)_"}
	m := newMachine(&fakeAdapter{})
	if _, err := m.runInterview(&mandate.State{}, &doc, []mandate.Card{bad}); err == nil {
		t.Fatal("expected malformed card to be rejected")
	}
	if strings.Contains(doc.Interview, "Q1") {
		t.Fatal("malformed card must never be written to mandate.md")
	}
}

func TestRunHuntParsesVerdictGrammarAndFillsUnhuntedGaps(t *testing.T) {
	fa := &fakeAdapter{responses: map[string]string{
		"hunt/serialization": "V|foo.go:Bar|ABSENT|foo.go:12|`no lock`\n",
	}}
	m := newMachine(fa)
	s := &mandate.State{}
	recon := ReconResult{Targets: map[string][]string{
		"serialization": {"foo.go:Bar", "foo.go:Baz"},
	}}
	entries := m.runHunt(context.Background(), s, recon)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %+v", entries)
	}
	var gotBar, gotBaz bool
	for _, e := range entries {
		if e.Target == "foo.go:Bar" {
			gotBar = true
			if e.Verdict != mandate.Absent || e.Location != "foo.go:12" {
				t.Fatalf("unexpected Bar entry: %+v", e)
			}
		}
		if e.Target == "foo.go:Baz" {
			gotBaz = true
			if e.Verdict != mandate.Unhunted {
				t.Fatalf("expected Baz UNHUNTED (not covered), got %+v", e)
			}
		}
	}
	if !gotBar || !gotBaz {
		t.Fatalf("missing expected targets: %+v", entries)
	}
}

func TestHuntPromptInlinesTargetWindows(t *testing.T) {
	dir := t.TempDir()
	writeGoFileWithFunc(t, dir)
	fa := &fakeAdapter{responses: map[string]string{}}
	m := newMachineAt(fa, dir)
	recon := ReconResult{Targets: map[string][]string{"serialization": {"foo.go:Bar"}}}
	m.runHunt(context.Background(), &mandate.State{}, recon)
	if len(fa.calls) == 0 {
		t.Fatal("expected at least one hunter dispatch")
	}
	// The hunter cannot open files; the driver must inline the enclosing window.
	for _, c := range fa.calls {
		if !strings.Contains(c.Prompt, "func Bar()") {
			t.Fatalf("hunter prompt missing inlined target window:\n%s", c.Prompt)
		}
	}
}

func TestRulePromptResolvesWindowsAgainstRoot(t *testing.T) {
	dir := t.TempDir()
	writeGoFileWithFunc(t, dir)
	suspects := []mandate.ChecklistEntry{{Lens: "serialization", Target: "foo.go:Bar", Verdict: mandate.Absent, Location: "foo.go:3"}}
	prompt := rulePrompt(dir, "contract", suspects, false)
	if !strings.Contains(prompt, "func Bar()") {
		t.Fatalf("rule prompt missing window resolved against mandate root:\n%s", prompt)
	}
}

// The api adapter is JSON-only (caller decodes into a map), so hunters
// reply {"lines": ["V|..."]}; raw V| text stays parseable for agentic
// adapters (observed live: 34/34 UNHUNTED — qwen's JSON-constrained reply
// carried no parseable verdict lines).
func TestRunHuntParsesJSONWrappedVerdictLines(t *testing.T) {
	fa := &fakeAdapter{responses: map[string]string{
		"hunt/serialization": `{"lines": ["V|foo.go:Bar|ABSENT|foo.go:9|` + "`no lock`" + `"]}`,
	}}
	m := newMachine(fa)
	recon := ReconResult{Targets: map[string][]string{"serialization": {"foo.go:Bar"}}}
	entries := m.runHunt(context.Background(), &mandate.State{}, recon)
	if len(entries) != 1 || entries[0].Verdict != mandate.Absent {
		t.Fatalf("expected ABSENT parsed from JSON-wrapped lines, got %+v", entries)
	}
}

// A PRESENT verdict with a quoted mechanism is the protecting mechanism on
// record — it is cleared mechanically, never sent to RULE and never left to
// block AllDisposed (observed live: 15 PRESENT entries dead-ended the drive).
// PRESENT without a quote stays undisposed — fail closed.
func TestRunRuleAutoClearsQuotedPresentEntries(t *testing.T) {
	fa := &fakeAdapter{responses: map[string]string{"rule": `{"suspects":[]}`}}
	m := newMachine(fa)
	entries := []mandate.ChecklistEntry{
		{Lens: "serialization", Target: "a.go:F", Verdict: mandate.Present, Location: "a.go:3", Quote: "`mu.Lock()`"},
		{Lens: "serialization", Target: "b.go:G", Verdict: mandate.Present},
	}
	res := m.runRule(context.Background(), &mandate.State{}, entries, "c", false)
	var quoted, unquoted mandate.ChecklistEntry
	for _, e := range res.Entries {
		switch e.Target {
		case "a.go:F":
			quoted = e
		case "b.go:G":
			unquoted = e
		}
	}
	if quoted.Disposition != mandate.DispDismissed {
		t.Fatalf("quoted PRESENT should auto-clear DISMISSED, got %q", quoted.Disposition)
	}
	if unquoted.Disposition != mandate.DispUndisposed {
		t.Fatalf("unquoted PRESENT must stay undisposed (fail closed), got %q", unquoted.Disposition)
	}
}

// A hunter reply that misses targets gets ONE mechanical re-dispatch with
// only the missing targets before anything is marked UNHUNTED (observed
// live: 3 stragglers dead-ended the drive with no re-entry path).
func TestRunHuntRetriesUncoveredTargetsOnce(t *testing.T) {
	sa := &seqAdapter{responses: map[string][]string{
		"hunt/serialization": {
			"V|foo.go:Bar|ABSENT|foo.go:9|`no lock`\n",
			"V|foo.go:Baz|PRESENT|foo.go:20|`mu.Lock()`\n",
		},
	}}
	m := newMachine(sa)
	recon := ReconResult{Targets: map[string][]string{"serialization": {"foo.go:Bar", "foo.go:Baz"}}}
	entries := m.runHunt(context.Background(), &mandate.State{}, recon)
	if sa.count("hunt/serialization") != 2 {
		t.Fatalf("expected exactly 2 dispatches (initial + one retry), got %d", sa.count("hunt/serialization"))
	}
	byTarget := map[string]mandate.Verdict{}
	for _, e := range entries {
		byTarget[e.Target] = e.Verdict
	}
	if byTarget["foo.go:Bar"] != mandate.Absent || byTarget["foo.go:Baz"] != mandate.Present {
		t.Fatalf("expected retry to cover the straggler, got %+v", entries)
	}
}

func TestRunHuntDispatchFailureMarksAllTargetsUnhunted(t *testing.T) {
	fa := &fakeAdapter{err: errors.New("down")}
	m := newMachine(fa)
	recon := ReconResult{Targets: map[string][]string{"serialization": {"foo.go:Bar"}}}
	entries := m.runHunt(context.Background(), &mandate.State{}, recon)
	if len(entries) != 1 || entries[0].Verdict != mandate.Unhunted {
		t.Fatalf("expected UNHUNTED on dispatch failure, got %+v", entries)
	}
}

func TestRunRuleDisposesSuspectsAndPinsPlans(t *testing.T) {
	fa := &fakeAdapter{responses: map[string]string{
		"rule": `{"suspects":[{"target":"foo.go:Bar","disposition":"CONFIRMED","intent":"add lock","test_name":"TestLock","test_sketch":"sketch","blast_radius":"low"}]}`,
	}}
	m := newMachine(fa)
	s := &mandate.State{}
	entries := []mandate.ChecklistEntry{
		{Lens: "serialization", Target: "foo.go:Bar", Verdict: mandate.Absent, Location: "foo.go:1", Quote: "no lock"},
		{Lens: "swallowed-errors", Target: "foo.go:Present", Verdict: mandate.Present},
	}
	res := m.runRule(context.Background(), s, entries, "own retry state", false)

	var barEntry mandate.ChecklistEntry
	for _, e := range res.Entries {
		if e.Target == "foo.go:Bar" {
			barEntry = e
		}
	}
	if barEntry.Disposition != mandate.DispConfirmed {
		t.Fatalf("expected foo.go:Bar CONFIRMED, got %+v", barEntry)
	}
	if len(res.Plans) != 1 || res.Plans[0].TestName != "TestLock" {
		t.Fatalf("expected 1 pinned plan, got %+v", res.Plans)
	}
	// PRESENT entries are never suspects and must not be sent to RULE.
	if strings.Contains(fa.calls[0].Prompt, "foo.go:Present") {
		t.Fatal("PRESENT entry should not have been included as a suspect")
	}
}

func TestRunRuleLeavesUndisposedWhenModelOmitsTarget(t *testing.T) {
	fa := &fakeAdapter{responses: map[string]string{"rule": `{"suspects":[]}`}}
	m := newMachine(fa)
	entries := []mandate.ChecklistEntry{
		{Lens: "serialization", Target: "foo.go:Bar", Verdict: mandate.Absent, Location: "foo.go:1"},
	}
	res := m.runRule(context.Background(), &mandate.State{}, entries, "contract", false)
	ok, undisposed := mandate.AllDisposed(res.Entries)
	if ok || len(undisposed) != 1 {
		t.Fatalf("expected 1 undisposed suspect blocking advance, got ok=%v undisposed=%+v", ok, undisposed)
	}
}

func TestRunRuleDispatchFailureLeavesAllUndisposed(t *testing.T) {
	fa := &fakeAdapter{err: errors.New("down")}
	m := newMachine(fa)
	entries := []mandate.ChecklistEntry{
		{Target: "foo.go:Bar", Verdict: mandate.Absent, Location: "foo.go:1"},
	}
	res := m.runRule(context.Background(), &mandate.State{}, entries, "contract", false)
	ok, _ := mandate.AllDisposed(res.Entries)
	if ok {
		t.Fatal("expected undisposed suspects on RULE dispatch failure")
	}
}

func TestPromptsCarryLensSemantics(t *testing.T) {
	// Small local models free-associate on bare lens names (observed live:
	// "serialization" hunted as data encoding/marshaling). Every lens in the
	// panel must have a semantics entry, and it must reach both prompts.
	for _, lens := range append(append([]string{}, Lenses...), "entrypoint-state-map") {
		if strings.TrimSpace(lensSemantics[lens]) == "" {
			t.Fatalf("lens %q has no semantics entry", lens)
		}
	}
	rp := reconPrompt("serialization", "== foo.go")
	if !strings.Contains(rp, "NOT data encoding") {
		t.Fatalf("recon prompt missing serialization semantics:\n%s", rp)
	}
	hp := huntPrompt("serialization", []string{"foo.go:Bar"}, t.TempDir())
	if !strings.Contains(hp, "NOT data encoding") {
		t.Fatalf("hunt prompt missing serialization semantics:\n%s", hp)
	}
}

func TestRehuntUnhuntedNoStragglersIsNoop(t *testing.T) {
	fa := &fakeAdapter{}
	m := newMachine(fa)
	entries := []mandate.ChecklistEntry{
		{Lens: "serialization", Target: "a.go:F", Verdict: mandate.Absent, Disposition: mandate.DispDismissed},
	}
	out := m.rehuntUnhunted(context.Background(), &mandate.State{}, entries)
	if len(fa.calls) != 0 {
		t.Fatalf("expected no dispatch when nothing is UNHUNTED, got %d", len(fa.calls))
	}
	if len(out) != 1 || out[0].Disposition != mandate.DispDismissed {
		t.Fatalf("expected entries untouched, got %+v", out)
	}
}
