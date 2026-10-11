package agents

import "strings"

// SummaryText is an excerpt of the agent's own final reply, not an inferred
// result. Keep one readable line and sentence, bounded like task titles.
func SummaryText(text string) string {
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
