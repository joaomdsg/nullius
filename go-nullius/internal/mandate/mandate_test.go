package mandate

import (
	"strings"
	"testing"
)

func TestStateSaveLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	s := &State{
		Slug:    "retry-ownership",
		Phase:   "HUNT",
		Mode:    "FIX",
		Head:    "abc123",
		Budgets: DefaultBudgets(),
		Receipts: []Receipt{
			{Phase: "RECON", Agent: "scout/serialization", Tokens: 38935, Ms: 45047},
		},
		ReconTargets:  map[string][]string{"serialization": {"foo.go:Bar"}},
		ExecResults:   []ExecRecord{{Target: "foo.go:Bar", Status: "DONE"}},
		Checklist:     []ChecklistEntry{{Lens: "serialization", Target: "foo.go:Bar", Verdict: Absent, Disposition: DispConfirmed}},
		Plans:         []PlanRecord{{Target: "foo.go:Bar", Intent: "add lock"}},
		HuntReentries: 1,
	}
	if err := s.Save(root); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := LoadState(root, "retry-ownership")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got.Phase != "HUNT" || got.Mode != "FIX" || got.Head != "abc123" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if len(got.Receipts) != 1 || got.Receipts[0].Tokens != 38935 {
		t.Fatalf("receipts mismatch: %+v", got.Receipts)
	}
	if len(got.ReconTargets["serialization"]) != 1 || got.ExecResults[0].Status != "DONE" {
		t.Fatalf("recon/exec round trip mismatch: %+v", got)
	}
	if len(got.Checklist) != 1 || got.Checklist[0].Disposition != DispConfirmed {
		t.Fatalf("checklist round trip mismatch: %+v", got.Checklist)
	}
	if len(got.Plans) != 1 || got.Plans[0].Intent != "add lock" {
		t.Fatalf("plans round trip mismatch: %+v", got.Plans)
	}
	if got.HuntReentries != 1 {
		t.Fatalf("hunt-reentry bound must survive resume, got %d", got.HuntReentries)
	}
}

func TestLoadStateMissingErrors(t *testing.T) {
	root := t.TempDir()
	if _, err := LoadState(root, "nope"); err == nil {
		t.Fatal("expected error loading nonexistent state")
	}
}

func TestPlanPathNaming(t *testing.T) {
	p := Paths("/root", "slug")
	got := p.PlanPath(1, "internal/foo/bar.go")
	want := p.PlansDir + "/01-bar-go.md"
	if got != want {
		t.Fatalf("PlanPath = %q, want %q", got, want)
	}
	got2 := p.PlanPath(12, "x")
	if !strings.HasSuffix(got2, "/12-x.md") {
		t.Fatalf("PlanPath(12) = %q", got2)
	}
}

func validCard(id string) Card {
	return Card{
		ID:       id,
		Question: "Who owns retry state?",
		Blocks:   "nothing (proceeding on B)",
		Layer:    Layer2,
		Found:    "`queue.go:141` clears `pending` before `flush()` confirms.",
		WhyYou:   "both orderings are implementable; the mandate text doesn't pick one.",
		Options: []Option{
			{Letter: "A", Text: "flush owns it"},
			{Letter: "B", Text: "sweep owns it", Recommended: true},
			{Letter: "C", Text: "something else"},
		},
	}
}

func TestCardValidateAccepts(t *testing.T) {
	if err := validCard("Q1").Validate(); err != nil {
		t.Fatalf("expected valid card, got %v", err)
	}
}

func TestCardValidateRejectsMalformed(t *testing.T) {
	cases := []struct {
		name string
		mut  func(c Card) Card
	}{
		{"bad id", func(c Card) Card { c.ID = "Question1"; return c }},
		{"no blocks", func(c Card) Card { c.Blocks = ""; return c }},
		{"no found", func(c Card) Card { c.Found = ""; return c }},
		{"no why", func(c Card) Card { c.WhyYou = ""; return c }},
		{"bad layer", func(c Card) Card { c.Layer = 0; return c }},
		{"one option", func(c Card) Card { c.Options = c.Options[:1]; return c }},
		{"letters not sequential", func(c Card) Card {
			c.Options[1].Letter = "Z"
			return c
		}},
		{"no recommendation", func(c Card) Card {
			for i := range c.Options {
				c.Options[i].Recommended = false
			}
			return c
		}},
		{"two recommendations", func(c Card) Card {
			c.Options[0].Recommended = true
			return c
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.mut(validCard("Q1"))
			if err := c.Validate(); err == nil {
				t.Fatalf("%s: expected rejection, got nil error", tc.name)
			}
		})
	}
}

