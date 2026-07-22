package leader

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"

	"go-nullius/internal/governor"
	"go-nullius/internal/ledger"
	verify "go-nullius/internal/rule"
)

type RuleTool struct {
	Ledger *ledger.Ledger
	Ed     *governor.Editor // optional: ruling triggers eviction of the file's tool results
}

func (r *RuleTool) Name() string { return "rule" }

func (r *RuleTool) Def() anthropic.ToolUnionParam {
	return anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
		Name: "rule",
		Description: anthropic.String(
			"Record the disposition of one open checklist item. status=fixed requires detail naming the pinning test. status=refuted requires locator (path or path:line) + evidence (>=20 chars quoted verbatim from the file) — the quote is verified mechanically against disk. status=out-of-mandate requires detail quoting the excluding mandate text. No line left unruled: every checklist item ends here."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"key":      map[string]any{"type": "string", "description": "checklist key (8-char prefix shown by hunt)"},
				"status":   map[string]any{"type": "string", "enum": []string{"fixed", "refuted", "out-of-mandate"}},
				"detail":   map[string]any{"type": "string", "description": "fixed: the pinning test; out-of-mandate: the excluding mandate text"},
				"locator":  map[string]any{"type": "string", "description": "refuted: path or path:line of the protecting mechanism"},
				"evidence": map[string]any{"type": "string", "description": "refuted: the mechanism quoted verbatim (>=20 chars)"},
			},
		},
	}}
}

func (r *RuleTool) Run(_ context.Context, input json.RawMessage) (string, bool) {
	var in struct {
		Key, Status, Detail, Locator, Evidence string
	}
	if err := json.Unmarshal(input, &in); err != nil || in.Key == "" {
		return "rule: invalid input: need {key, status, detail, locator?, evidence?}", true
	}

	key, ruling, err := r.resolve(in.Key)
	if err != nil {
		return "rule: " + err.Error(), true
	}

	switch in.Status {
	case ledger.StatusFixed:
		if strings.TrimSpace(in.Detail) == "" {
			return "rule: fixed requires detail naming the test that pins the changed behavior", true
		}
	case ledger.StatusRefuted:
		if in.Locator == "" || in.Evidence == "" {
			return "rule: refuted requires locator + evidence (the protecting mechanism, quoted)", true
		}
		if err := verify.Verify(in.Locator, in.Evidence); err != nil {
			return "rule: evidence failed mechanical verification: " + err.Error(), true
		}
	case ledger.StatusOutOfMand:
		if strings.TrimSpace(in.Detail) == "" {
			return "rule: out-of-mandate requires detail quoting the excluding mandate text", true
		}
	default:
		return "rule: status must be fixed | refuted | out-of-mandate", true
	}

	r.Ledger.Rule(ruling.Finding, in.Status, in.Detail)
	if err := r.Ledger.Save(); err != nil {
		return "rule: ledger save failed: " + err.Error(), true
	}
	if r.Ed != nil {
		r.Ed.MarkRuled(ruling.Finding.File)
	}
	return fmt.Sprintf("ruled [%s] %s (%s %s); open items remaining: %d",
		key[:8], in.Status, ruling.Finding.File, ruling.Finding.Fn, countOpen(r.Ledger)), false
}

// resolve matches a full key or unique prefix against the ledger.
func (r *RuleTool) resolve(prefix string) (string, ledger.Ruling, error) {
	var hits []string
	for k := range r.Ledger.Rulings {
		if strings.HasPrefix(k, prefix) {
			hits = append(hits, k)
		}
	}
	switch len(hits) {
	case 0:
		return "", ledger.Ruling{}, fmt.Errorf("no checklist item matches key %q", prefix)
	case 1:
		return hits[0], r.Ledger.Rulings[hits[0]], nil
	default:
		return "", ledger.Ruling{}, fmt.Errorf("key %q is ambiguous (%d matches); use more characters", prefix, len(hits))
	}
}

func countOpen(l *ledger.Ledger) int {
	n := 0
	for _, ru := range l.Rulings {
		if ru.Status == ledger.StatusOpen {
			n++
		}
	}
	return n
}

// TailRender renders the open checklist for recitation at the context
// edge — capped, mechanical, re-rendered every turn.
func TailRender(l *ledger.Ledger) func() string {
	return func() string {
		var keys []string
		for k, ru := range l.Rulings {
			if ru.Status == ledger.StatusOpen {
				keys = append(keys, k)
			}
		}
		if len(keys) == 0 {
			return ""
		}
		sortStrings(keys)
		var sb strings.Builder
		fmt.Fprintf(&sb, "OPEN CHECKLIST (%d items — no line left unruled; every item ends in rule():\n", len(keys))
		for i, k := range keys {
			if i == 40 {
				fmt.Fprintf(&sb, "  … and %d more\n", len(keys)-40)
				break
			}
			f := l.Rulings[k].Finding
			fmt.Fprintf(&sb, "  [%s] %s %s %s (%s)\n", k[:8], f.Verdict, f.File, f.Fn, f.Lens)
		}
		return sb.String()
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
