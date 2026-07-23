# Security Policy

## Reporting a vulnerability

Please report suspected vulnerabilities privately via
[GitHub security advisories](https://github.com/compuficial/agentfactory/security/advisories/new)
rather than opening a public issue. You'll get an acknowledgment within
a few days.

## Scope notes

`af` is a local-first tool: it manages processes you launch, on your
machine, with your privileges. Reports we consider in scope include
command injection through harness templates or config, sessions escaping
their dedicated tmux socket, and secrets leaking into logs or `--json`
output (session environments are deliberately excluded from the JSON
schema — a leak there is a bug).
