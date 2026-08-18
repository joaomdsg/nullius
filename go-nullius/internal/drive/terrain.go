package drive

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// A toolless one-shot adapter (api) cannot read the repo, so the DRIVER
// extracts the terrain mechanically and inlines it into every RECON/GATE
// prompt. Without this, scouts confabulate targets by construction
// (observed: qwen3.6 invented C files in a Go repo when handed bare
// filenames).

// srcExts bounds terrain extraction to source files a lens can bite on.
var srcExts = map[string]bool{
	".go": true, ".js": true, ".mjs": true, ".ts": true, ".tsx": true,
	".jsx": true, ".py": true, ".rb": true, ".rs": true, ".java": true,
	".c": true, ".h": true, ".cc": true, ".cpp": true,
}

var declRe = regexp.MustCompile(`^(func |type |class |def |function |export |pub |impl |var |const )`)

// scopeFiles resolves the mandate's scope: the explicit -files list, or
// every git-tracked source file when none was given.
func scopeFiles(root string, files []string) []string {
	if len(files) > 0 {
		return files
	}
	out, err := exec.Command("git", "-C", root, "ls-files").Output()
	if err != nil {
		return nil
	}
	var fs []string
	for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if srcExts[filepath.Ext(f)] {
			fs = append(fs, f)
		}
	}
	return fs
}

// terrainBlock renders a mechanical file+declaration map of the scope for
// inlining into one-shot prompts. EVERY scope file is named no matter the
// budget — only declarations degrade past it, and the truncation is named
// in the block (measured: dropping tail files whole hid 4 of 6 injected
// defects from RECON entirely; a scout cannot target a file it never saw).
func terrainBlock(root string, files []string, maxBytes int) string {
	var b strings.Builder
	declless := 0
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "== %s\n", f)
		if b.Len() > maxBytes {
			declless++
			continue
		}
		for n, line := range strings.Split(string(data), "\n") {
			if declRe.MatchString(line) {
				if b.Len() > maxBytes {
					declless++
					break
				}
				if len(line) > 160 {
					line = line[:160]
				}
				fmt.Fprintf(&b, "  %d: %s\n", n+1, line)
			}
		}
	}
	if declless > 0 {
		fmt.Fprintf(&b, "… declarations omitted for the last %d file(s) above (terrain budget) — they are still valid targets\n", declless)
	}
	return b.String()
}

// splitTarget splits a "path:symbol" recon target.
func splitTarget(t string) (path, symbol string) {
	i := strings.IndexByte(t, ':')
	if i < 0 {
		return t, ""
	}
	return t[:i], t[i+1:]
}

// targetExists reports whether a recon target names a file actually in the
// repo — the mechanical floor under scout testimony.
func targetExists(root, target string) bool {
	path, _ := splitTarget(target)
	if path == "" {
		return false
	}
	fi, err := os.Stat(filepath.Join(root, path))
	return err == nil && !fi.IsDir()
}

// symbolLine returns the first line on which a target's symbol appears, or 0.
func symbolLine(root, file, symbol string) int {
	if symbol == "" {
		return 0
	}
	data, err := os.ReadFile(filepath.Join(root, file))
	if err != nil {
		return 0
	}
	for n, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, symbol) {
			return n + 1
		}
	}
	return 0
}
