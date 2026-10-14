package main

import (
	"regexp"
	"strings"

	"github.com/egoist/mygo/ui/editor"
)

// Clipboard text has no filename. Only use distinctive syntax in a bounded
// sample, and leave unrecognized prose unhighlighted.
var pickTextLanguages = []struct {
	name    string
	pattern *regexp.Regexp
}{
	{"markdown", regexp.MustCompile("(?m)^\\s*(```|~~~)")},
	{"json", regexp.MustCompile(`^[\[{]\s*("|\{|\]|-?[0-9]|true\b|false\b|null\b)`)},
	{"html", regexp.MustCompile(`(?i)^\s*(<!doctype\s+html\b|<(html|head|body|div|span|p|script|style)\b)`)},
	{"xml", regexp.MustCompile(`^<\?xml\b`)},
	{"go", regexp.MustCompile(`(?m)^\s*(package\s+\w+\s*$|func\s+(\w+\s*\(|\([^\n]+\)\s*\w+\s*\())`)},
	{"python", regexp.MustCompile(`(?m)^\s*((async\s+)?def\s+\w+\([^\n]*\).*:|from\s+[\w.]+\s+import\s+|class\s+\w+[^\n]*:)`)},
	{"rust", regexp.MustCompile(`(?m)^\s*((pub\s+)?(async\s+)?fn\s+\w+\s*\(|use\s+[\w]+::)`)},
	{"shell", regexp.MustCompile(`(?m)^#![^\n]*\b(bash|sh|zsh|fish)\b|^\s*(export\s+\w+=|echo\s+|sudo\s+)`)},
	{"sql", regexp.MustCompile(`(?i)^\s*(select\s+[^\n]+\s+from\b|insert\s+into\b|update\s+\w+\s+set\b|create\s+table\b)`)},
	{"typescript", regexp.MustCompile(`(?m)^\s*((export\s+)?(async\s+)?function\s+\w+\s*\(|(export\s+)?(const|let|var)\s+\w+\s*[:=]|(export\s+)?(interface|type)\s+\w+\s*[={])`)},
	{"markdown", regexp.MustCompile(`(?m)^#{1,6}\s+\S`)},
}

func pickTextLanguage(text string) *editor.Language {
	text = strings.TrimSpace(text[:min(len(text), textReaderPageBytes)])
	for _, language := range pickTextLanguages {
		if language.pattern.MatchString(text) {
			return editor.LanguageNamed(language.name)
		}
	}
	return nil
}
