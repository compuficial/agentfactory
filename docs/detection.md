# Blocked-agent detection

How `af` decides a session is `awaiting-input` without being told. Four
tiers, layered; every mechanism is harness-agnostic — per-harness
knowledge is *data* (built-in defaults or your config), never code.

## Auto-wired hooks (`files:`)

A harness can declare files that `af` materializes under its data dir
before launch; the command template references them as `{{.FilesDir}}`.
The built-in `claude-code` harness uses this to pass `--settings` with a
generated hooks file whose `Stop`/`Notification` hooks run
`af signal "$AF_SESSION_ID" awaiting-input` — Claude Code sessions
report blocked the moment it happens, zero setup, no dotfiles touched.
Wire any harness with a file-based hook or config system the same way;
a Codex sketch:

```yaml
harnesses:
  codex:
    command: 'codex -c {{shellquote (printf "notify=[%q,%q]" "sh" (printf "%s/notify.sh" .FilesDir))}}{{if .Model}} --model {{shellquote .Model}}{{end}}'
    quit_keys: ["/quit"]
    files:
      notify.sh: |
        #!/bin/sh
        # codex runs this when a turn completes
        af signal "$AF_SESSION_ID" awaiting-input || true
```

Sessions not started by `af` have no `AF_SESSION_ID`, so wired hooks
exit harmlessly outside `af`.

## Terminal signals

The terminal protocol itself declares state, and `af` reads it from the
output stream it already logs: a **bell**, an **OSC 9/777 notification**
("Claude needs your permission…"), or an **OSC 133;B/D prompt mark**
flips the session to `awaiting-input` the instant it arrives — no
idle-threshold latency, no patterns, no per-harness anything. An OSC
133;C command-start reads as working. Notifications count as attention
by default (that's what a notification *is*); narrow them with
`signals.notify_awaiting` patterns if a harness sends noisy ones.
Disable the tier with `signals: {enabled: false}` / `AF_SIGNALS=false`.

## Screen detection

For everything else, `af` inspects a quiet session's rendered screen
during reconciliation. *Universal* patterns — `(y/n)`, `press enter`,
permission questions, numbered choice menus, suppressed by spinners and
"esc to interrupt" — apply to **every** harness out of the box;
per-harness `detect:` rules override them (`claude-code` ships a tuned
set). Latency ≈ `idle_threshold`. Disable the tier with
`detect: false` / `AF_DETECT=false`.

The CLI reconciles before commands that observe or act on sessions;
the dashboard does so on each configured TUI tick. Screen detection is
therefore polling-based even though hook and terminal signals are not.

## Explicit signals

`af signal <session> awaiting-input|done` (or the MCP `af_signal` tool)
from any script or hook — always available, and always outranks
detection. The session's next real output clears the state
automatically.
