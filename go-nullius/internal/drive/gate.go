package drive

import (
	"context"
	"fmt"
	"strings"

	"go-nullius/internal/dispatch"
	"go-nullius/internal/mandate"
)

type gateOptionOut struct {
	Text        string `json:"text"`
	Recommended bool   `json:"recommended"`
}

type gateCardOut struct {
	Question string          `json:"question"`
	Blocks   string          `json:"blocks"`
	Layer    int             `json:"layer"`
	Found    string          `json:"found"`
	WhyYou   string          `json:"why_you"`
	Options  []gateOptionOut `json:"options"`
}

type gateModelOut struct {
	Mode     string        `json:"mode"` // FIX | FEATURE | BUILD
	Contract string        `json:"contract"`
	Cards    []gateCardOut `json:"cards"`
}

// GateResult is GATE's ruling: the mechanically-backstopped mode, a drafted
// CONTRACT, and up to mandate.MaxCardsPerRound interview cards.
type GateResult struct {
	Mode     string
	Contract string
	Cards    []mandate.Card
	Notes    []string
}

// runGate is the one frontier dispatch that rules FULL vs BUILD-equivalent
// scope (DESIGN-mandates.md §4). The mechanical backstop overrides the
// model unconditionally: any pre-existing function declaration in the
// mandate's scope files forces FIX, exactly like go-nullius's
// normalizeMode.
//
// A transport failure returns an error and HALTS the drive — the phase
// pointer stays at GATE, so a rerun retries the dispatch. Marching on
// without a contract or interview produced a fantasy hunt when observed
// live; only an unparseable *reply* fail-closes forward (the model was
// reached, the terrain is real, FIX covers the scope).
func (m *Machine) runGate(ctx context.Context, s *mandate.State, recon ReconResult, doc mandate.Doc) (GateResult, error) {
	terrain := terrainBlock(m.cfg.Root, scopeFiles(m.cfg.Root, s.Files), reconTerrainBudget)
	req := dispatch.Request{
		Tier:      dispatch.TierFrontier,
		Objective: "gate",
		Prompt:    gatePrompt(doc.Intent, terrain, recon),
		MaxTokens: 3000,
	}
	resp, err := m.cfg.Adapter.Dispatch(ctx, req)
	s.Receipts = append(s.Receipts, mandate.Receipt{Phase: PhaseGate, Agent: "frontier/gate", Tokens: resp.Tokens, Ms: resp.Ms})
	if err != nil {
		return GateResult{}, fmt.Errorf("GATE dispatch failed: %w", err)
	}

	res := GateResult{Mode: "FIX"} // fail-closed default
	var out gateModelOut
	if perr := extractJSON(resp.Text, &out); perr == nil {
		res.Mode = normalizeMode(out.Mode, s.Files)
		res.Contract = out.Contract
		res.Cards = buildCards(out.Cards, &res.Notes)
	} else {
		res.Notes = append(res.Notes, fmt.Sprintf("GATE: unparseable reply (%v) — fail-closed FIX, no cards drafted", perr))
	}
	if res.Contract == "" {
		res.Contract = doc.Contract
	}
	return res, nil
}

// normalizeMode mechanically overrides the model's claimed mode: any scope
// file with a Go function declaration forces FIX, regardless of what the
// model says (mirrors go-nullius/internal/machine's normalizeMode
// backstop).
func normalizeMode(claimed string, files []string) string {
	for _, f := range files {
		if HasFuncDecl(f) {
			return "FIX"
		}
	}
	switch strings.ToUpper(strings.TrimSpace(claimed)) {
	case "FEATURE":
		return "FEATURE"
	case "BUILD":
		return "BUILD"
	case "FIX":
		return "FIX"
	default:
		return "FIX" // unrecognized -> fail-closed FIX
	}
}

// buildCards converts the model's raw card output into validated
// mandate.Card values, capped at mandate.MaxCardsPerRound and mechanically
// rejecting any malformed card — a bad card earns a note, never a write
// (DESIGN-mandates.md §5).
func buildCards(raw []gateCardOut, notes *[]string) []mandate.Card {
	var cards []mandate.Card
	for i, rc := range raw {
		if len(cards) >= mandate.MaxCardsPerRound {
			*notes = append(*notes, fmt.Sprintf("GATE: dropped card %d — round cap of %d reached", i+1, mandate.MaxCardsPerRound))
			continue
		}
		layer := mandate.Layer(rc.Layer)
		if layer != mandate.Layer2 && layer != mandate.Layer3 {
			layer = mandate.Layer3 // unclassifiable -> layer-3, fail closed
		}
		c := mandate.Card{
			ID:       fmt.Sprintf("Q%d", len(cards)+1),
			Question: rc.Question,
			Blocks:   rc.Blocks,
			Layer:    layer,
			Found:    rc.Found,
			WhyYou:   rc.WhyYou,
		}
		if strings.TrimSpace(c.Blocks) == "" {
			if layer == mandate.Layer3 {
				c.Blocks = "escapes mandate worktree"
			} else {
				c.Blocks = "nothing (revertible in worktree)"
			}
		}
		for j, o := range rc.Options {
			c.Options = append(c.Options, mandate.Option{
				Letter:      string(rune('A' + j)),
				Text:        o.Text,
				Recommended: o.Recommended,
			})
		}
		if err := c.Validate(); err != nil {
			*notes = append(*notes, fmt.Sprintf("GATE: rejected malformed card %d (%v) — never written", i+1, err))
			continue
		}
		cards = append(cards, c)
	}
	return cards
}

func gatePrompt(intent, terrain string, recon ReconResult) string {
	var b strings.Builder
	b.WriteString("You are the GATE ruling for a mandate. Rule FULL (mode=FIX/FEATURE/BUILD), draft a CONTRACT, and propose up to 4 interview cards ONLY where terrain shows two orderings are both implementable and the mandate text doesn't pick one.\n\n")
	fmt.Fprintf(&b, "INTENT:\n%s\n\n", intent)
	b.WriteString("Terrain (file + declared symbols, line-numbered):\n")
	b.WriteString(terrain)
	b.WriteString("\nRECON targets by lens:\n")
	for _, lens := range Lenses {
		fmt.Fprintf(&b, "- %s: %v\n", lens, recon.Targets[lens])
	}
	b.WriteString("\nReply with ONLY a JSON object: {\"mode\":\"FIX|FEATURE|BUILD\",\"contract\":\"...\",\"cards\":[{\"question\":\"...\",\"blocks\":\"...\",\"layer\":2,\"found\":\"...\",\"why_you\":\"...\",\"options\":[{\"text\":\"...\",\"recommended\":true}]}]}\n")
	return b.String()
}
