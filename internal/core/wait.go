package core

import "time"

// WaitOutcome is the result of a Wait poll loop.
type WaitOutcome int

// Wait poll outcomes.
const (
	WaitReached  WaitOutcome = iota // session hit a target status
	WaitTerminal                    // session ended in a non-target terminal status
	WaitTimeout                     // deadline elapsed first
)

// waitMinInterval bounds poll frequency: each poll is a full
// reconciliation pass (tmux queries + a stat per live session).
const waitMinInterval = 50 * time.Millisecond

// Wait polls — one reconciliation pass per tick, exactly like the TUI —
// until the session's status enters targets, the session reaches a
// terminal status outside targets, or timeout elapses (<= 0 waits
// forever). Terminal statuses in targets count as reached, so waiting
// for exited works naturally.
func (m *Manager) Wait(id string, targets map[Status]bool, timeout, interval time.Duration) (*AgentSession, WaitOutcome, error) {
	if interval < waitMinInterval {
		interval = waitMinInterval
	}
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	for {
		if err := m.Reconcile(); err != nil {
			return nil, 0, err
		}
		sess, err := m.Store.GetSession(id)
		if err != nil {
			return nil, 0, err
		}
		switch {
		case targets[sess.Status]:
			return sess, WaitReached, nil
		case sess.Status.Terminal():
			return sess, WaitTerminal, nil
		case !deadline.IsZero() && time.Now().After(deadline):
			return sess, WaitTimeout, nil
		}
		time.Sleep(interval)
	}
}
