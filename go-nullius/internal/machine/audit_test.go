package machine

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// After fixing f's unreachable code, this build introduces a FRESH defect in a new function g:
// a helper() call flagged by the derived calls-helper lens. Crucially it is vet-clean and
// builds — so drainOne's verification accepts it and the re-entry round, not a drain retry, is
// what catches g. (An unreachable-code fresh defect would fail `go vet` inside drainOne and be
// reverted, masking the re-entry path.)
const reentryAfter1 = `package sample

func f() int {
	x := 1
	return x
}

func g() {
	helper()
}

func helper() {}
`

const reentryAfter2 = `package sample

func f() int {
	x := 1
	return x
}

func g() {
}

func helper() {}
`

var siteLineRE = regexp.MustCompile(`SITE: \S+:(\d+) `)

// reentryFake judges every flagged site a DEFECT, echoing the site's own line as the decisive
// line so the validity gate always passes regardless of where the site sits. Recon derives the
// calls-helper lens (so the fresh g.helper() call is caught); baseline stmt-after-return catches
// f's original unreachable code.
func reentryFake() fakeCaller {
	o, g, r := reconCallsHelper()
	return fakeCaller{
		orient: o, gate: g, recon: r,
		custom: func(p string) (string, bool) {
			switch {
			case strings.Contains(p, "JUDGE phase"):
				line := "1"
				if m := siteLineRE.FindStringSubmatch(p); m != nil {
					line = m[1]
				}
				return `{"answer":"DEFECT","decisive_line":` + line + `,"because":"defect at site"}`, true
			case strings.Contains(p, "REFUTE phase"):
				return `{"stands":true,"refuting_line":null}`, true
			case strings.Contains(p, "PLAN phase"):
				return `{"target":"sample.go","intent":"remove the flagged code","test_name":"TestX","test_sketch":"x","blast_radius":"fn"}`, true
			}
			return "", false
		},
	}
}

func TestAuditReentryDrainsFreshDefect(t *testing.T) {
	dir := gitRepo(t, map[string]string{"go.mod": drainModFiles, "sample.go": drainDirtySrc})
	file := filepath.Join(dir, "sample.go")

	calls := 0
	m := New(reentryFake())
	m.Craftsman = fakeWriter{fn: func(d string) error {
		calls++
		if calls == 1 {
			return osWrite(file, reentryAfter1) // fixes f, introduces g's dead code
		}
		return osWrite(file, reentryAfter2) // re-entry round fixes g
	}}

	res, err := m.Run(context.Background(), Mandate{Task: "review", Files: []string{file}, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if calls < 2 {
		t.Fatalf("re-entry must drain the fresh defect: craftsman called %d time(s), want 2", calls)
	}
	if countDone(res.Drained) != 2 {
		t.Fatalf("want 2 DONE drains (original + re-entry), got %d: %+v", countDone(res.Drained), res.Drained)
	}
	got, _ := osRead(file)
	if got != reentryAfter2 {
		t.Fatalf("final file not fully fixed:\n%s", got)
	}
	var sawReentry bool
	for _, tr := range res.Trace {
		if tr.Phase == PhaseAudit && strings.Contains(tr.Msg, "fresh defect(s) confirmed") {
			sawReentry = true
		}
	}
	if !sawReentry {
		t.Errorf("expected an audit re-entry that confirmed a fresh defect; trace: %v", res.Trace)
	}
}

func TestAuditReentryTerminatesOnPersistentResidual(t *testing.T) {
	// The craftsman "fixes" nothing meaningful: it rewrites the file but the dead code in f
	// persists (same target). The residual is at an already-ruled target, so no fresh target
	// appears and the loop returns without re-draining — no oscillation.
	dir := gitRepo(t, map[string]string{"go.mod": drainModFiles, "sample.go": drainDirtySrc})
	file := filepath.Join(dir, "sample.go")

	m := New(reentryFake())
	m.Craftsman = fakeWriter{fn: func(d string) error {
		// touch the file (non-empty diff so the first drain is DONE) but keep f's dead code.
		return osWrite(file, drainDirtySrc+"\n// touched\n")
	}}
	res, err := m.Run(context.Background(), Mandate{Task: "review", Files: []string{file}, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	var sawResidualRisk bool
	for _, tr := range res.Trace {
		if tr.Phase == PhaseAudit && strings.Contains(tr.Msg, "fix incomplete, RISK") {
			sawResidualRisk = true
		}
	}
	if !sawResidualRisk {
		t.Errorf("a persistent residual at a ruled target must be surfaced as a RISK; trace: %v", res.Trace)
	}
}
