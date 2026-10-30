// Package formatter converts documents without performing network operations.
package formatter

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const MaxInputBytes = 1 << 20

type ConversionResult struct {
	Text     string   `json:"text"`
	Warnings []string `json:"warnings"`
}

func Convert(content, format string) (ConversionResult, error) {
	if len(content) > MaxInputBytes || !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
		return ConversionResult{}, fmt.Errorf("content must be valid UTF-8 without NUL, at most 1 MiB")
	}
	switch format {
	case "cosense":
		return ConversionResult{Text: content, Warnings: []string{}}, nil
	case "markdown":
		return markdown(content)
	case "html":
		return htmlDocument(content)
	default:
		return ConversionResult{}, fmt.Errorf("format must be markdown, html, or cosense")
	}
}

type renderer struct {
	warnings []string
	tables   int
}

func (r *renderer) warn(s string) {
	for _, w := range r.warnings {
		if w == s {
			return
		}
	}
	r.warnings = append(r.warnings, s)
}

// Cosense has no general backslash escape for literal brackets in prose.
func (r *renderer) literal(s string) string {
	if strings.ContainsAny(s, "[]") {
		r.warn("Literal square brackets were changed to fullwidth brackets to avoid unintended Cosense links.")
		s = strings.NewReplacer("[", "［", "]", "］").Replace(s)
	}
	return s
}

func indent(s string, depth int) string {
	prefix := strings.Repeat(" ", depth)
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}
