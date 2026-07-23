package scout

import "testing"

// modelFor is the dispatch-tier router: bulk scouts run on the fast tier,
// but a dispatch may escalate to smart. Unknown/empty tier falls back to
// the default (fast) model, and "smart" falls back to fast when no smart
// tier is configured — a dispatch must never fail to resolve a model.
func TestModelForTier(t *testing.T) {
	s := &Tool{Model: "fast", SmartModel: "smart"}
	cases := map[string]string{
		"":      "fast",
		"fast":  "fast",
		"smart": "smart",
		"bogus": "fast",
	}
	for tier, want := range cases {
		if got := s.modelFor(tier); got != want {
			t.Errorf("modelFor(%q) = %q, want %q", tier, got, want)
		}
	}

	// No smart tier configured → smart escalation degrades to fast.
	noSmart := &Tool{Model: "fast"}
	if got := noSmart.modelFor("smart"); got != "fast" {
		t.Errorf("modelFor(smart) with no SmartModel = %q, want fast", got)
	}
}
