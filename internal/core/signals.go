package core

import (
	"regexp"
	"strings"
)

// T1.75 terminal signals (spec v1.2): the session's own protocol
// stream — BEL, OSC 9/777 notifications, OSC 133 prompt marks —
// declares state that screen patterns can only guess. Phase 0 verified
// every sequence survives into the pipe-pane log, so extraction is a
// second parse of the delta bytes reconciliation already reads.

// SignalVerdict is the last-seen protocol verdict in a delta. Ordering
// matters: a bell followed by a command-start means working, not
// blocked — later events overwrite earlier ones.
type SignalVerdict int

const (
	SignalNone      SignalVerdict = iota
	SignalAttention               // bell / notification / prompt-input / turn end
	SignalWorking                 // OSC 133;C — command running
)

// StreamSignals is what ScanStreamEvents found in one delta.
type StreamSignals struct {
	Verdict       SignalVerdict
	Notifications []string // OSC 9 payloads and OSC 777;notify bodies, in order
}

// CompiledSignals configures notification classification. A nil/empty
// NotifyAwaiting means every notification counts as attention — a
// terminal notification is by definition an attention request; the
// keyword list exists to narrow that when a harness emits noisy
// progress notifications.
type CompiledSignals struct {
	NotifyAwaiting []*regexp.Regexp
}

// CompileSignals builds the notification filter from config overrides
// (nil = match all). Invalid patterns warn and are skipped.
func CompileSignals(overrides []string, warn func(string)) *CompiledSignals {
	return &CompiledSignals{
		NotifyAwaiting: compilePatterns("signals", "notify_awaiting", overrides, warn),
	}
}

func (c *CompiledSignals) notifyMatches(payload string) bool {
	if len(c.NotifyAwaiting) == 0 {
		return true
	}
	for _, re := range c.NotifyAwaiting {
		if re.MatchString(payload) {
			return true
		}
	}
	return false
}

// ScanStreamEvents scans raw session output for terminal-protocol
// signals. Tolerant by design: unknown escapes are skipped, and an OSC
// truncated at the buffer edge ends the scan (the next delta re-reads
// nothing — same latitude the T1 heuristic accepts for its text check).
// A BEL that terminates an OSC is a terminator, not a bell.
func ScanStreamEvents(buf []byte, cfg *CompiledSignals) StreamSignals {
	var out StreamSignals
	for i := 0; i < len(buf); i++ {
		switch buf[i] {
		case 0x07: // standalone BEL
			out.Verdict = SignalAttention
		case 0x1b: // ESC
			if i+1 >= len(buf) || buf[i+1] != ']' {
				continue // not an OSC; other escapes pass through
			}
			content, end, ok := parseOSC(buf, i+2)
			i = end
			if ok {
				classifyOSC(content, cfg, &out)
			}
		}
	}
	return out
}

// parseOSC consumes an OSC body starting at buf[start] up to its BEL or
// ESC\ (ST) terminator, returning the body, the index of the last
// consumed byte, and whether the sequence terminated cleanly. A bare
// ESC inside the body aborts the sequence — mirroring terminal parsers,
// and keeping escape bytes out of notification payloads — with next
// positioned so the scanner reprocesses that ESC. A body truncated at
// the buffer edge consumes the rest of the delta.
func parseOSC(buf []byte, start int) (content string, next int, ok bool) {
	for j := start; j < len(buf); j++ {
		switch buf[j] {
		case 0x07:
			return string(buf[start:j]), j, true
		case 0x1b:
			if j+1 < len(buf) && buf[j+1] == '\\' {
				return string(buf[start:j]), j + 1, true
			}
			return "", j - 1, false // abort; rescan from this ESC
		}
	}
	return "", len(buf), false // truncated at the edge
}

func classifyOSC(content string, cfg *CompiledSignals, out *StreamSignals) {
	switch {
	case strings.HasPrefix(content, "9;"):
		payload := content[len("9;"):]
		out.Notifications = append(out.Notifications, payload)
		if cfg.notifyMatches(payload) {
			out.Verdict = SignalAttention
		}
	case strings.HasPrefix(content, "777;notify;"):
		payload := strings.ReplaceAll(content[len("777;notify;"):], ";", " ")
		out.Notifications = append(out.Notifications, payload)
		if cfg.notifyMatches(payload) {
			out.Verdict = SignalAttention
		}
	case strings.HasPrefix(content, "133;"):
		switch rest := content[len("133;"):]; {
		case strings.HasPrefix(rest, "B"), strings.HasPrefix(rest, "D"):
			// B: input begins. D: turn/command finished — for an agent
			// REPL, at-prompt is awaiting-input.
			out.Verdict = SignalAttention
		case strings.HasPrefix(rest, "C"):
			out.Verdict = SignalWorking
		}
	}
}
