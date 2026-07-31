# Agent tools

Recommended host toolchain for an agentic SDLC with this template:

| Tool | Job | Install / wire |
|------|-----|----------------|
| [rtk](https://github.com/rtk-ai/rtk) | Compress noisy shell/`go test`/`git` output before it hits the model | CLI + agent hook |
| [codegraph](https://github.com/colbymchenry/codegraph) | Local code knowledge graph over MCP (navigate, impact) | CLI + MCP + per-project index |
| [ast-grep](https://github.com/ast-grep/ast-grep) | Structural (AST) search & rewrite — not text grep | CLI (`ast-grep` / `sg`) |

These are **host** tools, not Go module dependencies. CI does not require them.

## One-shot setup

From the project root:

```bash
make agent-tools
# or
./scripts/setup-agent-tools.sh
```

Check without installing:

```bash
make agent-tools-check
```

Then **restart** your agent (Cursor, Claude Code, …) so hooks/MCP reload.

> **Trust boundary:** setup installs host software globally and may execute
> current upstream package or HTTPS installer content. Review this script and
> the linked upstream projects before running it in a controlled environment.
> CI never invokes these installers; `make agent-tools-check` is local-only.

## When to use which

| Situation | Prefer |
|-----------|--------|
| `go test`, `git diff`, `golangci-lint`, long `rg` output | **rtk** (automatic if hooked; or `rtk go test ./...`) |
| “Where is this defined / who calls it / blast radius?” | **codegraph** MCP tools |
| “Find/rewrite every place that *looks like this syntax*” | **ast-grep** |

Do **not** use ast-grep for ordinary navigation — that is codegraph’s job.

## Per-tool notes

### rtk

```bash
rtk init -g --agent cursor --auto-patch   # Cursor
rtk init -g --auto-patch                  # Claude Code
```

Hooks rewrite Bash tool calls (e.g. `git status` → `rtk git status`). Built-in editor Read/Grep tools may bypass hooks; use shell/`rtk read`/`rtk grep` when you want compression there.

### codegraph

```bash
codegraph install --yes   # once per machine: wire MCP into agents
codegraph init            # once per project: build .codegraph/ (gitignored)
```

Auto-sync keeps the graph fresh while you edit. Re-run `codegraph index --force` only if the index looks wrong.

### ast-grep

Go is a built-in language. Examples:

```bash
# Find err-check shapes
ast-grep -p 'if err != nil { return err }' -l go .

# Interactive rewrite (example)
ast-grep -p 'context.TODO()' -l go --rewrite 'context.Background()' --interactive .
```

Optional project config: `sgconfig.yml` + `rules/` for checked-in structural rules.

Claude Code skill (optional):

```bash
npx skills @ast-grep/agent-skill
```

## Agent contract

See [AGENTS.md](../AGENTS.md) for the short “use these tools” rules agents should follow.
