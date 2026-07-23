# AGENTS.md

Operational guide for coding agents (and humans in a hurry) working on
this repository.

## What this is

`af` (AgentFactory) is a daemonless, local-first agent session manager
built on tmux. Go CLI + Bubble Tea TUI. Design rationale:
[docs/architecture.md](docs/architecture.md).

## Commands

```sh
make build                 # build ./af
go test ./... -race        # full suite; needs tmux >= 3.2 installed
go vet ./...               # must be clean
gofmt -l .                 # must print nothing
make cover                 # coverage summary
make fuzz                  # byte-parser fuzzers (~20s)
```

Suite design and how to extend it: [docs/testing.md](docs/testing.md).

Run a single package: `go test ./internal/core/ -run TestName -v`.

Tests create their own throwaway tmux sockets and temp data dirs; they
never touch the user's `af` socket or `~/.local/share/agentfactory`.
Keep it that way.

## Repo map

| Path | What lives there |
|---|---|
| `cmd/af/` | `main()` only — calls `cli.Execute` |
| `internal/cli/` | cobra command tree; one file per command area (incl. the `af mcp` stdio server) |
| `internal/core/` | domain logic: model, store (SQLite), reconciliation, lifecycle ops, harness templates |
| `internal/tmux/` | the only package that knows tmux exists (`SessionBackend` impl) |
| `internal/tui/` | Bubble Tea dashboard |
| `internal/config/` | YAML + `AF_*` env + defaults, precedence handling |
| `testdata/fixtures/` | shell scripts that simulate agent behavior in tests |

## Hard rules

1. **Never commit, push, or touch git state** unless explicitly asked.
2. **Exit codes are API**: 0 success · 1 runtime · 2 usage · 3 not
   found · 4 environment · 5 wait timeout. Attach codes at the error
   source with `core.Errf(code, ...)`; don't map by string matching.
3. **`status --json` field names/shapes are API** (`internal/core/json.go`
   is the DTO — never marshal `AgentSession` directly; it would leak
   `env`, which holds secrets).
4. **CLI output goes to `cmd.OutOrStdout()` / `cmd.ErrOrStderr()`**,
   never `os.Stdout`. The TUI command bar runs commands in-process
   through the same cobra root and captures those writers.
5. **Nothing above `internal/tmux` mentions tmux.** New backend
   capabilities extend the `SessionBackend` interface (spec §6).
6. **tmux is the source of truth for liveness; SQLite for identity and
   history.** Any command that answers questions about sessions must
   reconcile first (`newApp` does this — use it).
7. **No new dependencies** without being asked. Prefer stdlib.
8. **Tests accompany behavior changes.** Simulate harnesses with
   `testdata/fixtures/` shell scripts through the `custom` harness —
   real agent harnesses can't run in CI.

## Style

- Simplicity beats generality. No speculative abstractions, no
  interfaces with one implementation (`SessionBackend` is the mandated
  exception), no frameworks.
- Comments explain constraints and *why* — not what the next line does.
- Match the existing voice in help strings: short, lowercase-ish,
  imperative ("Print a session's captured output").
- `gofmt`; exported identifiers carry doc comments.

## Verifying a change end-to-end

```sh
go build -o /tmp/af ./cmd/af
SOCK=af-dev-$$; DD=$(mktemp -d)
/tmp/af --socket $SOCK --data-dir $DD doctor
/tmp/af --socket $SOCK --data-dir $DD open --cmd 'while :; do date; sleep 1; done' --name tick
/tmp/af --socket $SOCK --data-dir $DD status
/tmp/af --socket $SOCK --data-dir $DD peek tick
/tmp/af --socket $SOCK --data-dir $DD kill tick
tmux -L $SOCK kill-server; rm -rf $DD    # cleanup
```

The dashboard (`af dashboard`) needs a real terminal; drive it in tests
via `tui.NewTestModel` (see `internal/tui/tui_test.go`) instead.
