package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

type EditTool struct{ Tr *Tracker }

func (e *EditTool) Name() string { return "edit" }

func (e *EditTool) Def() anthropic.ToolUnionParam {
	return anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
		Name: "edit",
		Description: anthropic.String(
			"Replace old_string with new_string in a file. The file must have been read this session and be unchanged since (staleness is checked by mtime). old_string must match exactly once unless replace_all. To create a new file, pass old_string=\"\" on a path that does not exist."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"path":        map[string]any{"type": "string"},
				"old_string":  map[string]any{"type": "string"},
				"new_string":  map[string]any{"type": "string"},
				"replace_all": map[string]any{"type": "boolean"},
			},
		},
	}}
}

func (e *EditTool) Run(_ context.Context, input json.RawMessage) (string, bool) {
	var in struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := json.Unmarshal(input, &in); err != nil || in.Path == "" {
		return "edit: invalid input: need {path, old_string, new_string, replace_all?}", true
	}

	// Creation path: empty old_string on a non-existent file.
	if in.OldString == "" {
		if _, err := os.Stat(in.Path); err == nil {
			return "edit: old_string is empty but " + in.Path + " exists; provide the text to replace", true
		}
		if err := writeTracked(e.Tr, in.Path, in.NewString); err != nil {
			return "edit: " + err.Error(), true
		}
		return fmt.Sprintf("created %s (%d bytes)", in.Path, len(in.NewString)), false
	}

	if err := e.Tr.Fresh(in.Path); err != nil {
		return "edit: " + err.Error(), true
	}
	raw, err := os.ReadFile(in.Path)
	if err != nil {
		return "edit: " + err.Error(), true
	}
	content := string(raw)
	n := strings.Count(content, in.OldString)
	switch {
	case n == 0:
		return "edit: old_string not found in " + in.Path, true
	case n > 1 && !in.ReplaceAll:
		return fmt.Sprintf("edit: old_string matches %d times in %s; enlarge it to be unique or set replace_all", n, in.Path), true
	}
	if in.ReplaceAll {
		content = strings.ReplaceAll(content, in.OldString, in.NewString)
	} else {
		content = strings.Replace(content, in.OldString, in.NewString, 1)
	}
	if err := writeTracked(e.Tr, in.Path, content); err != nil {
		return "edit: " + err.Error(), true
	}
	return fmt.Sprintf("edited %s (%d replacement(s))", in.Path, max(n*b2i(in.ReplaceAll), 1)), false
}

func writeTracked(tr *Tracker, path, content string) error {
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	tr.Record(path, fi.ModTime())
	return nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
