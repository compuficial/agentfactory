# Claude instructions

Follow [AGENTS.md](./AGENTS.md) for layout, the agent-agnostic and
daemonless invariants, quality gates, commit conventions, and the
testing mandate.

Before finishing any task that touches Go source, config, or docs:

```bash
make precommit
```

Never author AI or tool attribution in commit messages. Never commit
planning artifacts (specs, design docs, working plans).
