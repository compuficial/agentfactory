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
make precommit             # tidy + text/docs/shell + format + lint (--fix) + go test -race
make ci                    # precommit (report-only lint) + govulncheck + crossbuild + clean tree
make cover                 # coverage summary
make fuzz                  # byte-parser fuzzers (~20s)
```

Run `make precommit` before claiming work done (tests need tmux >= 3.2
installed). CI runs the same gates plus the test matrix on Linux and
macOS. Never add `//nolint` or disable linters to pass — fix the code.

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
5. **Keep tmux behind `SessionBackend`.** Core and TUI code use only the
   interface; CLI composition and environment probing are the narrow
   implementation-aware exceptions. Extend the interface for new runtime
   substrate capabilities.
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
- `gofumpt` + `goimports`; exported identifiers carry doc comments.

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

## Commits, versions, releases

Only touch git when asked (hard rule 1). When you do, the repo runs a
deterministic release loop:

| Spec | Rule |
|------|------|
| [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) | Every commit message follows this format |
| [Semantic Versioning](https://semver.org/) | Versions are `MAJOR.MINOR.PATCH` |
| [release-please](https://github.com/googleapis/release-please) | Automates the changelog, version bumps, and tags; goreleaser publishes the binaries |

- `feat:` new user-facing capability (minor) · `fix:` bug fix (patch) ·
  `!` or `BREAKING CHANGE:` → major.
- `docs:`, `chore:`, `refactor:`, `test:`, `ci:`, `build:` produce no
  changelog entry / no release (per `release-please-config.json`).
- **Never author AI or tool attribution** in commit messages.
- **Never commit planning artifacts** — specs, design docs, working plans.

Merge conventional commits to `main`; release-please opens a release PR;
merging it publishes the tag, notes, and binaries. Don't hand-bump
versions or edit tags.

## Agent skills (mattpocock + superpowers)

```sh
make agent-skills        # global install via npx skills
```

| Need | Prefer |
|------|--------|
| Align before building | `/grill-me` or `/grill-with-docs` |
| Plan → implement → review loop | Superpowers (auto-triggers) |
| Explicit TDD on a slice | Superpowers TDD **or** `/tdd` (not both at once) |
| Hard bug | `/systematic-debugging` |

Don't skip grilling on ambiguous work. Details:
[docs/agent-skills.md](docs/agent-skills.md).

## Agent host tools (rtk + codegraph + ast-grep)

```sh
make agent-tools         # install/wire; make agent-tools-check for status
```

| Need | Tool |
|------|------|
| Smaller shell / test / git / lint output | **rtk** |
| Navigate symbols, callers, blast radius | **codegraph** (MCP, before broad Grep/Read) |
| Find or rewrite a syntax shape across files | **ast-grep** (`ast-grep -p '...' -l go`) |

Discover structure with codegraph, not endless file reads; compress noisy
output with rtk; use ast-grep only for structural search/codemods.
Details: [docs/agent-tools.md](docs/agent-tools.md).
