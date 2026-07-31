package tui

import "testing"

func TestValidateCommandBarArgsAllowsOnlyBoundedCommands(t *testing.T) {
	for _, args := range [][]string{
		{"status"},
		{"open", "definition"},
		{"logs", "session"},
		{"send", "session", "hello"},
		{"peek", "session"},
		{"close", "session"},
		{"kill", "session"},
		{"rm", "session"},
		{"prune"},
		{"define", "definition"},
		{"defs"},
		{"rm-def", "definition"},
		{"signal", "session", "done"},
		{"doctor"},
		{"version"},
	} {
		if err := validateCommandBarArgs(args); err != nil {
			t.Errorf("safe command %v rejected: %v", args, err)
		}
	}

	for _, args := range [][]string{
		{"claude"},
		{"mcp"},
		{"wait", "session"},
		{"logs", "session", "--follow"},
		{"logs", "-f", "session"},
	} {
		if err := validateCommandBarArgs(args); err == nil {
			t.Errorf("unsafe command %v was accepted", args)
		}
	}
}
