package mandate

import "path/filepath"

// FilePaths is the file protocol for one mandate (DESIGN-mandates.md §3):
// mandate.md is the only file a user edits; everything else is machine-owned.
type FilePaths struct {
	Root      string
	Slug      string
	Dir       string
	MandateMD string
	StateJSON string
	Checklist string
	Ledger    string
	PlansDir  string
	CloseMD   string
	ReportMD  string
}

// Paths returns the file protocol rooted at <root>/.nullius/mandates/<slug>.
func Paths(root, slug string) FilePaths {
	dir := filepath.Join(root, ".nullius", "mandates", slug)
	return FilePaths{
		Root:      root,
		Slug:      slug,
		Dir:       dir,
		MandateMD: filepath.Join(dir, "mandate.md"),
		StateJSON: filepath.Join(dir, "state.json"),
		Checklist: filepath.Join(dir, "checklist.md"),
		Ledger:    filepath.Join(dir, "ledger.md"),
		PlansDir:  filepath.Join(dir, "plans"),
		CloseMD:   filepath.Join(dir, "close.md"),
		ReportMD:  filepath.Join(dir, "report.md"),
	}
}

// PlanPath names the NN-<target>.md plan file for the nth (1-based) plan.
func (p FilePaths) PlanPath(n int, target string) string {
	return filepath.Join(p.PlansDir, planFileName(n, target))
}

func planFileName(n int, target string) string {
	return zeroPad2(n) + "-" + sanitizeTarget(target) + ".md"
}

func zeroPad2(n int) string {
	if n < 10 {
		return "0" + itoa(n)
	}
	return itoa(n)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func sanitizeTarget(target string) string {
	base := filepath.Base(target)
	out := make([]rune, 0, len(base))
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			out = append(out, r)
		case r == '.' || r == '-' || r == '_':
			out = append(out, '-')
		default:
			out = append(out, '-')
		}
	}
	if len(out) == 0 {
		return "target"
	}
	return string(out)
}
