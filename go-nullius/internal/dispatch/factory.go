package dispatch

import (
	"fmt"

	"go-nullius/internal/caller"
)

// Options carries the construction inputs for each adapter kind.
type Options struct {
	Caller    caller.Caller // required for "api"
	ClaudeBin string        // optional override for "claude"
}

// New constructs the named adapter. Unknown names error immediately — a
// typo in --adapter must never silently fall back to a different tier.
func New(name string, opts Options) (Adapter, error) {
	switch name {
	case "api":
		if opts.Caller == nil {
			return nil, fmt.Errorf("dispatch: api adapter requires a caller.Caller")
		}
		return APIAdapter{Caller: opts.Caller}, nil
	case "claude":
		return ClaudeAdapter{Bin: opts.ClaudeBin}, nil
	case "pi":
		return NewPi(), nil
	default:
		return nil, fmt.Errorf("dispatch: unknown adapter %q", name)
	}
}
