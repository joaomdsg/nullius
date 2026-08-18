// Package dispatch normalizes one-shot model sessions the drive phase
// machine shells out to — the frontier tier never has a resident session;
// every call is a curated dispatch that receives a hand-built prompt and
// dies (DESIGN-mandates.md §6).
package dispatch

import "context"

// Tier selects which model tier answers a dispatch.
type Tier int

const (
	// TierScout is the haiku fan-out tier: RECON panel members, HUNT
	// lens-hunters, the AUDIT/CLOSE scouts.
	TierScout Tier = iota
	// TierFrontier is the one-shot curated dispatch tier: GATE, RULE, and
	// bounded micro-rulings. Budgeted at 2 fat dispatches per mandate.
	TierFrontier
)

func (t Tier) String() string {
	switch t {
	case TierScout:
		return "scout"
	case TierFrontier:
		return "frontier"
	default:
		return "tier(?)"
	}
}

// Request is one dispatch: objective, exact inputs, boundaries — workers see
// none of the mandate history except what the driver curates in
// (DESIGN-mandates.md §6).
type Request struct {
	Tier      Tier
	Objective string // short label for receipts, e.g. "recon/serialization"
	Prompt    string
	MaxTokens int
}

// Response is a dispatch's result plus its cost receipt.
type Response struct {
	Text   string
	Tokens int
	Ms     int64
}

// Adapter shells one-shot model sessions and normalizes their output.
type Adapter interface {
	Name() string
	Dispatch(ctx context.Context, req Request) (Response, error)
}
