package mandate

import (
	"fmt"
	"regexp"
	"strings"
)

// MaxCardsPerRound caps INTERVIEW cards per round: past ~4, users satisfice
// and a careless answer carries false authority, worse than ASSUMED
// (DESIGN-mandates.md §5).
const MaxCardsPerRound = 4

// Layer classifies a card's escape analysis verdict (DESIGN-mandates.md §5):
// layer-2 (revertible in worktree) proceeds PROVISIONAL; layer-3 (escapes:
// exported API, wire format, shared DB, spend, dep lock-in) blocks.
// Unclassifiable defaults to layer-3 — fail closed.
type Layer int

const (
	Layer2 Layer = 2
	Layer3 Layer = 3
)

// Option is one lettered choice on an interview card.
type Option struct {
	Letter      string
	Text        string
	Recommended bool
	Selected    bool
	// Affirmed marks an explicit user answer: `- [X]` (capital X). The
	// machine always renders lowercase `[x]`, so a capital X can only come
	// from a human edit — it is how a user ACCEPTS the pre-checked
	// recommendation on a blocking card (a lowercase [x] is byte-identical
	// whether the user read it or not).
	Affirmed bool
}

// Card is one interview question (DESIGN-mandates.md §5 grammar).
type Card struct {
	ID       string
	Question string
	Blocks   string
	Layer    Layer
	Found    string
	WhyYou   string
	Options  []Option
}

var cardIDRe = regexp.MustCompile(`^Q[0-9]+$`)

// Validate mechanically rejects a malformed card — same spirit as the diet
// governor: a card earns its slot with finding + why-code-can't-decide +
// recommendation, or it is never written at all.
func (c Card) Validate() error {
	if !cardIDRe.MatchString(c.ID) {
		return fmt.Errorf("mandate: card id %q must match Q<n>", c.ID)
	}
	if strings.TrimSpace(c.Question) == "" {
		return fmt.Errorf("mandate: card %s: empty question", c.ID)
	}
	if strings.TrimSpace(c.Blocks) == "" {
		return fmt.Errorf("mandate: card %s: missing blocks: escape-analysis verdict", c.ID)
	}
	if strings.TrimSpace(c.Found) == "" {
		return fmt.Errorf("mandate: card %s: missing Found (evidence)", c.ID)
	}
	if strings.TrimSpace(c.WhyYou) == "" {
		return fmt.Errorf("mandate: card %s: missing Why you (why code can't decide)", c.ID)
	}
	if c.Layer != Layer2 && c.Layer != Layer3 {
		return fmt.Errorf("mandate: card %s: layer must be 2 or 3 (unclassifiable defaults to 3)", c.ID)
	}
	if len(c.Options) < 2 {
		return fmt.Errorf("mandate: card %s: needs at least 2 options", c.ID)
	}
	recommended := 0
	for i, o := range c.Options {
		want := string(rune('A' + i))
		if o.Letter != want {
			return fmt.Errorf("mandate: card %s: option %d letter %q must be sequential %q", c.ID, i, o.Letter, want)
		}
		if strings.TrimSpace(o.Text) == "" {
			return fmt.Errorf("mandate: card %s: option %s has empty text", c.ID, o.Letter)
		}
		if o.Recommended {
			recommended++
		}
	}
	if recommended != 1 {
		return fmt.Errorf("mandate: card %s: exactly one option must be pre-checked recommended, got %d", c.ID, recommended)
	}
	return nil
}

// ValidateRound rejects a round mechanically if it exceeds MaxCardsPerRound
// or contains any malformed card — no partial write of a bad round.
func ValidateRound(cards []Card) error {
	if len(cards) > MaxCardsPerRound {
		return fmt.Errorf("mandate: %d cards exceeds cap of %d per round", len(cards), MaxCardsPerRound)
	}
	seen := map[string]bool{}
	for _, c := range cards {
		if err := c.Validate(); err != nil {
			return err
		}
		if seen[c.ID] {
			return fmt.Errorf("mandate: duplicate card id %s", c.ID)
		}
		seen[c.ID] = true
	}
	return nil
}

