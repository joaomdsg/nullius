package governor

import (
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

func toolUseMsg(id, name, inputJSON string) anthropic.MessageParam {
	return anthropic.MessageParam{
		Role: anthropic.MessageParamRoleAssistant,
		Content: []anthropic.ContentBlockParamUnion{{
			OfToolUse: &anthropic.ToolUseBlockParam{ID: id, Name: name, Input: map[string]any{"raw": inputJSON}},
		}},
	}
}

func toolResultMsg(id, content string) anthropic.MessageParam {
	return anthropic.MessageParam{
		Role: anthropic.MessageParamRoleUser,
		Content: []anthropic.ContentBlockParamUnion{{
			OfToolResult: &anthropic.ToolResultBlockParam{
				ToolUseID: id,
				Content: []anthropic.ToolResultBlockParamContentUnion{{
					OfText: &anthropic.TextBlockParam{Text: content},
				}},
			},
		}},
	}
}

func textOfResult(m anthropic.MessageParam) string {
	return m.Content[0].OfToolResult.Content[0].OfText.Text
}

func TestSweepEvictsRuledFileResults(t *testing.T) {
	big := strings.Repeat("x", 500)
	msgs := []anthropic.MessageParam{
		toolUseMsg("tu_1", "read", `{"path":"pkg/a.go"}`),
		toolResultMsg("tu_1", big),
		toolUseMsg("tu_2", "read", `{"path":"pkg/b.go"}`),
		toolResultMsg("tu_2", big),
		toolUseMsg("tu_3", "bash", `{"command":"grep -n foo pkg/a.go"}`),
		toolResultMsg("tu_3", big),
	}

	e := NewEditor()
	if n := e.Sweep(msgs); n != 0 {
		t.Fatalf("nothing ruled yet, swept %d", n)
	}

	e.MarkRuled("pkg/a.go")
	if n := e.Sweep(msgs); n != 2 {
		t.Fatalf("want 2 evictions (read + bash touching a.go), got %d", n)
	}
	if got := textOfResult(msgs[1]); !strings.Contains(got, "[evicted after ruling on pkg/a.go") {
		t.Errorf("read result not evicted: %q", got)
	}
	if got := textOfResult(msgs[5]); !strings.Contains(got, "[evicted after ruling") {
		t.Errorf("bash result touching the ruled file not evicted: %q", got)
	}
	if got := textOfResult(msgs[3]); got != big {
		t.Error("unruled file's result must stay resident")
	}

	// Idempotent: a second sweep finds nothing new.
	if n := e.Sweep(msgs); n != 0 {
		t.Errorf("re-sweep evicted %d, want 0", n)
	}
}

func TestSweepSkipsTinyResults(t *testing.T) {
	msgs := []anthropic.MessageParam{
		toolUseMsg("tu_1", "read", `{"path":"small.go"}`),
		toolResultMsg("tu_1", "short"),
	}
	e := NewEditor()
	e.MarkRuled("small.go")
	if n := e.Sweep(msgs); n != 0 {
		t.Errorf("tiny results are not worth evicting, got %d", n)
	}
}

func TestResidencyLifecycle(t *testing.T) {
	e := NewEditor()
	if e.IsResident("f.go", 1, 100) {
		t.Fatal("nothing read yet")
	}
	e.NoteRead("f.go", 1, 100)
	if !e.IsResident("f.go", 1, 100) {
		t.Fatal("identical read must be resident")
	}
	if e.IsResident("f.go", 50, 100) {
		t.Fatal("different range is not the same residency")
	}
	// Ruling evicts → residency drops (content left context).
	e.MarkRuled("f.go")
	if e.IsResident("f.go", 1, 100) {
		t.Error("evicted content must not be served as resident")
	}
	// File changed on disk → residency drops.
	e.NoteRead("g.go", 1, 100)
	e.Invalidate("g.go")
	if e.IsResident("g.go", 1, 100) {
		t.Error("invalidated file must not be served as resident")
	}
}

// TestEditorReset pins post-close compaction semantics: residency must
// not survive a reset — the transcript it described is gone.
func TestEditorReset(t *testing.T) {
	e := NewEditor()
	e.NoteRead("/a/b.go", 0, 0)
	e.MarkRuled("/c/d.go")
	e.Reset()
	if e.IsResident("/a/b.go", 0, 0) {
		t.Error("read still resident after Reset — a dup-read would be served a pointer to evicted content")
	}
	if n := e.Sweep(nil); n != 0 {
		t.Errorf("Sweep after Reset evicted %d, want 0 (ruled set must be cleared)", n)
	}
}
