// Package rule implements the mechanical quote-verify: a REFUTED or
// out-of-mandate ruling must carry evidence that exists verbatim on disk.
// Nullius in verba — testimony is checked against the source, not trusted.
package rule

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const minEvidence = 20

var locatorRe = regexp.MustCompile(`^(.+?):(\d+)$`)

// Verify checks that evidence appears verbatim in the file named by locator
// ("path" or "path:line"). When a line number is given, the evidence must
// appear within ±20 lines of it (quotes drift; anchors shouldn't lie by much).
// Returns nil on success and an honest, specific error otherwise.
func Verify(locator, evidence string) error {
	evidence = strings.TrimSpace(evidence)
	if len(evidence) < minEvidence {
		return fmt.Errorf("evidence too short (%d chars, need >= %d): a ruling needs a real quoted mechanism, not a fragment", len(evidence), minEvidence)
	}
	path, line := locator, 0
	if m := locatorRe.FindStringSubmatch(locator); m != nil {
		if _, err := os.Stat(locator); err != nil { // "a:1" could be a real filename
			path = m[1]
			line, _ = strconv.Atoi(m[2])
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read %s: %v — the locator must name an existing file", path, err)
	}
	content := string(raw)
	if !strings.Contains(normalize(content), normalize(evidence)) {
		return fmt.Errorf("evidence not found in %s: the quoted mechanism does not exist on disk (whitespace-normalized search)", path)
	}
	if line > 0 {
		lines := strings.Split(content, "\n")
		lo, hi := max(0, line-1-20), min(len(lines), line+20)
		window := normalize(strings.Join(lines[lo:hi], "\n"))
		if !strings.Contains(window, normalize(evidence)) {
			return fmt.Errorf("evidence exists in %s but not near line %d (checked ±20 lines): fix the anchor", path, line)
		}
	}
	return nil
}

var wsRe = regexp.MustCompile(`\s+`)

func normalize(s string) string { return wsRe.ReplaceAllString(strings.TrimSpace(s), " ") }
