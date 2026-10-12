package clip

import "strings"

func normalizeNewlines(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

func clipboardText(s, platform string) string {
	s = normalizeNewlines(s)
	if platform == "windows" {
		return strings.ReplaceAll(s, "\n", "\r\n")
	}
	return s
}
