# Contributing to AgentFactory

Thanks for your interest in `af`. This guide covers how the project thinks,
what belongs in it, and how to get a change merged.

- [Report a bug or request a feature](https://github.com/compuficial/agentfactory/issues)
- [Open a pull request](https://github.com/compuficial/agentfactory/pulls)
- Architecture: [docs/architecture.md](docs/architecture.md)

## What is AgentFactory?

`af` is a daemonless, local-first session manager for terminal AI agents —
Claude Code, Codex, Grok, OpenCode, or any command. It layers identity,
lifecycle, trustworthy live status, and agent-to-agent coordination on top of
tmux, which does the actual multiplexing. It's a Go CLI plus a Bubble Tea TUI,
and it's deliberately small: we'd rather do a few things well than grow into a
platform.

## Ways to contribute

| Type | Examples |
|---|---|
| **Report** | File a bug with `af doctor` output and your tmux version; note a harness that misbehaves |
| **Fix** | Bug fixes, flaky-test fixes, correctness edges in reconciliation |
| **Build** | A new coordination primitive, a TUI improvement, a second `SessionBackend` |
| **Harness** | Add or tune support for an agent CLI — this is *data*, not code (see [Adding a harness](#adding-a-harness)) |
| **Review** | Read open PRs; a second pair of eyes on the tmux boundary is always welcome |
| **Document** | Tighten the docs, add an example, fix a rough edge in the README |

## Design philosophy

Read [docs/architecture.md](docs/architecture.md) for the full picture. In
short, five principles govern what "fits":

1. **Daemonless.** `af` has no background process. tmux holds the live
   sessions, SQLite holds identity and state, and commands that observe or
   act on sessions reconcile the two before answering. Never add an `af`
   daemon, watcher, or other background service.

2. **tmux lives behind an interface.** Core and TUI substrate access goes
   through `SessionBackend`. CLI composition and the environment probe are
   the narrow places that construct/inspect the tmux implementation. New
   runtime capabilities extend the interface rather than leaking tmux into
   lifecycle or dashboard logic.

3. **Harnesses are data, not code.** A harness is a command template, env,
   quit keys, detection patterns, and wired files — pure data. Supporting a
   new agent CLI is a config entry, never a code path. There is no
   `if harness == "claude-code"` anywhere, and there never will be. Anything
   one harness can do, every harness can do through the same data.

4. **Contracts are API.** Exit codes (`0/1/2/3/4/5`), the `status --json`
   field set, the status names, and session addressing are scriptable
   contracts. Changing them is a breaking change and needs a strong case.

5. **Less code, boring solutions.** Elegant and simple beats clever and
   general. Fewer lines, fewer abstractions, fewer dependencies. If a change
   adds a lot of surface area, expect a conversation about whether it earns it.

## What belongs in af?

**In scope:** managing terminal agent sessions `af` started — identity,
lifecycle, status detection (as data), coordination primitives, harness
definitions, CLI and TUI ergonomics, and substrate work behind
`SessionBackend`.

**Out of scope** (project non-goals — propose these elsewhere):

- Automatic task decomposition, routing, or orchestration.
- Model/backend abstraction inside `af` — harnesses talk to models; `af` talks
  to harnesses. `--model` is passthrough.
- Process discovery — `af` manages only sessions it started.
- Sandboxing or tool-execution isolation.
- Multi-user, remote, or multi-node management.
- A plugin system or harness marketplace.
- Windows support. Linux-first; macOS works incidentally.

## Adding a harness

The most common contribution, and the one with a fixed shape. A built-in
harness is a data entry in `Builtins()` (`internal/core/harness.go`):

- [ ] Add the entry: `name`, command template (Go `text/template`), `QuitKeys`
      (empty = signal-only close), and — optionally — `Detect` patterns and
      wired `Files`.
- [ ] Shell-quote every inserted value with `{{shellquote ...}}`; commands
      are trusted configuration executed via `sh -c`.
- [ ] Keep it **data**. No Go branches on the harness name, anywhere. If you
      find yourself needing one, the mechanism is wrong — make it a field.
- [ ] Add a row to the built-in harnesses table in the [README](README.md).
- [ ] If you add `Detect` patterns, treat them as heuristics: they match a
      rendered screen and can break when a TUI changes. Flag them for human
      verification against the live CLI in your PR.
- [ ] If it exercises new behavior, add a fixture (real agent CLIs can't run
      in CI — see [Testing](#testing)).

Users add or override harnesses the same way via
`~/.config/agentfactory/config.yaml` — a built-in is just a default.

## Commit messages & changelog

We use [Conventional Commits](https://www.conventionalcommits.org/) with
[release-please](https://github.com/googleapis/release-please), which computes
the [SemVer](https://semver.org) bump and writes
[CHANGELOG.md](CHANGELOG.md) automatically. **Never edit CHANGELOG.md by
hand.**

```
<type>(<scope>): <short description>
```

| Type | SemVer | When to use |
|---|---|---|
| `feat` | minor | A new capability, flag, or harness |
| `fix` | patch | A bug fix or correctness fix |
| `perf` | patch | A performance improvement |
| `revert` | patch | Undo a previous change |
| `refactor` | — | Code restructuring, no behavior change (no release) |
| `docs` | — | Docs or website only (no release) |
| `chore` / `test` / `ci` | — | Tooling, tests, CI (no release) |
| `feat!` / `fix!` or `BREAKING CHANGE:` footer | major | A breaking contract change |

```
feat(wait): add --any for multi-session waits
fix(tmux): capture session output from the first byte
docs: rewrite the README around the problem it solves
```

## Branch naming

Prefix + a kebab-case description of the change:

| Prefix | When to use |
|---|---|
| `fix/` | Bug fixes and corrections |
| `feat/` | New features and harnesses |
| `chore/` | Tooling, CI, deps, maintenance |
| `docs/` | Documentation and website |

```
fix/reconcile-harvest-race
feat/wait-any-multi-session
```

## Pull request process

Contributions are licensed under the project's [AGPL-3.0](LICENSE). There is
no CLA.

**Scope.** One logical change per PR. Keep refactors separate from behavior
changes — a PR that both moves code around and changes what it does is hard to
review and risky to merge. For large work, stack focused PRs
(`feat(part 1): …`, `feat(part 2): …`).

1. **Branch** off `main`: `git checkout -b fix/thing`.
2. **Make the change**, respecting the [design philosophy](#design-philosophy).
   Comments explain *why*, not *what*; most code needs none.
3. **Add tests** with the change, not promised for later (see
   [Testing](#testing)).
4. **Update docs** if you touched behavior, flags, config, or a contract (see
   [Documentation](#documentation)).
5. **Open the PR against `main`.** CI runs on it; a maintainer reviews. There
   is one `main` branch — merging to it ships nothing on its own (see below).

**Releases are automatic.** Merging a `feat`/`fix` to `main` doesn't cut a
release — release-please opens a `chore(main): release X.Y.Z` PR and keeps it
current. Merging *that* PR tags the version and attaches binaries. No manual
tagging, ever.

## Testing

Test-driven is the norm. The suite's job is confidence in three things:
statuses are trustworthy, lifecycle operations are safe, and the scriptable
surface (JSON, exit codes) stays stable. Full design:
[docs/testing.md](docs/testing.md).

| Layer | Where | Substrate |
|---|---|---|
| Unit | `internal/core/*_test.go` | mocked `SessionBackend` |
| Byte-parser fuzz | `internal/core/fuzz_test.go` | none |
| Backend | `internal/tmux/tmux_test.go` | real tmux, throwaway socket |
| CLI integration | `internal/cli/*_test.go` | real tmux + fixture scripts |
| Golden | `testdata/status_golden.json` | — (the `--json` schema) |
| TUI | `internal/tui/tui_test.go` | `tui.NewTestModel` |

Two rules that keep the suite honest:

- **Isolation.** Anything touching tmux runs on a throwaway socket
  (`af-test-*`) and a temp data dir, torn down afterward. Never touch the
  user's real socket or `~/.local/share/agentfactory`. The existing helpers do
  this — use them.
- **Fixtures over mocks for behavior.** Real agent CLIs can't run in CI, so
  `testdata/fixtures/*.sh` simulate the behaviors that matter — one script per
  behavior. To test a new behavior, add a fixture.

### Pre-commit gate

One command runs the whole local gate — module tidiness, repository text,
shell syntax, local documentation links, gofumpt/goimports, golangci-lint
(with `--fix` locally), and the race-enabled test suite:

```sh
make precommit
```

`make ci` runs that gate with report-only lint, then adds govulncheck, a
crossbuild smoke, and a clean-tree check. GitHub Actions runs the same
checks as split jobs on Linux and macOS. Tools are pinned in
`tools/go.mod`; lint configuration is `.golangci.yml`. **Never add
`//nolint` or disable a linter to get green — fix the code.**

`make cover` gives a coverage summary; `make fuzz` runs the byte-parser
fuzzers. `make hooks` installs the optional pre-commit + commit-msg
hooks (conventional-commit enforcement).

### PR testing checklist

- [ ] Tests accompany the change (fixtures over mocks for behavior)
- [ ] `make precommit` passes (text/docs/shell + format + lint + race tests)
- [ ] Harness knowledge stayed *data* — no Go branches on a harness name
- [ ] Contract changes (exit codes, `--json` fields, status names) are called
      out explicitly in the PR
- [ ] No new dependencies without prior discussion in an issue
- [ ] Docs updated for any behavior, flag, config, or contract change
- [ ] Manual check: built `af`, ran the change (`af doctor`, `af status`)

## Documentation

Keep docs concise and practical — examples over explanations. When you change
something, update its docs in the same PR:

| What you changed | Update these |
|---|---|
| A command or flag | [README.md](README.md) + [AGENTS.md](AGENTS.md) |
| A status, exit code, or `--json` field | README status/scripting tables + [docs/architecture.md](docs/architecture.md) |
| Blocked-agent detection | [docs/detection.md](docs/detection.md) |
| The test approach | [docs/testing.md](docs/testing.md) |
| Quality gates or audited limitations | [docs/quality.md](docs/quality.md) |
| Architecture or the backend boundary | [docs/architecture.md](docs/architecture.md) |
| The website | [site/](site/) (deploys to agentfactory.sh on merge) |

Coding agents working in this repo should read [AGENTS.md](AGENTS.md). Do
**not** edit [CHANGELOG.md](CHANGELOG.md) by hand — release-please owns it.

## Questions?

- Bugs and feature requests: [Issues](https://github.com/compuficial/agentfactory/issues)
- Security: report privately per [SECURITY.md](SECURITY.md), not a public issue.

Thanks for contributing to AgentFactory.
