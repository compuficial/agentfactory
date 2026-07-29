package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// day is the largest unit shortDuration renders.
const day = 24 * time.Hour

// shortDuration renders a duration as its two most significant units
// ("2h3m", "4m32s", "12s").
func shortDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	switch {
	case d >= day:
		return fmt.Sprintf("%dd%dh", d/day, (d%day)/time.Hour)
	case d >= time.Hour:
		return fmt.Sprintf("%dh%dm", d/time.Hour, (d%time.Hour)/time.Minute)
	case d >= time.Minute:
		return fmt.Sprintf("%dm%ds", d/time.Minute, (d%time.Minute)/time.Second)
	default:
		return fmt.Sprintf("%ds", d/time.Second)
	}
}

// Ago humanizes a past timestamp ("5s ago").
func Ago(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return shortDuration(time.Since(t)) + " ago"
}

// ExpandHome resolves a leading ~ or ~/ to the user's home directory.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	return p
}

// TildePath abbreviates the home directory prefix to ~ (the inverse of
// ExpandHome, for display).
func TildePath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+"/") {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// TailLines returns the last n lines of data (n<=0 = everything).
func TailLines(data []byte, n int) []byte {
	if n <= 0 || len(data) == 0 {
		return data
	}
	end := len(data)
	if data[end-1] == '\n' {
		end--
	}
	seen := 0
	for i := end - 1; i >= 0; i-- {
		if data[i] == '\n' {
			seen++
			if seen == n {
				return data[i+1:]
			}
		}
	}
	return data
}

// StatusLabel appends the exit code to terminal statuses ("exited(7)").
func StatusLabel(s *AgentSession) string {
	if s.Status == StatusExited && s.ExitCode != nil {
		return fmt.Sprintf("exited(%d)", *s.ExitCode)
	}
	return string(s.Status)
}

// Uptime is the session's lifetime so far (or total, once ended).
func Uptime(s *AgentSession) string {
	end := time.Now()
	if s.EndedAt != nil {
		end = *s.EndedAt
	}
	return shortDuration(end.Sub(s.StartedAt))
}
