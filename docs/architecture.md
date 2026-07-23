# Architecture

How `af` works, and why it's built the way it is. The original
implementation spec lives at [spec.md](spec.md); this document is the
short version a contributor actually needs.

## The one-paragraph version

`af` is **daemonless**. tmux (on a dedicated socket) holds every live
session; SQLite holds identity, metadata, and history. There is no
background `af` process to crash, upgrade, or babysit: every `af`
invocation — and the dashboard on each tick — runs a *reconciliation*
pass that trues up the database against tmux before answering. You can
delete the `af` binary, rebuild it, and pick up your running agents
exactly where they were.

```
        ┌────────────┐        ┌────────────────┐
        │  af CLI     │        │  af dashboard  │      (sibling clients,
        │  (cobra)    │        │  (Bubble Tea)  │       equal peers)
        └─────┬──────┘        └───────┬────────┘
              └──────────┬────────────┘
                         ▼
              ┌─────────────────────┐
              │  internal/core       │
              │  definitions · state │
              │  reconciliation ·    │
              │  lifecycle ops       │
              └──────┬───────┬──────┘
                     │       │
                     ▼       ▼
             ┌──────────┐ ┌──────────────────┐
             │  SQLite   │ │  SessionBackend   │
             │ (identity,│ │  = internal/tmux  │
             │  history) │ │  (tmux -L af)     │
             └──────────┘ └────────┬─────────┘
                                    ▼
                     detached tmux sessions, one per
                     agent/service, each running its
                     harness process tree in its workdir
```

## Why tmux

Managed harnesses (Claude Code, Codex, ...) are full-screen TUI apps.
tmux gives us, for free, the four things that are genuinely hard:

1. **Attach fidelity** — raw-mode passthrough, resize, alt-screen.
   `af attach` execs into `tmux attach`; there is no proxy to get wrong.
2. **Crash isolation** — sessions live in the tmux server, so `af`
   crashing or upgrading cannot take an agent down.
3. **Rendered previews** — `capture-pane -p` returns the drawn screen,
   exactly what the dashboard preview needs.
4. **No daemon** — tmux *is* the long-running process.

All sessions run on a dedicated socket (`tmux -L af`) with a locked
server config (`-f /dev/null`, options set explicitly): `remain-on-exit
on`, `status off`, `exit-empty off`, `default-terminal tmux-256color`,
`history-limit 50000`, `default-shell /bin/sh`. The user's own tmux and
`.tmux.conf` are never touched.

Two of those options carry real design weight:

- **`remain-on-exit on`** solves the daemonless exit-code trap: nothing
  `wait()`s on the payload, so dead panes persist until reconciliation
  reads `pane_dead_status`, records the exit code, and kills the
  session.
- **`default-shell /bin/sh`** guarantees rendered commands get POSIX
  `sh -c` semantics (pipes, quoting, `K=V` prefixes) even when the
  user's login shell is fish or csh.

Everything above `internal/tmux` talks to a small `SessionBackend`
interface (create/attach/capture/send/liveness/kill). A native-PTY
backend could be added behind it without touching core, cli, or tui.

## Reconciliation

For every non-terminal session in the DB, one pass:

1. tmux session missing → `failed` (ended, no exit code).
2. Pane dead → `exited(pane_dead_status)`, then `kill-session`.
3. Log file grew with *meaningful* text → `working`, update
   `last_active` (this also clears `awaiting-input`).
4. Status `awaiting-input` → left alone until output clears it.
5. Otherwise: past `idle_threshold` → `idle`; a `starting` session
   stays `starting` until its first output; anything else → `working`.

Two deliberate refinements over the naive "log grew ⇒ working":

- **Meaningful growth.** Idle TUI harnesses animate (spinners, pulsing
  logos), which writes escape bytes to the log forever. Growth only
  counts if the appended bytes contain actual text, so `idle` and
  `awaiting-input` stay truthful under animation.
- **Per-session fault isolation.** A session whose tmux queries race
  away mid-pass (killed externally at just the wrong moment) is skipped
  until the next pass rather than failing the whole command.

Reconciliation is idempotent and safe to run concurrently (SQLite WAL,
`busy_timeout`, last-writer-wins on status) — the CLI and an open
dashboard routinely interleave passes.

## Status detection tiers

| Tier | Source | Sets |
|---|---|---|
| T0 | tmux queries (`has-session`, `pane_dead`) | `exited`, `failed` |
| T1 | log-file growth vs a stored high-water mark | `working`, `idle` |
| T2 | harness adapters calling `af signal` | `awaiting-input` |

T2 refines but never replaces T0/T1: `awaiting-input` is set by a hook
(e.g. Claude Code's `Stop` hook) and cleared automatically by the next
observed output. `af` injects `AF_SESSION_ID` / `AF_SESSION_NAME` into
every session's environment so hooks need zero per-session config.

## Persistence

- SQLite via `modernc.org/sqlite` (pure Go, no CGO) at
  `<data_dir>/agentfactory.db`. Two tables — `definitions`, `sessions` —
  map fields as JSON text, times as RFC3339Nano (sub-second precision
  matters to the T1 idle heuristic).
- Compatibility is judged by *shape* (required columns present), not by
  `PRAGMA user_version`, so a data dir can be shared across builds that
  version the schema differently. `af doctor` reports the version.
- Session output streams to `<data_dir>/logs/<id>.log` from creation
  via `pipe-pane`, so `af logs` works for never-attached sessions.

## The CLI/TUI seam

The dashboard's `:` command bar does **not** shell out — it builds a
fresh cobra root and executes it in-process, capturing output into the
footer. This is why every command writes to `cmd.OutOrStdout()` instead
of `os.Stdout`, and why the CLI and TUI can never drift apart: they are
the same command tree.

Attach is the one special case in each world:

- Bare `af attach` replaces the process with tmux (`syscall.Exec`).
- The dashboard tears down Bubble Tea, fork/execs `tmux attach`, waits,
  and re-initializes on detach.

## Exit codes are API

| Code | Meaning |
|---|---|
| 0 | success |
| 1 | runtime error |
| 2 | usage error |
| 3 | session or definition not found |
| 4 | environment problem (tmux missing/old, DB unopenable) |

Errors carry their code from the point of origin
(`core.Errf(code, ...)` → `CodedError`); `main` unwraps exactly one
integer from whatever bubbles up. Scripts may rely on these, as may
`status --json` field names — both are contracts, not conveniences.

## Testing strategy

Real agent harnesses can't run in CI, so integration tests drive the
`custom` harness with small shell fixtures (`testdata/fixtures/`) that
simulate the behaviors that matter: continuous output (`working`), going
quiet (`idle`), exiting with a known code, echoing stdin (`send`),
trapping SIGTERM (close escalation), and spawning children that outlive
their parent (process-group kills). Every test runs on a throwaway
socket (`af-test-<random>`) with a temp data dir and kills its tmux
server on cleanup. Core logic (reconciliation transitions, config
precedence, template rendering, name suffixing) is unit-tested against
a mock backend — the `SessionBackend` interface exists partly for this.
