#!/bin/sh
# Simulates a full-screen TUI harness (grok/opencode style): alternate
# screen, clear + cursor addressing on every frame, bottom-anchored
# input bar. Reads stdin; redraws after each line.
printf '\033[?1049h'
LAST=""
while :; do
	# stty, not tput: it reads the pty directly, needing no terminfo
	# entry for tmux-256color (absent on e.g. macOS CI runners).
	rows=$(stty size 2>/dev/null | cut -d' ' -f1)
	[ -n "$rows" ] || rows=24
	printf '\033[2J\033[H'
	printf 'CONVO rows=%s\n' "$rows"
	if [ -n "$LAST" ]; then
		printf 'echo: %s\n' "$LAST"
	fi
	printf '\033[%s;1H> input-bar' "$rows"
	IFS= read -r LAST || break
done
printf '\033[?1049l'
