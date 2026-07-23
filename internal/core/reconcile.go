package core

import (
	"io"
	"os"
	"time"
)

// Reconcile runs the §10.1 algorithm over every non-terminal session.
// tmux is the runtime source of truth for liveness; the DB is updated
// to match. detect holds compiled T1.5 screen-pattern rules per harness
// and signals the T1.75 terminal-signal config (either nil = tier off).
// Idempotent; safe to run concurrently (WAL, last-writer-wins). A
// session whose tmux queries race away mid-pass is skipped until the
// next pass; environment and DB-write failures abort the pass.
func Reconcile(store *Store, backend SessionBackend, idleThreshold time.Duration, detect map[string]*CompiledDetect, signals *CompiledSignals, now time.Time) error {
	sessions, err := store.ListSessions(false)
	if err != nil {
		return err
	}
	for _, sess := range sessions {
		if err := reconcileOne(store, backend, sess, idleThreshold, detect, signals, now); err != nil {
			return err
		}
	}
	return nil
}

func reconcileOne(store *Store, backend SessionBackend, sess *AgentSession, idleThreshold time.Duration, detect map[string]*CompiledDetect, signals *CompiledSignals, now time.Time) error {
	// 1. tmux session missing => failed (server killed externally, etc.)
	alive, err := backend.IsAlive(sess.ID)
	if err != nil {
		return err
	}
	if !alive {
		sess.markFailed(now)
		return store.UpdateSession(sess)
	}

	// 2. pane dead => exited(pane_dead_status), then kill-session.
	// Query/kill errors here are session-scoped races (the session
	// vanished mid-pass); leave this row for the next pass instead of
	// failing everyone's reconciliation.
	dead, code, err := backend.DeadStatus(sess.ID)
	if err != nil {
		return nil
	}
	if dead {
		sess.markExited(code, now)
		if err := backend.Kill(sess.ID); err != nil {
			return nil // retry the harvest next pass; remain-on-exit holds the pane
		}
		return store.UpdateSession(sess)
	}

	// 3. One read of the new log bytes feeds two tiers: T1 activity
	// (growth that contains text — idle TUIs animate, and those frames
	// must not read as work) and T1.75 terminal signals (BEL, OSC
	// notifications, OSC 133 marks — the app declaring its own state).
	size := int64(0)
	if fi, err := os.Stat(sess.LogPath); err == nil {
		size = fi.Size()
	}
	prev := sess.Status
	grew := size > sess.LogOffset
	meaningful := false
	var sigs StreamSignals
	if grew {
		if delta, ok := readLogDelta(sess.LogPath, sess.LogOffset, size); !ok {
			meaningful = true // read problems count as activity
		} else {
			meaningful = MeaningfulText(delta)
			if signals != nil {
				sigs = ScanStreamEvents(delta, signals)
			}
		}
		sess.LogOffset = size // always advance, so deltas stay small
	}

	// 4. Sticky-state hold (§7.1 rule 3). Signal-set state (T2) yields
	// only to meaningful output. Term-set state additionally yields to
	// new protocol events (a bell refreshes it, a command-start clears
	// it). Detect-set state falls through — re-evaluated every pass.
	if sess.Status.Sticky() && sess.StatusOrigin != OriginDetect {
		held := !meaningful
		if sess.StatusOrigin == OriginTerm && sigs.Verdict != SignalNone {
			held = false
		}
		if held {
			if grew {
				return store.UpdateSession(sess)
			}
			return nil
		}
	}

	// 5. T1.75 verdicts apply immediately — a bell fires at the moment
	// of turn end, before the session reads as quiet; gating on the
	// idle threshold would forfeit the tier's latency win.
	switch sigs.Verdict {
	case SignalAttention:
		if meaningful {
			sess.LastActive = now
		}
		sess.Status = StatusAwaitingInput
		sess.StatusOrigin = OriginTerm
		return store.UpdateSession(sess)
	case SignalWorking:
		sess.LastActive = now
		sess.Status = StatusWorking
		sess.StatusOrigin = ""
		return store.UpdateSession(sess)
	}
	if meaningful {
		sess.LastActive = now
		sess.Status = StatusWorking
		sess.StatusOrigin = ""
		return store.UpdateSession(sess)
	}

	// 6. idle vs working by threshold. A starting session has emitted no
	// output yet, so it never reads working (§7.1 rule 1: starting ->
	// working happens on first observed growth); it stays starting until
	// idle_threshold, then reads idle.
	quiet := now.Sub(sess.LastActive) >= idleThreshold
	if quiet {
		sess.Status = StatusIdle
		sess.StatusOrigin = ""
	} else if sess.Status != StatusStarting {
		sess.Status = StatusWorking
	}

	// 7. T1.5 detection: a quiet session whose harness has rules gets
	// one screen capture; a matching screen reads as awaiting-input
	// (origin detect, re-evaluated every pass — the screen ceasing to
	// match reverts it above). An actively-streaming session is by
	// definition not awaiting input, so no capture happens for it.
	// Capture errors skip detection: a session-scoped race, same
	// policy as step 2.
	if quiet {
		rules := detect[sess.Harness]
		if rules == nil {
			rules = detect[UniversalDetect] // shared TUI conventions
		}
		if rules != nil {
			if screen, err := backend.CapturePane(sess.ID, detectLines); err == nil && rules.AwaitingScreen(screen) {
				sess.Status = StatusAwaitingInput
				sess.StatusOrigin = OriginDetect
			}
		}
	}

	if sess.Status != prev || grew {
		return store.UpdateSession(sess)
	}
	return nil
}

// readLogDelta returns the newly appended log bytes (capped to the
// trailing 256KB for huge deltas). ok=false on read problems — callers
// treat that as activity: better a false working than a stuck idle.
func readLogDelta(path string, from, to int64) ([]byte, bool) {
	const maxDelta = 256 << 10
	if to-from > maxDelta {
		from = to - maxDelta
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return nil, false
	}
	buf := make([]byte, to-from)
	n, err := io.ReadFull(f, buf)
	if n == 0 && err != nil {
		return nil, false
	}
	return buf[:n], true
}
