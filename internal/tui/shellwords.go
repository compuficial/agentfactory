package tui

import "strings"

// splitWords splits a command-bar line into argv, honoring single and
// double quotes (no escapes; this is a command bar, not a shell).
func splitWords(line string) []string {
	var words []string
	var cur strings.Builder
	var quote rune
	flush := func() {
		if cur.Len() > 0 {
			words = append(words, cur.String())
			cur.Reset()
		}
	}
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return words
}
