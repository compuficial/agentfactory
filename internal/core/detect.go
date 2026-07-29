package core

import (
	"fmt"
	"regexp"
)

// DetectRules are a harness's T1.5 screen-pattern rules — pure data,
// user-configurable like every other harness field. A quiet session
// whose rendered screen matches any awaiting_input pattern, and no
// working pattern, reads awaiting-input without a harness-side hook.
type DetectRules struct {
	AwaitingInput []string `json:"awaiting_input" yaml:"awaiting_input"`
	Working       []string `json:"working" yaml:"working"`
}

// Empty reports whether the rules can never match (no awaiting
// patterns; working patterns alone only suppress).
func (r DetectRules) Empty() bool { return len(r.AwaitingInput) == 0 }

// CompiledDetect is one harness's rules, compiled.
type CompiledDetect struct {
	AwaitingInput []*regexp.Regexp
	Working       []*regexp.Regexp
}

// detectLines bounds how much rendered screen detection inspects: the
// dialogs and prompt footers being matched live at the bottom.
const detectLines = 40

// UniversalDetect keys the fallback rules in a compiled detect map:
// terminal-UI conventions shared across harnesses. A harness with its
// own rules overrides the fallback entirely; "*" is reserved (it can
// never collide with a harness name — names come from map keys users
// choose, and Resolve never sees it).
const UniversalDetect = "*"

// Detect patterns shared between the universal fallback rules and
// specific harnesses, so each literal lives in one place.
const (
	patDoYouWant      = `(?i)do you want`
	patEscToInterrupt = `esc to interrupt`
)

// universalRules are the fallback patterns. Conservative on purpose: a
// false awaiting-input poisons af wait, a missed one only delays it.
var universalRules = DetectRules{
	AwaitingInput: []string{
		`(?i)\((y/n|yes/no)\)`,
		`(?i)\[(y/n|yes/no)\]`,
		`(?i)press (enter|any key)`,
		patDoYouWant,
		`(?i)would you like`,
		`(?i)proceed\?`,
		`❯ 1\.`, // numbered choice menus
	},
	Working: []string{
		`(?i)esc to interrupt`,
		`(?i)ctrl\+c to (stop|cancel|interrupt)`,
		`[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏]`, // braille spinners
	},
}

// CompileDetect compiles every harness's rules. Invalid patterns are
// reported through warn and skipped (config precedent: warn, don't
// fail). Harnesses whose rules end up empty are omitted entirely so
// reconciliation skips their screen captures.
func CompileDetect(set HarnessSet, warn func(string)) map[string]*CompiledDetect {
	out := map[string]*CompiledDetect{
		UniversalDetect: {
			AwaitingInput: compilePatterns(UniversalDetect, "awaiting_input", universalRules.AwaitingInput, warn),
			Working:       compilePatterns(UniversalDetect, "working", universalRules.Working, warn),
		},
	}
	for name, h := range set {
		if h.Detect.Empty() {
			continue
		}
		c := &CompiledDetect{
			AwaitingInput: compilePatterns(name, "awaiting_input", h.Detect.AwaitingInput, warn),
			Working:       compilePatterns(name, "working", h.Detect.Working, warn),
		}
		if len(c.AwaitingInput) > 0 {
			out[name] = c
		}
	}
	return out
}

func compilePatterns(harness, field string, patterns []string, warn func(string)) []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			warn(fmt.Sprintf("harness %s: bad detect %s pattern %q: %v", harness, field, p, err))
			continue
		}
		out = append(out, re)
	}
	return out
}

// AwaitingScreen reports whether the rendered screen reads as blocked
// on the user: any awaiting pattern matches and no working pattern does.
func (c *CompiledDetect) AwaitingScreen(screen string) bool {
	for _, re := range c.Working {
		if re.MatchString(screen) {
			return false
		}
	}
	for _, re := range c.AwaitingInput {
		if re.MatchString(screen) {
			return true
		}
	}
	return false
}
