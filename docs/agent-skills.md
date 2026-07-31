# Agent skills

This template recommends two skill packs for an agentic SDLC:

| Pack | Repo | Role |
|------|------|------|
| **Skills for Real Engineers** | [mattpocock/skills](https://github.com/mattpocock/skills) | Small, composable skills: grill for alignment, domain docs, TDD, diagnose, architecture |
| **Superpowers** | [obra/superpowers](https://github.com/obra/superpowers) | End-to-end methodology: brainstorm → plan → TDD → review → finish branch |

They install into your **coding agent** (Cursor, Claude Code, Codex, …), not into `go.mod`.

## One-shot setup

```bash
make agent-skills          # global install via npx skills (default)
make agent-skills-project  # copy into this repo instead
make agent-skills-check    # list what is installed
```

Or:

```bash
./scripts/setup-agent-skills.sh
./scripts/setup-agent-skills.sh --project
```

Requires [Node.js](https://nodejs.org/) (`npx`).

> **Trust boundary:** these commands resolve `skills@latest` and install
> third-party code into an agent or this repository. Even
> `make agent-skills-check` invokes `npx` and may fetch current registry
> content. Review the upstream packs and pin an approved installer version in
> the script when your environment requires reproducible host tooling.

### After install (required once per repo)

In the agent chat, run:

```text
/setup-matt-pocock-skills
```

That configures issue tracker, triage labels, and where docs (`CONTEXT.md`, ADRs) live — other Matt Pocock engineering skills read that config.

### Marketplace alternatives

If you prefer agent-native plugins instead of (or in addition to) `npx skills`:

**Cursor**

```text
/add-plugin superpowers
```

**Claude Code**

```bash
claude plugins install mattpocock-skills
# or in-session:
# /plugin install mattpocock-skills
# /plugin install superpowers@claude-plugins-official
```

Matt’s README warns: **plugin + skills.sh copy of the same pack = duplicated skills**. Pick one install path per pack.

## How they work together

| Phase | Prefer |
|-------|--------|
| Align on *what* to build | Matt: `/grill-me` or `/grill-with-docs` (builds shared language / ADRs) |
| Default delivery loop | Superpowers (brainstorm → plan → subagent/TDD → review → finish) |
| Explicit red-green loop | Either Superpowers `test-driven-development` or Matt `/tdd` — don’t run both on the same change |
| Hard bug | Matt `/diagnose` / `diagnosing-bugs`, or Superpowers `systematic-debugging` |
| Rescue messy design | Matt `/improve-codebase-architecture` |
| Navigate code cheaply | Host tools: codegraph + rtk + ast-grep ([agent-tools.md](./agent-tools.md)) |

Superpowers **auto-triggers** many workflows. Matt’s **user-invoked** skills (`/grill-me`, `/to-spec`, …) are for when you want a specific ritual. Use Matt’s grilling before large Superpowers implementation runs.

## Project artifacts these skills may create

Depending on `/setup-matt-pocock-skills` answers:

- `CONTEXT.md` — ubiquitous language / domain glossary  
- ADRs under a docs path you choose  
- Local issue/ticket files if you are not using GitHub/Linear  

Commit those when they are project truth. Do not commit agent-private skill caches.

## Updates

```bash
npx skills update
```

Plugin installs update through the agent’s marketplace/plugin updater.
