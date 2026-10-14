package agents

import (
	"encoding/json"
	"strings"
)

// SummaryText is an excerpt of the agent's own final reply, not an inferred
// result. Keep one readable line and sentence, bounded like task titles.
func SummaryText(text string) string {
	if summary, ok := structuredSummary(text); ok {
		text = summary
	}
	fenced := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced || line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimLeft(line, "-*•> ")
		line = strings.ReplaceAll(strings.ReplaceAll(line, "**", ""), "`", "")
		line = TaskText(line)
		for i, r := range line {
			if strings.ContainsRune("。！？", r) {
				return line[:i+len(string(r))]
			}
			if r == '.' && (i+1 == len(line) || line[i+1] == ' ') {
				return line[:i+1]
			}
		}
		if line != "" {
			return line
		}
	}
	return ""
}

// Extract before sentence/length clipping. Older producers may already have
// clipped a one-line JSON string at its first sentence, without closing quotes.
func structuredSummary(text string) (string, bool) {
	text = strings.TrimSpace(text)
	for _, fence := range []string{"```", "~~~"} {
		if strings.HasPrefix(text, fence) {
			if newline := strings.IndexByte(text, '\n'); newline >= 0 {
				text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text[newline+1:]), fence))
			}
			break
		}
	}
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(text), &object) == nil {
		if raw, ok := object["summary"]; ok {
			var summary string
			_ = json.Unmarshal(raw, &summary)
			return summary, true
		}
		return "", false
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return "", false
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			break
		}
		if key == "summary" {
			var summary string
			if decoder.Decode(&summary) == nil {
				return summary, true
			}
			// Repair only a missing closing quote, never arbitrary malformed JSON.
			value := strings.TrimSpace(text[decoder.InputOffset():])
			value = strings.TrimSpace(strings.TrimPrefix(value, ":"))
			if strings.HasPrefix(value, "\"") {
				_ = json.Unmarshal([]byte(value+"\""), &summary)
			}
			return summary, true
		}
		var ignored json.RawMessage
		if decoder.Decode(&ignored) != nil {
			break
		}
	}
	return "", false
}
