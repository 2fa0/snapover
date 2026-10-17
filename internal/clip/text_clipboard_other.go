//go:build !darwin && !windows

package clip

import "github.com/egoist/mygo"

func readLimitedClipboardText(limit int) (string, error) {
	limit = normalizeTextLimit(limit)
	text := mygo.Clipboard.ReadText()
	if text == "" {
		return "", nil
	}
	if err := CheckTextSize(text, limit); err != nil {
		return "", err
	}
	return text, nil
}
