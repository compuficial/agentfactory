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

## Trust boundaries

- Harness `command` templates are trusted configuration executed through
  `sh -c`. Quote inserted values with the template `shellquote` helper;
  built-in harnesses do so for model and generated-file paths.
- The data directory is private (`0700`), and SQLite databases, sidecars,
  and session logs are restricted to the user (`0600`). Existing paths
  are hardened when opened.
- `status --json` deliberately omits session environments. Definitions
  are reusable launch configuration, so `defs --json` includes their env
  map and must be handled as sensitive output.
- The dashboard masks environment values using a conservative key-name
  heuristic. It is defense in depth, not a secret scanner; avoid storing
  secrets in definition environments when a harness can load them from a
  dedicated credential store instead.
- `af` provides process management, not sandboxing. Managed commands run
  with the invoking user's filesystem and network privileges.
