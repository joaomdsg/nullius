package mandate

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Doc is the parsed mandate.md: a driver-owned STATUS banner (line 1) plus
// four human sections (DESIGN-mandates.md §3). INTENT/CONTRACT are user
// prose; INTERVIEW/RATIFICATION are machine-rewritten at each phase
// transition — recitation is load-bearing, not bookkeeping.
type Doc struct {
	Status       string
	Intent       string
	Contract     string
	Interview    string
	Ratification string
}

const sectionOrder = "INTENT,CONTRACT,INTERVIEW,RATIFICATION"

var sectionHeaderRe = regexp.MustCompile(`(?m)^## (INTENT|CONTRACT|INTERVIEW|RATIFICATION)\s*$`)

// ParseDoc parses mandate.md content. Unknown/missing sections are left
// empty rather than erroring — a hand-edited file with a section deleted is
// recoverable, not fatal.
func ParseDoc(content string) Doc {
	var d Doc
	lines := strings.SplitN(content, "\n", 2)
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "> STATUS:") {
		d.Status = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[0]), "> STATUS:"))
	}

	locs := sectionHeaderRe.FindAllStringSubmatchIndex(content, -1)
	for i, loc := range locs {
		name := content[loc[2]:loc[3]]
		bodyStart := loc[1]
		bodyEnd := len(content)
		if i+1 < len(locs) {
			bodyEnd = locs[i+1][0]
		}
		body := strings.Trim(content[bodyStart:bodyEnd], "\n")
		switch name {
		case "INTENT":
			d.Intent = body
		case "CONTRACT":
			d.Contract = body
		case "INTERVIEW":
			d.Interview = body
		case "RATIFICATION":
			d.Ratification = body
		}
	}
	return d
}

// Render reassembles mandate.md: banner line, then the four sections in
// fixed order.
func (d Doc) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "> STATUS: %s\n\n", d.Status)
	fmt.Fprintf(&b, "## INTENT\n\n%s\n\n", strings.TrimSpace(d.Intent))
	fmt.Fprintf(&b, "## CONTRACT\n\n%s\n\n", strings.TrimSpace(d.Contract))
	fmt.Fprintf(&b, "## INTERVIEW\n\n%s\n\n", strings.TrimSpace(d.Interview))
	fmt.Fprintf(&b, "## RATIFICATION\n\n%s\n", strings.TrimSpace(d.Ratification))
	return b.String()
}

// ReadDoc reads and parses mandate.md for slug under root.
func ReadDoc(root, slug string) (Doc, error) {
	b, err := os.ReadFile(Paths(root, slug).MandateMD)
	if err != nil {
		return Doc{}, fmt.Errorf("mandate: read mandate.md: %w", err)
	}
	return ParseDoc(string(b)), nil
}

// WriteDoc renders and writes mandate.md for slug under root.
func WriteDoc(root, slug string, d Doc) error {
	return os.WriteFile(Paths(root, slug).MandateMD, []byte(d.Render()), 0o644)
}