func TestValidateRoundCapsAtFour(t *testing.T) {
	cards := []Card{validCard("Q1"), validCard("Q2"), validCard("Q3"), validCard("Q4"), validCard("Q5")}
	if err := ValidateRound(cards); err == nil {
		t.Fatal("expected cap rejection for 5 cards")
	}
	if err := ValidateRound(cards[:4]); err != nil {
		t.Fatalf("4 cards should be accepted: %v", err)
	}
}

func TestValidateRoundRejectsDuplicateID(t *testing.T) {
	cards := []Card{validCard("Q1"), validCard("Q1")}
	if err := ValidateRound(cards); err == nil {
		t.Fatal("expected duplicate-id rejection")
	}
}

func TestMalformedCardNeverWritten(t *testing.T) {
	bad := validCard("Q1")
	bad.Found = ""
	if _, err := AppendCards("_(no cards yet)_", []Card{bad}); err == nil {
		t.Fatal("expected AppendCards to reject malformed card")
	} else if strings.Contains(err.Error(), "no cards yet") {
		t.Fatal("error should not leak prior body")
	}
}

func TestRenderParseCardRoundTrip(t *testing.T) {
	c := validCard("Q2")
	rendered := RenderCard(c)
	if !strings.Contains(rendered, "### Q2 — Who owns retry state? ·· blocks: nothing (proceeding on B)") {
		t.Fatalf("header missing: %s", rendered)
	}
	parsed := ParseCards(rendered)
	if len(parsed) != 1 {
		t.Fatalf("expected 1 parsed card, got %d", len(parsed))
	}
	p := parsed[0]
	if p.ID != "Q2" || p.Recommended() != "B" {
		t.Fatalf("parsed mismatch: %+v", p)
	}
	if p.Answered() {
		t.Fatal("untouched card (only recommendation checked) should not read as Answered")
	}
}

func TestParseCardsDetectsUserAnswer(t *testing.T) {
	body := validCard("Q1")
	rendered := RenderCard(body)
	// user re-checks A instead of the recommended B
	rendered = strings.Replace(rendered, "- [ ] A.", "- [x] A.", 1)
	rendered = strings.Replace(rendered, "- [x] B. sweep owns it  ← recommended", "- [ ] B. sweep owns it  ← recommended", 1)
	parsed := ParseCards(rendered)
	if len(parsed) != 1 {
		t.Fatalf("expected 1 card, got %d", len(parsed))
	}
	if parsed[0].Selected() != "A" {
		t.Fatalf("expected selected A, got %q", parsed[0].Selected())
	}
	if !parsed[0].Answered() {
		t.Fatal("expected Answered true when user picks non-recommended option")
	}
}

// A blocking card whose recommendation the user AGREES with needs an
// explicit affirm mark — `- [X]` (capital X) — because the pre-checked
// `- [x]` is byte-identical whether the user read it or not (observed live:
// 4 blocking cards with sound recommendations and no way to accept them).
func TestParseCardsCapitalXAffirmsRecommendation(t *testing.T) {
	rendered := RenderCard(validCard("Q1"))
	affirmed := strings.Replace(rendered, "- [x] B.", "- [X] B.", 1)

	if got := ParseCards(rendered); got[0].Answered() {
		t.Fatal("machine-rendered lowercase [x] must NOT read as answered")
	}
	parsed := ParseCards(affirmed)
	if !parsed[0].Answered() {
		t.Fatal("capital [X] on the recommendation must read as answered")
	}
	if parsed[0].Selected() != "B" {
		t.Fatalf("affirmed selection = %q, want B", parsed[0].Selected())
	}
}

func TestParseCardsPrefersUserTickWhenTwoBoxesChecked(t *testing.T) {
	rendered := RenderCard(validCard("Q1"))
	// user checks A but forgets to uncheck the pre-checked recommendation B
	twoChecked := strings.Replace(rendered, "- [ ] A.", "- [x] A.", 1)
	parsed := ParseCards(twoChecked)
	if parsed[0].Selected() != "A" {
		t.Fatalf("expected the non-recommended tick to win, got %q", parsed[0].Selected())
	}
	if !parsed[0].Answered() {
		t.Fatal("expected Answered true when a second box is checked")
	}
}

