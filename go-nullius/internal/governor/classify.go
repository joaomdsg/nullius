// Package governor implements the pre-dispatch gate: it classifies leader
// tool calls BEFORE tokens are spent and routes context-fattening work to
// throwaway scout contexts. Denial is a tool result teaching the route,
// never a post-generation rejection (the measured double-bill flaw in the
// hook-based ports).
package governor

import (
	"regexp"
	"strings"
)

// Verdict is the gate's ruling on a proposed action.
type Verdict struct {
	Allow  bool
	Route  string // "" when allowed; otherwise the tool to use instead
	Reason string
}

var heavyRe = regexp.MustCompile(`(^|[\s;&|(])(go\s+(test|build|vet)|npm\s+(test|run)|npx\s+\S+|pytest|cargo\s+(test|build|check)|make|mvn|gradle|tsc|eslint|ruff|golangci-lint|dotnet\s+test)\b`)

// wideSearchRe matches recursive/unbounded search commands; whether they are
// actually wide depends on the target check in IsWideSearch (Go regexp has
// no lookahead).
var wideSearchRe = regexp.MustCompile(`(^|[\s;&|(])(grep\s+(-\w*\s+)*-\w*r|rg\s)`)

// boundedRe: the FINAL pipe segment caps output. More pipes after the cap
// segment un-bound it again.
var boundedRe = regexp.MustCompile(`\|\s*(tail|head|wc|grep\s+-c)\b[^|]*$`)

// IsHeavyCmd reports whether cmd runs a build/test/lint pipeline whose bulk
// output belongs in a scout context, not the leader's.
func IsHeavyCmd(cmd string) bool {
	return heavyRe.MatchString(cmd)
}

// IsBounded reports whether the command's final pipe segment caps its output.
func IsBounded(cmd string) bool {
	return boundedRe.MatchString(cmd)
}

// IsWideSearch reports whether cmd is a repo-wide search with no concrete
// file target (an unbounded absorption).
func IsWideSearch(cmd string) bool {
	if !wideSearchRe.MatchString(cmd) {
		return false
	}
	// A concrete target (a path with "/" or an extension like ".go") in the
	// trailing arguments bounds the search enough to allow it.
	fields := strings.Fields(cmd)
	for i := len(fields) - 1; i > 0; i-- {
		f := strings.Trim(fields[i], `"'`)
		if strings.HasPrefix(f, "-") || f == "|" {
			continue
		}
		if (strings.Contains(f, "/") || extRe.MatchString(f)) && !strings.ContainsAny(f, "*?[") {
			return false // concrete file target: bounded enough
		}
	}
	return true
}

var extRe = regexp.MustCompile(`\.\w{1,6}$`)

// Gate classifies a bash command. Heavy commands and wide searches route to
// the scout; everything else passes (bounding happens in the tool's result
// builder, never by rewriting the command).
func Gate(cmd string) Verdict {
	if IsHeavyCmd(cmd) && !IsBounded(cmd) {
		return Verdict{Route: "scout", Reason: "heavy command: run it in a scout dispatch (mode rerun) and absorb only the capped anchored report"}
	}
	if IsWideSearch(cmd) {
		return Verdict{Route: "scout", Reason: "wide search: dispatch a scout with the question instead of absorbing raw matches"}
	}
	return Verdict{Allow: true}
}
