package leader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The close suite must match the project's actual toolchain — hardcoded Go
// commands against a Rust/Node/Python tree is a close that verifies
// nothing and costs the leader a correction turn.
func TestDetectCloseCommands(t *testing.T) {
	cases := []struct {
		manifest string
		want     string // a command fragment the suite must contain
	}{
		{"go.mod", "go test"},
		{"Cargo.toml", "cargo test"},
		{"package.json", "npm test"},
		{"pyproject.toml", "pytest"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, c.manifest), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.Join(DetectCloseCommands(dir), " | "), c.want) {
			t.Errorf("%s: suite %v missing %q", c.manifest, DetectCloseCommands(dir), c.want)
		}
	}
	// Unknown terrain falls back to Go defaults, never an empty suite.
	if cmds := DetectCloseCommands(t.TempDir()); len(cmds) == 0 {
		t.Error("unknown project type must fall back, not return empty")
	}
}