func TestAppendCardsRemovesPlaceholder(t *testing.T) {
	body, err := AppendCards("_(no cards yet)_", []Card{validCard("Q1")})
	if err != nil {
		t.Fatalf("AppendCards: %v", err)
	}
	if strings.Contains(body, "no cards yet") {
		t.Fatalf("placeholder must be removed once cards exist:\n%s", body)
	}
	if !strings.Contains(body, "### Q1") {
		t.Fatalf("card missing:\n%s", body)
	}
}

func TestDocParseRenderRoundTrip(t *testing.T) {
	d := Doc{
		Status:       "HUNT · 2 questions open (0 blocking) · rerun `nullius drive`",
		Intent:       "Fix retry ownership race.",
		Contract:     "Own retry state in sweep.",
		Interview:    "### Q1 — x ·· blocks: nothing\n**Found:** y\n**Why you:** z\n- [x] A. a  ← recommended",
		Ratification: "_(populated at RATIFY)_",
	}
	rendered := d.Render()
	got := ParseDoc(rendered)
	if got.Status != d.Status {
		t.Fatalf("status mismatch: %q vs %q", got.Status, d.Status)
	}
	if strings.TrimSpace(got.Intent) != d.Intent {
		t.Fatalf("intent mismatch: %q vs %q", got.Intent, d.Intent)
	}
	if strings.TrimSpace(got.Contract) != d.Contract {
		t.Fatalf("contract mismatch: %q vs %q", got.Contract, d.Contract)
	}
	if !strings.Contains(got.Interview, "Q1") {
		t.Fatalf("interview mismatch: %q", got.Interview)
	}
}

func TestDocParseMissingSectionsLeavesEmpty(t *testing.T) {
	got := ParseDoc("> STATUS: INIT\n\n## INTENT\n\nhello\n")
	if got.Intent != "hello" {
		t.Fatalf("intent = %q", got.Intent)
	}
	if got.Contract != "" || got.Interview != "" || got.Ratification != "" {
		t.Fatalf("expected empty missing sections, got %+v", got)
	}
}

func TestScaffoldCreatesFileProtocol(t *testing.T) {
	root := t.TempDir()
	s, err := Scaffold(root, "retry-ownership", "abc123", InitOptions{Intent: "fix the race", Files: []string{"a.go", "b.go"}})
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	if s.Phase != "INIT" || s.ReconMode != "scouts" {
		t.Fatalf("unexpected initial state: %+v", s)
	}
	if len(s.Files) != 2 || s.Files[0] != "a.go" {
		t.Fatalf("expected scope files persisted, got %+v", s.Files)
	}
	doc, err := ReadDoc(root, "retry-ownership")
	if err != nil {
		t.Fatalf("ReadDoc: %v", err)
	}
	if !strings.Contains(doc.Intent, "fix the race") {
		t.Fatalf("intent not written: %q", doc.Intent)
	}
	loaded, err := LoadState(root, "retry-ownership")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if loaded.Head != "abc123" {
		t.Fatalf("head not persisted: %+v", loaded)
	}
}

func TestScaffoldRefusesToOverwriteExisting(t *testing.T) {
	root := t.TempDir()
	if _, err := Scaffold(root, "slug", "abc", InitOptions{}); err != nil {
		t.Fatalf("first Scaffold: %v", err)
	}
	if _, err := Scaffold(root, "slug", "def", InitOptions{}); err == nil {
		t.Fatal("expected error scaffolding over an existing mandate")
	}
}

func TestAppendCardsAccumulatesAcrossRounds(t *testing.T) {
	body := "_(no cards yet)_"
	body, err := AppendCards(body, []Card{validCard("Q1")})
	if err != nil {
		t.Fatalf("round 1: %v", err)
	}
	body, err = AppendCards(body, []Card{validCard("Q2")})
	if err != nil {
		t.Fatalf("round 2: %v", err)
	}
	if !strings.Contains(body, "Q1") || !strings.Contains(body, "Q2") {
		t.Fatalf("expected both rounds preserved: %s", body)
	}
}
