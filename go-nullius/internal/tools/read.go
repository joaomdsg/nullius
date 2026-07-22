package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"

	"go-nullius/internal/governor"
)

// Read caps are enforced HERE, in the tool — not by a hook that fires
// after the bytes were already absorbed.
const (
	MaxReadLines = 1500
	MaxReadBytes = 64 << 10
)

type ReadTool struct {
	Tr *Tracker
	Ed *governor.Editor // optional: dup-read served from the resident copy
}

func (r *ReadTool) Name() string { return "read" }

func (r *ReadTool) Def() anthropic.ToolUnionParam {
	return anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
		Name: "read",
		Description: anthropic.String(fmt.Sprintf(
			"Read a file with 1-based line numbers. Whole reads are capped at %d lines / %dKB with a truncation marker — use offset+limit for large files; read only the range you need.",
			MaxReadLines, MaxReadBytes>>10)),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"path":   map[string]any{"type": "string", "description": "absolute or workspace-relative file path"},
				"offset": map[string]any{"type": "integer", "description": "1-based first line (default 1)"},
				"limit":  map[string]any{"type": "integer", "description": "max lines to return"},
			},
		},
	}}
}

func (r *ReadTool) Run(_ context.Context, input json.RawMessage) (string, bool) {
	var in struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &in); err != nil || in.Path == "" {
		return "read: invalid input: need {path, offset?, limit?}", true
	}
	fi, err := os.Stat(in.Path)
	if err != nil {
		return "read: " + err.Error(), true
	}
	if r.Ed != nil {
		if last, ok := r.Tr.Last(in.Path); ok && !fi.ModTime().Equal(last) {
			r.Ed.Invalidate(in.Path)
		}
		if r.Ed.IsResident(in.Path, in.Offset, in.Limit) {
			return fmt.Sprintf("[dup-read: %s (offset=%d limit=%d) is already resident in this context from an earlier read — use that copy]",
				in.Path, in.Offset, in.Limit), false
		}
	}
	raw, err := os.ReadFile(in.Path)
	if err != nil {
		return "read: " + err.Error(), true
	}
	r.Tr.Record(in.Path, fi.ModTime())
	if r.Ed != nil {
		r.Ed.NoteRead(in.Path, in.Offset, in.Limit)
	}

	lines := strings.Split(string(raw), "\n")
	start := in.Offset
	if start < 1 {
		start = 1
	}
	if start > len(lines) {
		return fmt.Sprintf("read: offset %d past end of file (%d lines)", start, len(lines)), true
	}
	limit := in.Limit
	if limit <= 0 || limit > MaxReadLines {
		limit = MaxReadLines
	}
	end := start - 1 + limit
	if end > len(lines) {
		end = len(lines)
	}

	var sb strings.Builder
	truncated := false
	for i := start - 1; i < end; i++ {
		line := lines[i]
		fmt.Fprintf(&sb, "%6d\t%s\n", i+1, line)
		if sb.Len() > MaxReadBytes {
			truncated = true
			end = i + 1
			break
		}
	}
	if truncated || end < len(lines) {
		fmt.Fprintf(&sb, "[truncated at line %d of %d — re-read with offset=%d and a limit]\n", end, len(lines), end+1)
	}
	return sb.String(), false
}
