package dispatch

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// PiAdapter shells `pi -p --mode json --no-session <prompt>`
// (DESIGN-mandates.md §6).
type PiAdapter struct {
	Bin string
}

func (a PiAdapter) Name() string { return "pi" }

func (a PiAdapter) Dispatch(ctx context.Context, req Request) (Response, error) {
	bin := a.Bin
	if bin == "" {
		bin = "pi"
	}
	start := time.Now()
	cmd := exec.CommandContext(ctx, bin, "-p", req.Prompt, "--mode", "json", "--no-session")
	out, err := cmd.CombinedOutput()
	ms := time.Since(start).Milliseconds()
	if err != nil {
		return Response{Text: string(out), Ms: ms}, fmt.Errorf("pi adapter: %w: %s", err, firstLine(string(out)))
	}
	return Response{Text: string(out), Ms: ms}, nil
}

// unavailableAdapter registers an adapter name with a clear unavailable
// error rather than silently degrading to another adapter (ASSUMED: `pi` is
// only wired for real when a `pi` binary resolves on PATH at NewPi time).
type unavailableAdapter struct {
	name   string
	reason string
}

func (u unavailableAdapter) Name() string { return u.name }

func (u unavailableAdapter) Dispatch(ctx context.Context, req Request) (Response, error) {
	return Response{}, fmt.Errorf("%s adapter unavailable: %s", u.name, u.reason)
}

// NewPi resolves a `pi` binary on PATH. If none is found it returns an
// adapter that always errors clearly, rather than failing at construction —
// so `nullius drive --adapter=pi` reports why, once, at dispatch time.
func NewPi() Adapter {
	bin, err := exec.LookPath("pi")
	if err != nil {
		return unavailableAdapter{name: "pi", reason: "no `pi` binary resolvable on PATH"}
	}
	return PiAdapter{Bin: bin}
}