// Recommended returns the letter of the pre-checked recommendation.
func (c Card) Recommended() string {
	for _, o := range c.Options {
		if o.Recommended {
			return o.Letter
		}
	}
	return ""
}

// Selected returns the letter of the user's choice. When two boxes are
// checked (the user ticked an option without unchecking the pre-checked
// recommendation), the non-recommended tick wins — only a human adds it.
func (c Card) Selected() string {
	first := ""
	for _, o := range c.Options {
		if !o.Selected {
			continue
		}
		if first == "" {
			first = o.Letter
		}
		if !o.Recommended {
			return o.Letter
		}
	}
	return first
}

// Answered reports whether the user actually answered: a different box than
// the recommendation is checked, or the recommendation carries the explicit
// affirm mark `- [X]` (capital X). The machine's own pre-checked lowercase
// `[x]` never counts — it is byte-identical whether the user read it or not.
func (c Card) Answered() bool {
	sel := c.Selected()
	if sel == "" {
		return false
	}
	if sel != c.Recommended() {
		return true
	}
	for _, o := range c.Options {
		if o.Letter == sel && o.Affirmed {
			return true
		}
	}
	return false
}

// RenderCard renders one card in the §5 grammar, pre-checking the
// recommended option.
func RenderCard(c Card) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### %s — %s ·· blocks: %s\n", c.ID, c.Question, c.Blocks)
	fmt.Fprintf(&b, "**Found:** %s\n", c.Found)
	fmt.Fprintf(&b, "**Why you:** %s\n", c.WhyYou)
	for _, o := range c.Options {
		box := " "
		if o.Selected || o.Recommended {
			box = "x"
		}
		suffix := ""
		if o.Recommended {
			suffix = "  ← recommended"
		}
		fmt.Fprintf(&b, "- [%s] %s. %s%s\n", box, o.Letter, o.Text, suffix)
	}
	return b.String()
}

var (
	cardHeaderRe = regexp.MustCompile(`^### (Q[0-9]+) — (.+?) ·· blocks: (.*)$`)
	optionRe     = regexp.MustCompile(`^- \[([ xX])\] ([A-Z])\. (.*)$`)
)

// ParseCards parses the INTERVIEW section body back into cards, best-effort,
// so a rerun can detect the user's edits (which option is now checked).
func ParseCards(section string) []Card {
	lines := strings.Split(section, "\n")
	var cards []Card
	var cur *Card
	for _, line := range lines {
		if m := cardHeaderRe.FindStringSubmatch(line); m != nil {
			if cur != nil {
				cards = append(cards, *cur)
			}
			cur = &Card{ID: m[1], Question: m[2], Blocks: m[3]}
			continue
		}
		if cur == nil {
			continue
		}
		switch {
		case strings.HasPrefix(line, "**Found:**"):
			cur.Found = strings.TrimSpace(strings.TrimPrefix(line, "**Found:**"))
		case strings.HasPrefix(line, "**Why you:**"):
			cur.WhyYou = strings.TrimSpace(strings.TrimPrefix(line, "**Why you:**"))
		default:
			if m := optionRe.FindStringSubmatch(line); m != nil {
				checked := strings.EqualFold(m[1], "x")
				text := m[3]
				recommended := strings.Contains(text, "← recommended")
				text = strings.TrimSpace(strings.SplitN(text, "← recommended", 2)[0])
				cur.Options = append(cur.Options, Option{
					Letter:      m[2],
					Text:        text,
					Recommended: recommended,
					Selected:    checked,
					Affirmed:    m[1] == "X",
				})
			}
		}
	}
	if cur != nil {
		cards = append(cards, *cur)
	}
	return cards
}
