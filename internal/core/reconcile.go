package core

import (
	"io"
	"os"
	"time"
)

// Reconcile updates every non-terminal session from runtime observations.
// The backend is the runtime source of truth for liveness; the DB is updated
// to match. detect holds compiled T1.5 screen-pattern rules per harness
// and signals the T1.75 terminal-signal config (either nil = tier off).
// Idempotent; safe to run concurrently (WAL, optimistic writes). A
// session whose backend queries race away mid-pass is skipped until the
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
	// 1-2. Liveness: backend session gone => failed; payload dead => exited +
	// cleanup. Resolved here means the caller is done with this row.
	if handled, err := reconcileLiveness(store, backend, sess, now); handled {
		return err
	}
	observedStatus := sess.Status
	observedOrigin := sess.StatusOrigin
	observedOffset := sess.LogOffset
	persist := func() error {
		return store.UpdateReconciledSession(sess, observedStatus, observedOrigin, observedOffset)
	}

	// 3. One read of the new log bytes feeds two tiers: T1 activity
	// (growth that contains text — idle TUIs animate, and those frames
	// must not read as work) and T1.75 terminal signals (BEL, OSC
	// notifications, OSC 133 marks — the app declaring its own state).
	prev := sess.Status
	grew, meaningful, sigs := readActivity(sess, signals)

	// 4. Sticky-state hold. Signal-set state (T2) yields
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
				return persist()
			}
			return nil
		}
	}

	// 5. T1.75 verdicts (or plain meaningful output) apply immediately —
	// a bell fires at the moment of turn end, before the session reads as
	// quiet; gating on the idle threshold would forfeit the latency win.
	if applyStreamVerdict(sess, sigs, meaningful, now) {
		return persist()
	}

	// 6. idle vs working by threshold. A starting session has emitted no
	// output yet, so it never reads working (starting ->
	// working happens on first observed growth); it stays starting until
	// idle_threshold, then reads idle.
	quiet := now.Sub(sess.LastActive) >= idleThreshold
	if quiet {
		sess.Status = StatusIdle
		sess.StatusOrigin = ""
	} else if sess.Status != StatusStarting {
		sess.Status = StatusWorking
	}

	// 7. T1.5 detection: a quiet session whose harness has rules gets one
	// screen capture; a match reads as awaiting-input (origin detect,
	// re-evaluated every pass). A streaming session is by definition not
	// awaiting input, so no capture happens for it.
	if quiet {
		detectAwaiting(backend, sess, detect)
	}

	if sess.Status != prev || grew {
		return persist()
	}
	return nil
}

// reconcileLiveness handles the two terminal transitions: backend session
// gone (=> failed) and payload dead (=> exited + cleanup). It reports
// whether it resolved the session (caller returns handled's err) and a
// hard error. Query/kill errors are session-scoped races — the session
// vanished mid-pass — so they resolve the row without failing the pass.
func reconcileLiveness(store *Store, backend SessionBackend, sess *AgentSession, now time.Time) (handled bool, err error) {
	alive, err := backend.IsAlive(sess.ID)
	if err != nil {
		return true, err
	}
	if !alive {
		sess.markFailed(now)
		return true, store.UpdateSession(sess)
	}
	dead, code, err := backend.DeadStatus(sess.ID)
	if err != nil {
		return true, nil // leave this row for the next pass
	}
	if dead {
		sess.markExited(code, now)
		if err := backend.Kill(sess.ID); err != nil {
			return true, nil // retry the harvest next pass; remain-on-exit holds the pane
		}
		return true, store.UpdateSession(sess)
	}
	return false, nil
}

// readActivity reads the newly appended log bytes once and derives the
// signals reconciliation needs: whether the delta held meaningful text
// (T1) and any terminal-protocol events (T1.75). It advances
// sess.LogOffset; grew reports whether the log file grew this pass.
func readActivity(sess *AgentSession, signals *CompiledSignals) (grew, meaningful bool, sigs StreamSignals) {
	size := int64(0)
	if fi, err := os.Stat(sess.LogPath); err == nil {
		size = fi.Size()
	}
	grew = size > sess.LogOffset
	if grew {
		nextOffset := size
		if delta, ok := readLogDelta(sess.LogPath, sess.LogOffset, size); !ok {
			meaningful = true // read problems count as activity
		} else {
			meaningful = MeaningfulText(delta)
			if signals != nil {
				var consumed int
				sigs, consumed = scanStreamEvents(delta, signals)
				nextOffset = size - int64(len(delta)-consumed)
			}
		}
		sess.LogOffset = nextOffset
	}
	return grew, meaningful, sigs
}

// applyStreamVerdict applies a T1.75 protocol verdict, or plain
// meaningful output, to the session immediately. It reports whether it
// resolved the session (caller returns) and the store-write error.
func applyStreamVerdict(sess *AgentSession, sigs StreamSignals, meaningful bool, now time.Time) bool {
	switch sigs.Verdict {
	case SignalAttention:
		if meaningful {
			sess.LastActive = now
		}
		sess.Status = StatusAwaitingInput
		sess.StatusOrigin = OriginTerm
		return true
	case SignalWorking:
		sess.LastActive = now
		sess.Status = StatusWorking
		sess.StatusOrigin = ""
		return true
	}
	if meaningful {
		sess.LastActive = now
		sess.Status = StatusWorking
		sess.StatusOrigin = ""
		return true
	}
	return false
}

// detectAwaiting applies T1.5 screen-pattern detection to a quiet
// session: one screen capture matched against the harness's rules (or
// the universal fallback). A match flips the session to awaiting-input
// (origin detect). Capture errors skip detection — a session-scoped
// race, same policy as reconcileLiveness.
func detectAwaiting(backend SessionBackend, sess *AgentSession, detect map[string]*CompiledDetect) {
	rules := detect[sess.Harness]
	if rules == nil {
		rules = detect[UniversalDetect] // shared TUI conventions
	}
	if rules == nil {
		return
	}
	if screen, err := backend.CapturePane(sess.ID, detectLines); err == nil && rules.AwaitingScreen(screen) {
		sess.Status = StatusAwaitingInput
		sess.StatusOrigin = OriginDetect
	}
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
	if _, seekErr := f.Seek(from, io.SeekStart); seekErr != nil {
		return nil, false
	}
	buf := make([]byte, to-from)
	n, err := io.ReadFull(f, buf)
	if n == 0 && err != nil {
		return nil, false
	}
	return buf[:n], true
}
