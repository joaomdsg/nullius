package machine

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"go-nullius/internal/caller"
)

// Two structurally-identical helper() calls in separate functions — one lens ("calls-helper"),
// two candidates. The `pad` slice dilutes the named-node count so the 2 hits stay under the
// 0.10 selectivity ceiling (else the lens is dropped as over-broad). helper call lines: fa=6,
// fb=10 (used as the cited scope-source lines).
const pairSrc = `package p

var pad = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}

func fa() {
	helper("scope")
}

func fb() {
	helper("scope")
}

func helper(s string) {}
`

// reconCallsHelper is the FIX-mode fake wiring for the calls-helper lens.
func reconCallsHelper() (orient, gate, recon string) {
	return `{"intent_summary":"x","focus_pkgs":[],"risk_note":"y"}`,
		`{"mode":"FIX","has_inscope_code":true,"justification":"code"}`,
		`{"lenses":[{"id":"calls-helper","template":"call-to","params":{"fn_regex":"helper"},` +
			`"free_scm":"","mechanism":"call","anchor":"call",` +
			`"positive":"package p\nfunc a(){ helper() }","negative":"package p\nfunc a(){ other() }"}]}`
}

func judgeCantTell(p string) (string, bool) {
	if strings.Contains(p, "JUDGE phase") {
		return `{"answer":"CANT_TELL","decisive_line":6,"because":"isolation ambiguous"}`, true
	}
	return "", false
}

func TestPairDiscriminationConfirmsUnsafeClearsSafe(t *testing.T) {
	f := writeGo(t, "a.go", pairSrc)
	o, g, r := reconCallsHelper()
	fc := fakeCaller{orient: o, gate: g, recon: r, custom: func(p string) (string, bool) {
		if s, ok := judgeCantTell(p); ok {
			return s, ok
		}
		if strings.Contains(p, "PAIR-DISCRIMINATION phase") {
			// A (fa, line 6) is UNSAFE with a valid cited origin; B (fb, line 10) is SAFE.
			return `{"a":"UNSAFE","b":"SAFE","a_scope_source_line":6,"b_scope_source_line":10}`, true
		}
		return "", false
	}}

	res := run(t, fc, []string{f}, "audit helper calls for scope leaks")

	var faConf, fbConf bool
	for _, c := range res.Judged {
		if c.Candidate.Fn == "fa" {
			faConf = c.Confirmed
		}
		if c.Candidate.Fn == "fb" {
			fbConf = c.Confirmed
		}
	}
	if !faConf {
		t.Fatalf("UNSAFE site fa must be confirmed by pair-discrimination: %+v", res.Judged)
	}
	if fbConf {
		t.Fatal("SAFE site fb must NOT be confirmed")
	}
}

func TestPairDiscriminationUnsupportedOriginDoesNotConfirm(t *testing.T) {
	// Differentiated verdict but the UNSAFE side cites an out-of-window origin line → nothing
	// is proven, so neither site confirms (fail-closed).
	f := writeGo(t, "a.go", pairSrc)
	o, g, r := reconCallsHelper()
	fc := fakeCaller{orient: o, gate: g, recon: r, custom: func(p string) (string, bool) {
		if s, ok := judgeCantTell(p); ok {
			return s, ok
		}
		if strings.Contains(p, "PAIR-DISCRIMINATION phase") {
			return `{"a":"UNSAFE","b":"SAFE","a_scope_source_line":999,"b_scope_source_line":8}`, true
		}
		return "", false
	}}
	res := run(t, fc, []string{f}, "audit")
	if countConfirmed(res) != 0 {
		t.Fatalf("unsupported origin must confirm nothing, got %d", countConfirmed(res))
	}
}

func TestPairDiscriminationEscalatesToSmart(t *testing.T) {
	f := writeGo(t, "a.go", pairSrc)
	o, g, r := reconCallsHelper()
	var smartPairCalls int
	fc := fakeCaller{
		orient: o, gate: g, recon: r,
		custom: judgeCantTell,
		customTier: func(tier caller.Tier, p string) (string, bool) {
			if !strings.Contains(p, "PAIR-DISCRIMINATION phase") {
				return "", false
			}
			if tier == caller.Fast {
				return `{"a":"SAFE","b":"SAFE","a_scope_source_line":6,"b_scope_source_line":10}`, true // undifferentiated
			}
			smartPairCalls++
			return `{"a":"UNSAFE","b":"SAFE","a_scope_source_line":6,"b_scope_source_line":10}`, true // smart resolves it
		},
	}
	res := run(t, fc, []string{f}, "audit")

	if smartPairCalls != 1 {
		t.Fatalf("undifferentiated fast verdict must escalate to smart exactly once, got %d", smartPairCalls)
	}
	if countConfirmed(res) != 1 {
		t.Fatalf("smart escalation should confirm the UNSAFE site, got %d confirmed", countConfirmed(res))
	}
}

func TestPairEscalationBudgetCapsSmartCalls(t *testing.T) {
	// 8 identical helper() calls → 4 same-lens CANT_TELL pairs. Every pair is undifferentiated
	// at both tiers, so all 4 would escalate — the budget must cap smart calls at 3.
	var b strings.Builder
	b.WriteString("package p\n\nvar pad = []int{")
	for i := 0; i < 120; i++ {
		fmt.Fprintf(&b, "%d,", i) // dilute named-node count under the 0.10 ceiling for 8 hits
	}
	b.WriteString("}\n\n")
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&b, "func f%d() {\n\thelper(\"scope\")\n}\n\n", i)
	}
	b.WriteString("func helper(s string) {}\n")
	f := writeGo(t, "a.go", b.String())

	o, g, r := reconCallsHelper()
	var smartPairCalls int
	fc := fakeCaller{
		orient: o, gate: g, recon: r,
		custom: judgeCantTell,
		customTier: func(tier caller.Tier, p string) (string, bool) {
			if !strings.Contains(p, "PAIR-DISCRIMINATION phase") {
				return "", false
			}
			if tier == caller.Smart {
				smartPairCalls++
			}
			return `{"a":"SAFE","b":"SAFE","a_scope_source_line":0,"b_scope_source_line":0}`, true // never differentiates
		},
	}
	m := New(fc)
	if _, err := m.Run(context.Background(), Mandate{Task: "audit", Files: []string{f}}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if smartPairCalls != maxSmartEscalations {
		t.Fatalf("smart escalations must be capped at %d, got %d", maxSmartEscalations, smartPairCalls)
	}
}
