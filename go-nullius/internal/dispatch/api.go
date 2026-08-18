package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go-nullius/internal/caller"
)

// APIAdapter wraps the existing internal/caller engine — grammar-500
// unconstrained retry, backoff, and the two-tier (Fast/Smart) endpoint map —
// for schema-constrained rulings (DESIGN-mandates.md §6 `api` adapter).
type APIAdapter struct {
	Caller caller.Caller
}

func (a APIAdapter) Name() string { return "api" }

// Dispatch decodes the model's reply as JSON into a generic map and
// re-marshals it to Response.Text, so callers parse the same JSON shape
// regardless of which adapter answered. TierFrontier maps to caller.Smart;
// TierScout maps to caller.Fast.
func (a APIAdapter) Dispatch(ctx context.Context, req Request) (Response, error) {
	tier := caller.Fast
	if req.Tier == TierFrontier {
		tier = caller.Smart
	}
	var tokens int
	opts := []caller.AskOption{caller.WithUsage(&tokens)}
	if req.MaxTokens > 0 {
		opts = append(opts, caller.WithMaxTokens(req.MaxTokens))
	}
	start := time.Now()
	var out map[string]any
	err := a.Caller.Ask(ctx, tier, req.Prompt, "", &out, opts...)
	ms := time.Since(start).Milliseconds()
	if err != nil {
		return Response{Tokens: tokens, Ms: ms}, fmt.Errorf("api adapter: %w", err)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return Response{Tokens: tokens, Ms: ms}, fmt.Errorf("api adapter: re-marshal reply: %w", err)
	}
	return Response{Text: string(b), Tokens: tokens, Ms: ms}, nil
}
