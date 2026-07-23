package agent

import (
	"bytes"
	"strings"
	"testing"
)

// trace() must be silent when the gate is off and emit a "· "-prefixed
// line when on. Off-by-default is what keeps the trace from polluting
// headless report output; the gate is the whole contract.
func TestTraceGating(t *testing.T) {
	var buf bytes.Buffer
	oldW, oldOn := traceW, traceOn
	defer func() { traceW, traceOn = oldW, oldOn }()
	traceW = &buf

	traceOn = false
	trace("hidden %d", 1)
	if buf.Len() != 0 {
		t.Fatalf("gate off: wrote %q, want nothing", buf.String())
	}

	traceOn = true
	trace("shown %d", 7)
	got := buf.String()
	if !strings.HasPrefix(got, "· ") || !strings.Contains(got, "shown 7") {
		t.Fatalf("gate on: got %q, want a '· '-prefixed 'shown 7' line", got)
	}
}

// Tool-call arguments are traced as pretty-printed JSON so a watcher can
// read a wide dispatch or a ranged read at a glance. Non-JSON input falls
// back to a flattened one-liner rather than mangling it.
func TestPrettyTraceIndentsJSON(t *testing.T) {
	out := prettyTrace(`{"path":"a.go","limit":20}`)
	if !strings.Contains(out, "\n") || !strings.Contains(out, `"path"`) {
		t.Fatalf("want indented multiline JSON, got %q", out)
	}
	if got := prettyTrace("not json\nsecond"); got != "not json second" {
		t.Fatalf("non-JSON fallback = %q, want flattened one-liner", got)
	}
}
