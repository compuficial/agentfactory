# Development

## Loop

1. `git checkout -b fix/thing`
2. Change it test-first (see [testing.md](testing.md)).
3. Pass the gates CI enforces: `gofmt -l .` (empty), `go vet ./...`, `go test -race ./...`.
4. Commit with a [Conventional Commit](https://www.conventionalcommits.org/) — the prefix sets the version bump.
5. PR → CI (staticcheck + tests on Linux/macOS) → merge to `main`.

## Commit prefix → version

| prefix | bump |
|---|---|
| `fix:` | patch |
| `feat:` | minor |
| `feat!:` / `BREAKING CHANGE:` footer | major |
| `perf:` / `revert:` | patch |
| `docs:` `refactor:` `chore:` `test:` `ci:` | no release |

## Releasing

Merging to `main` ships **nothing**. release-please opens a `chore(main): release X.Y.Z`
PR and keeps it current as you merge work. **Merging that PR** tags the release and
goreleaser attaches the binaries — the only "ship it" action. No manual tagging.

One `main` is enough: it accumulates freely, and the release PR is the gate. No `develop` branch needed.

## Website

Edit `site/`, push to `main` → Pages redeploys `agentfactory.sh` in ~a minute.
Preview locally: `python3 -m http.server 8787` in `site/`.

## Don't commit

Planning artifacts (`docs/design/`, `docs/superpowers/` are gitignored) or secrets.
