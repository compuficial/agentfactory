# Testing — suite design and harness

How af is tested, why each layer exists, and how to extend it. The
suite's job is confidence in three promises: **statuses are
trustworthy**, **lifecycle operations are safe**, and **the scriptable
surface (JSON, exit codes) is a stable contract**.

## Layers

| Layer | Where | Substrate | What it pins |
|---|---|---|---|
| Core unit tests | `internal/core/core_test.go` | mocked `SessionBackend` | Reconciliation transitions (§7.1 rules, sticky-state precedence, detection tiers), wait outcomes, harness rendering/files, ID and name rules |
| Byte-parser fuzzers | `internal/core/fuzz_test.go` | none | `ScanStreamEvents` and `SanitizeTerminal` never panic and never leak escape bytes, on arbitrary pty streams. Seeds double as regression cases on every plain `go test` run |
| Backend tests | `internal/tmux/tmux_test.go` | real tmux, throwaway socket | The substrate contract: create/liveness/capture/send round-trips, `remain-on-exit` exit-code harvest, attach env stripping, version parsing |
| CLI integration | `internal/cli/*_test.go` | real tmux + fixture scripts | Every command end-to-end through the cobra root: exit codes, `--json` shapes, coordination flows (wait/signal/terminal signals), close/kill escalation, MCP tool round-trips (in-memory client) |
| Golden files | `testdata/status_golden.json` | — | The normative §8.3 JSON schema, byte-for-byte |
| Config tests | `internal/config/config_test.go` | temp files | Precedence (flags > env > file > defaults), kill switches, warning-not-error policy |
| TUI tests | `internal/tui/tui_test.go` | `tui.NewTestModel` | Rendering reflects state; no real terminal needed |

## Invariants the suite enforces

- **Isolation**: every test that touches tmux runs on a throwaway
  socket (`af-test-*` / `af-tmuxtest-*`) with a temp data dir, torn
  down afterward. The user's `af` socket and
  `~/.local/share/agentfactory` are never touched. Keep it that way.
- **Fixtures over real harnesses**: real agent CLIs can't run in CI, so
  `testdata/fixtures/*.sh` simulate the behaviors that matter — one
  script per behavior (continuous output, quiet, exit codes, stdin
  echo, SIGTERM traps, process groups, quit commands, TUI redraws,
  terminal signals). To test a new behavior, add a fixture, not a mock.
- **Race-clean**: `go test -race ./...` is the gate; CI runs it on
  Linux and macOS.
- **Contract changes are loud**: JSON schema edits must update the
  golden file; exit-code changes must update `TestExitCodes`. Additive
  only, per the spec addenda.

## Running

```sh
make test     # full suite, -race (the gate)
make cover    # coverage summary (writes cover.out)
make fuzz     # byte-parser fuzzers, ~20s (seeds always run in make test)
go test ./internal/core -run TestReconcile -v    # one area
```

## Not machine-verifiable (by design)

Flagged for human acceptance instead of fake assertions: attach
fidelity with real full-screen harnesses, dashboard look/feel, the
Claude Code hook/detect data against the live TUI. Everything else
should have a test — if a change can only be verified by hand, say so
in its PR/summary rather than skipping verification silently.
