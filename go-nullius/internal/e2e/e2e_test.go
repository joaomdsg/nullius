// Package e2e runs the seeded-defect acceptance: a REAL haiku hunter
// (spawned through the real go-nullius binary in scout mode) hunts the
// fixture, and the checklist must catch the seeded defects.
// Gated: NULLIUS_LIVE_E2E=1 (spends real tokens).
package e2e

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go-nullius/internal/leader"
	"go-nullius/internal/ledger"
	"go-nullius/internal/scout"
)

func TestSeededDefectsCaught(t *testing.T) {
	if os.Getenv("NULLIUS_LIVE_E2E") != "1" {
		t.Skip("set NULLIUS_LIVE_E2E=1 to run the live seeded-defect acceptance")
	}

	work := t.TempDir()
	bin := filepath.Join(work, "go-nullius")
	build := exec.Command("go", "build", "-o", bin, "go-nullius/cmd/go-nullius")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building binary: %v\n%s", err, out)
	}

	fixture, err := filepath.Abs("testdata/seeded")
	if err != nil {
		t.Fatal(err)
	}

	led, err := ledger.Load(filepath.Join(work, "hunt.json"))
	if err != nil {
		t.Fatal(err)
	}
	sct := &scout.Tool{Bin: bin, Dir: fixture, Model: "haiku", NulliusDir: filepath.Join(work, ".nullius")}
	hunt := &leader.HuntTool{Ledger: led, Scout: sct, Dir: fixture}

	caught := map[string]bool{}
	for _, tc := range []struct{ lens, fn string }{
		{"lost-updates", "Inc"},
		{"fault-survival", "Flush"},
	} {
		raw, _ := json.Marshal(map[string]any{
			"lens":    tc.lens,
			"targets": []string{filepath.Join(fixture, "seeded.go") + ":" + tc.fn},
		})
		out, isErr := hunt.Run(context.Background(), raw)
		t.Logf("hunt %s:\n%s", tc.lens, out)
		if isErr {
			t.Fatalf("hunt %s failed: %s", tc.lens, out)
		}
		// cc-nullius polarity: a seeded DEFECT surfaces as ABSENT (the
		// protective mechanism is missing).
		for _, ru := range led.Rulings {
			if ru.Finding.Lens == tc.lens && ru.Finding.Verdict == "ABSENT" &&
				strings.Contains(ru.Finding.Fn+ru.Finding.Detail+ru.Finding.SnippetHead, tc.fn) {
				caught[tc.lens] = true
			}
		}
	}
	for _, lens := range []string{"lost-updates", "fault-survival"} {
		if !caught[lens] {
			t.Errorf("seeded %s defect NOT caught as ABSENT — checklist out failed its acceptance", lens)
		}
	}
}
