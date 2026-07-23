package core

import (
	"strings"
	"testing"
)

// The byte parsers face arbitrary pty streams — every harness bug,
// partial write, and hostile payload lands here first. Fuzzing pins
// the two invariants that matter: never panic, never emit garbage.

func FuzzScanStreamEvents(f *testing.F) {
	for _, seed := range []string{
		"hello\aworld",
		"\x1b]9;needs your permission\x07",
		"\x1b]777;notify;Claude;waiting for input\x1b\\",
		"\x1b]133;B\x07", "\x1b]133;C\x07", "\x1b]133;D;0\x07",
		"\x1b]0;title\x07plain",
		"text\x1b]9;unfinished",
		"\x1b]", "\x1b", "\a\a\a", "",
		"\x1b]133;", "\x1b]9;\x1b\\", "\x1b]777;notify;",
		"\x1b]9;\x1b\x07", // fuzz-found: bare ESC aborts the OSC body
	} {
		f.Add([]byte(seed))
	}
	cfg := CompileSignals(nil, func(string) {})
	f.Fuzz(func(t *testing.T, data []byte) {
		out := ScanStreamEvents(data, cfg)
		if out.Verdict < SignalNone || out.Verdict > SignalWorking {
			t.Fatalf("verdict out of range: %v", out.Verdict)
		}
		for _, n := range out.Notifications {
			if strings.ContainsAny(n, "\x1b\x07") {
				t.Fatalf("notification leaked escape bytes: %q", n)
			}
		}
	})
}

func FuzzSanitizeTerminal(f *testing.F) {
	for _, seed := range []string{
		"plain text\n",
		"\x1b[2J\x1b[Hcleared\x1b[?1049h",
		"progress 10%\rprogress 99%\r\n",
		"\x1b]0;title\x07body\x1b]9;note\x1b\\",
		"\x1b[38;5;205mcolor\x1b[0m",
		"\r\r\r", "\x1b", "",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		out := SanitizeTerminal(data)
		if strings.ContainsRune(out, 0x1b) {
			t.Fatalf("sanitized output leaked ESC: %q", out)
		}
		// MeaningfulText builds on the same sanitizer; it must agree
		// that letterless output is not meaningful.
		if !MeaningfulText(data) {
			for _, r := range out {
				if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
					t.Fatalf("MeaningfulText=false but sanitized output has %q", r)
				}
			}
		}
	})
}
