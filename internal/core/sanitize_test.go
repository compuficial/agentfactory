package core

import (
	"strings"
	"testing"
)

func TestSanitizeTerminal(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain crlf", "hello\r\nworld\r\n", "hello\nworld\n"},
		{"colors stripped", "\x1b[31mred\x1b[0m plain\n", "red plain\n"},
		{"cursor and clears stripped", "\x1b[2J\x1b[1;1H\x1b[?1049htop\n", "top\n"},
		{"osc title stripped", "\x1b]0;my title\x07text\n", "text\n"},
		{"charset stripped", "\x1b(Bhello\n", "hello\n"},
		{"progress overwrite keeps last", "10%\r20%\r99%\r\n", "99%\n"},
		{"blank runs collapse", "a\n\n\n\n\nb\n", "a\n\nb\n"},
		{"leading/trailing blanks trimmed", "\n\n\nmiddle\n\n\n", "middle\n"},
		{"control chars dropped, tab kept", "a\x00\x08b\tc\n", "ab\tc\n"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		if got := SanitizeTerminal([]byte(tc.in)); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
	// Nothing that could move a cursor or clear a screen survives.
	soup := "\x1b[3;9H\x1b[K\x1b[2Jgarble\x1b[?25l\x1b[0m\n"
	if got := SanitizeTerminal([]byte(soup)); strings.ContainsRune(got, 0x1b) {
		t.Errorf("escape byte survived: %q", got)
	}
}
