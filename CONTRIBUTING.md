# Contributing to AgentFactory

Thanks for your interest! `af` is a small, focused tool and we intend to
keep it that way: elegant, simple solutions are preferred over clever or
general ones, and less code is better than more.

## Development setup

You need Go 1.25+ and tmux ≥ 3.2 on `PATH`.

```sh
git clone <repo>
cd af
make build        # builds ./af
make test         # go test ./... -race
af doctor         # verify your environment
```

`make help` lists every target (`vet`, `fmt`, `tidy`, `install`,
`reset`, `fresh`, ...).

## Testing

- **Never touch real state.** Integration tests must run on a throwaway
  tmux socket (`af-test-<random>`) and a `t.TempDir()` data dir, and
  kill their tmux server in cleanup. The existing test helpers do this;
  use them.
- **No real harnesses.** Tests simulate agent behavior with the shell
  fixtures in `testdata/fixtures/` via the `custom` harness. If you need
  a new behavior (e.g. a process that ignores SIGTERM), add a fixture.
- Unit tests for core logic (reconciliation, config precedence, name
  suffixing) run against the mock backend in `core_test.go` — no tmux
  required.
- Everything must pass `go test ./... -race` and `go vet ./...`, and
  `gofmt -l .` must print nothing. CI enforces all three.

## Commits and releases

Commits follow [Conventional Commits](https://www.conventionalcommits.org/):
`feat:`, `fix:`, `docs:`, `refactor:`, `perf:`, `test:`, `chore:`, with
`!` or a `BREAKING CHANGE:` footer for breaking changes. They aren't
just style — [release-please](https://github.com/googleapis/release-please)
computes the [SemVer](https://semver.org) bump and writes
[CHANGELOG.md](CHANGELOG.md) from them: `fix` → patch, `feat` → minor,
breaking → major. Merging the release PR it maintains tags the release,
and goreleaser attaches binaries. No release steps are ever run by hand.

## What we look for in a change

- **Behavior contracts hold.** Exit codes (`0/1/2/3/4/5`), the
  `status --json` field set, and session addressing are scriptable API.
  Changing them is a breaking change and needs a strong case.
- **Scope.** One logical change per PR. Refactors separate from
  behavior changes.
- **Tests with the change**, not promised for later.
- **Comments explain *why*, not *what*.** Most code should need none.
- **No new dependencies** without prior discussion in an issue. The
  current set (cobra, bubbletea/lipgloss/bubbles, yaml, modernc sqlite)
  is deliberate and small.

## Architecture guardrails

Read [docs/architecture.md](docs/architecture.md) first. In short:

- `af` is daemonless. tmux holds live sessions, SQLite holds state,
  and every invocation reconciles the two before answering. Don't add
  background processes.
- Nothing above `internal/tmux` may know tmux exists; everything goes
  through the `SessionBackend` interface.
- The TUI and CLI are equal peers over `internal/core`. Command-bar
  commands route through the same cobra root in-process — so CLI
  commands write to `cmd.OutOrStdout()`, never `os.Stdout`.

## Reporting bugs

Include `af doctor` output, your tmux version, and — if a session
misbehaved — the relevant `af status --all --json` row. Logs live under
`~/.local/share/agentfactory/logs/<id>.log`.
