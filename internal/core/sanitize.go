package core

import (
	"io"
	"os"
	"regexp"
	"strings"
	"unicode"
)

// Terminal escape sequences in pipe-pane logs. Full-screen harnesses
// emit cursor addressing, clears, and alt-screen switches; printed
// verbatim those corrupt whatever terminal shows them.
var (
	oscRe = regexp.MustCompile(`\x1b\][^\x07\x1b]*(\x07|\x1b\\)?`)
	csiRe = regexp.MustCompile(`\x1b\[[0-9;?:><=!]*[ -/]*[@-~]`)
	escRe = regexp.MustCompile(`\x1b[()#%][0-9A-Za-z@]|\x1b[@-_=><~c]`)
)

// SanitizeTerminal reduces a raw terminal byte stream to displayable
// text: escape sequences dropped, carriage-return overwrites resolved
// (last write wins), control characters removed, and runs of blank
// lines collapsed. Lossy by design; use the raw stream for forensics.
func SanitizeTerminal(data []byte) string {
	s := string(data)
	s = oscRe.ReplaceAllString(s, "")
	s = csiRe.ReplaceAllString(s, "")
	s = escRe.ReplaceAllString(s, "")

	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blanks := 0
	for _, line := range lines {
		line = strings.TrimRight(line, "\r") // CRLF is a plain line ending
		if i := strings.LastIndexByte(line, '\r'); i >= 0 {
			line = line[i+1:] // progress-bar style overwrite: keep the last write
		}
		line = strings.Map(func(r rune) rune {
			if r == '\t' || r >= 0x20 {
				return r
			}
			return -1
		}, line)
		if strings.TrimSpace(line) == "" {
			blanks++
			if blanks > 1 {
				continue // collapse blank runs to a single line
			}
			line = ""
		} else {
			blanks = 0
		}
		out = append(out, line)
	}
	// Trim leading/trailing blank lines.
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

// MeaningfulText reports whether raw terminal output contains letters
// or digits once escape sequences are stripped. Idle TUI harnesses can
// animate constantly (grok-cli redraws a braille-art logo at ~11KB/s),
// but animation is symbols and color codes — words and numbers mean an
// agent actually said something.
func MeaningfulText(data []byte) bool {
	for _, r := range SanitizeTerminal(data) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// ReadLogTail reads at most maxBytes from the end of the log file and
// returns it sanitized for display. maxBytes <= 0 reads the whole file.
// A missing file is an empty log.
func ReadLogTail(path string, maxBytes int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	defer f.Close()
	if maxBytes > 0 {
		if fi, statErr := f.Stat(); statErr == nil && fi.Size() > maxBytes {
			if _, seekErr := f.Seek(fi.Size()-maxBytes, io.SeekStart); seekErr != nil {
				return "", seekErr
			}
		}
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	return SanitizeTerminal(data), nil
}
