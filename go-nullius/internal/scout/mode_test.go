package scout

import (
	"context"
	"strings"
	"testing"
)

// The drain reuses the scout spawn machinery for the craftsman, differing
// only in the child mode flag. Mode must select the subprocess flag; empty
// stays the read-only scout so existing callers are unchanged.
func TestModeSelectsChildFlag(t *testing.T) {
	dir := t.TempDir()
	// The fake child records its own argv so we can assert the mode flag.
	bin := fakeBin(t, dir, `echo "ARGS: $@"`)

	craft := &Tool{Bin: bin, Dir: dir, Mode: "craftsman", Model: "fast", NulliusDir: dir}
	out, isErr := craft.Run(context.Background(), input(t, "fix it"))
	if isErr || !strings.Contains(out, "--craftsman") {
		t.Fatalf("craftsman mode must spawn --craftsman, got %q", out)
	}

	scout := &Tool{Bin: bin, Dir: dir, Model: "fast", NulliusDir: dir}
	out, isErr = scout.Run(context.Background(), input(t, "read it"))
	if isErr || !strings.Contains(out, "--scout") {
		t.Fatalf("default mode must stay --scout, got %q", out)
	}
}
