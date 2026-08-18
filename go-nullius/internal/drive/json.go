package drive

import (
	"encoding/json"
	"fmt"
	"strings"
)

// extractJSON decodes v from s, tolerating a claude/pi-style reply that
// wraps the JSON object in prose or a ```json fence — the api adapter's
// replies are already bare JSON (caller.Ask strict-decodes), so this is a
// no-op fast path for that adapter and a best-effort salvage for the
// shelled-CLI adapters.
func extractJSON(s string, v any) error {
	if err := json.Unmarshal([]byte(s), v); err == nil {
		return nil
	}
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return fmt.Errorf("drive: no JSON object found in reply")
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
		case c == '\\' && inStr:
			esc = true
		case c == '"':
			inStr = !inStr
		case inStr:
			// inside a string literal; braces don't count
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return json.Unmarshal([]byte(s[start:i+1]), v)
			}
		}
	}
	return fmt.Errorf("drive: unterminated JSON object in reply")
}
