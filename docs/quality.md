# Quality and audit status

This page is the maintained record of AgentFactory's automated quality
gates and known engineering limitations. It was established by a
repository-wide code, test, tooling, security, and documentation audit on
2026-07-31. Report suspected vulnerabilities privately through
[SECURITY.md](../SECURITY.md), not by expanding this public list.

## Enforced gates

`make precommit` is the local and hook gate:

| Gate | Enforcement |
|---|---|
| Module consistency | `go mod tidy` in the main and tools modules |
| Repository text | US-English `misspell` across docs, YAML, HTML, and shell |
| Shell syntax | `bash -n` / `sh -n` for scripts, fixtures, and the installer |
| Workflow supply chain | every remote action uses a full commit SHA with a reviewable version label |
| Documentation links | repository-local Markdown targets must exist |
| Go formatting | `gofumpt` and `goimports` in report-only check mode |
| Static analysis | the high-signal suite in [`.golangci.yml`](../.golangci.yml), with no blanket security-rule disables |
| Behavior and races | `go test -race ./...`, including real tmux integration tests |

`make ci` adds `govulncheck`, Linux/macOS ARM64 cross-build smoke tests,
and a clean-tree check. GitHub Actions runs the equivalent checks in
split jobs and runs the race suite on Linux and macOS. Workflow actions and
the conventional-commit hook are pinned to full commit SHAs; adjacent version
comments keep automated update reviews readable.

The 69-linter suite emphasizes correctness, resource handling, SQL
row/error handling, context-aware I/O, security, API clarity, modern Go
constructs, and maintainability. It also enforces the core/TUI backend
boundary and bans direct process writers outside the process entry point.
Source/path exclusions are narrow and documented in `.golangci.yml`;
test-only complexity exclusions do not apply to production code.

## Resolved audit findings

- Shell-inserted harness values are quoted, generated-file names are
  validated, and unsafe state paths no longer produce malformed SQLite
  connection strings.
- State directories, databases, sidecars, and logs are hardened to
  user-only permissions.
- tmux setup and final-persistence failures roll back created sessions;
  session removal preserves the log when the database delete fails.
- Reconciliation uses optimistic updates so stale passes cannot overwrite
  explicit signals or terminal rows. Signal writes also advance the log
  watermark atomically.
- Split terminal-control sequences are retained until complete, default
  log reads are bounded, waits honor cancellation, and invalid durations
  or negative line/timeout values fail with documented exit codes.
- Dashboard refreshes reject stale asynchronous results, destructive
  confirmations re-resolve their target, command-bar execution is bounded,
  and substrate operations now use the core backend interface.
- Completion candidates, config diagnostics, root-session frictionless
  launch behavior, tmux error classification, and resolved-config output
  were brought back into contract.
- CI and release workflows use current supported action releases pinned to
  immutable commits rather than mutable major tags.

## Known limitations

These are accepted follow-up work, not claims that the behavior is ideal.

| ID | Severity | Area | Limitation and current mitigation |
|---|---|---|---|
| Q-01 | Medium | Persistence | Concurrent opens can still choose the same display name, and schema migrations are not wrapped in one transaction. IDs remain unique and SQLite serializes individual writes. |
| Q-02 | Medium | Persistence | Malformed persisted JSON/timestamps can fall back to zero values, and a few database-corruption paths do not yet carry the standard coded-error classification. `doctor` checks schema shape/version but is not a full integrity audit. |
| Q-03 | Medium | Secrets | Dashboard environment masking is a key-name heuristic and can miss unusually named secrets or mask harmless values. Session env is excluded from `status --json`; `defs --json` intentionally includes definition env and is documented as sensitive. |
| Q-04 | Medium | Log following | Follow-mode terminal sanitization is chunk-local and does not yet preserve split escape sequences across reads or detect file truncation/rotation. Non-follow reads use the stateful scanner and bounded defaults. |
| Q-05 | Low | Cancellation | `wait` and MCP waits honor request cancellation; close/kill harvest loops and attach round-trips are not yet driven by caller contexts. Their internal timeouts remain bounded. |
| Q-06 | Low | MCP | A terminal-session `peek` and some store/transport failures are less consistently classified than their CLI equivalents. Stable successful response shapes remain covered by tests. |
| Q-07 | Low | TUI parsing | The definition picker and tiny-terminal layouts are not viewport-complete, and command-bar word splitting does not preserve empty quoted arguments or reject every unterminated quote. The allowlist prevents blocking/process-replacing commands. |
| Q-08 | Low | Configuration | Failure to discover the user's home directory can silently skip the default config path. Environment overrides and errors encountered after a path is found still return coded validation failures. |
| Q-09 | Low | Diagnostics | `af doctor` may start an otherwise empty dedicated tmux server while probing the socket. It does not start an `af` daemon or touch the user's normal tmux socket. |
| Q-10 | Low | Backend seam | Core and TUI are backend-agnostic, but CLI composition and `doctor` still know the tmux implementation. A second backend therefore needs composition/probe work, not only a new interface implementation. |
| Q-11 | Low | Tooling | Shell, workflow, and Markdown checks cover syntax, immutable action refs, spelling, and local links but not full `shellcheck`, `actionlint`, or Markdown style semantics. Those tools are not added under the project's no-new-dependencies rule. |
| Q-12 | Low | Coverage | The suite has no hard line-coverage threshold and exercises representative rather than every CLI command end to end. Contract goldens, race tests, deterministic fake-tmux failures, and real-tmux lifecycle flows cover the highest-risk paths. |
| Q-13 | Low | Cleanup | If deleting a finished session's log fails after its database row is removed, the log can remain orphaned. Database consistency is preferred over claiming a failed removal while the row is already gone. |
| Q-14 | Medium | Host tooling | Optional agent-tool and skill setup intentionally resolves current third-party packages or upstream installer scripts and may install them globally. These paths never run in build/CI, HTTPS is enforced for direct downloads, and the trust boundary is called out in the setup documentation. |

## Linter boundaries

Two plausible checks are intentionally not enabled globally:

- `contextcheck` requires a repository-wide context-propagation design for
  lifecycle APIs; enabling it piecemeal would encourage placeholder
  contexts. Request-driven wait paths now propagate cancellation directly.
- `exhaustive` is noisy for switches that deliberately use default handling
  for forward-compatible status/config values. Contract enums remain pinned
  by targeted tests and golden data.

Other disabled linters are excluded by category rather than oversight:

- `wrapcheck` and `err113` conflict with the coded-error contract: internal
  layers intentionally propagate an already classified error instead of
  repeatedly wrapping it or replacing dynamic messages with sentinels.
- `paralleltest` and `tparallel` are unsuitable as mandatory rules for
  timing- and process-sensitive tmux integration tests.
- `prealloc`, `tagalign`, `varnamelen`, `nlreturn`, and `wsl_v5` are
  style/micro-optimization policy rather than correctness. Formatting,
  readability, complexity, and maintainability are already enforced by
  gofumpt/goimports, revive, gocritic, gocyclo, and maintidx.
- Framework-specific checks (Ginkgo, Testify, slog, OpenTelemetry,
  Prometheus, protobuf, and database clients not used here) are not enabled.

`nilerr` and `nilnil` are enabled with three exact, documented exceptions:
two reconciliation race paths deliberately retry on the next pass, and a
nil live-session match is the launcher's established "start new" result.

Revisit these boundaries when the related API design changes. Do not add a
blanket linter disable or `//nolint` to bypass a finding.
