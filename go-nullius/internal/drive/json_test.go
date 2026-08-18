package drive

import "testing"

type sampleOut struct {
	Mode string `json:"mode"`
}

func TestExtractJSONBareObject(t *testing.T) {
	var out sampleOut
	if err := extractJSON(`{"mode":"FIX"}`, &out); err != nil {
		t.Fatalf("extractJSON: %v", err)
	}
	if out.Mode != "FIX" {
		t.Fatalf("got %+v", out)
	}
}

func TestExtractJSONFencedAndProseWrapped(t *testing.T) {
	var out sampleOut
	text := "Here is my ruling:\n```json\n{\"mode\": \"FIX\"}\n```\nHope that helps."
	if err := extractJSON(text, &out); err != nil {
		t.Fatalf("extractJSON: %v", err)
	}
	if out.Mode != "FIX" {
		t.Fatalf("got %+v", out)
	}
}

func TestExtractJSONNestedBraces(t *testing.T) {
	var out map[string]any
	text := `noise {"a": {"b": 1}, "c": "}"} trailing`
	if err := extractJSON(text, &out); err != nil {
		t.Fatalf("extractJSON: %v", err)
	}
	if out["a"] == nil {
		t.Fatalf("got %+v", out)
	}
}

func TestExtractJSONNoObjectErrors(t *testing.T) {
	var out sampleOut
	if err := extractJSON("no json here", &out); err == nil {
		t.Fatal("expected error for reply with no JSON object")
	}
}

func TestExtractJSONUnterminatedErrors(t *testing.T) {
	var out sampleOut
	if err := extractJSON(`{"mode": "FIX"`, &out); err == nil {
		t.Fatal("expected error for unterminated JSON object")
	}
}
