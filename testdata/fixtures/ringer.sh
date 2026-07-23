#!/bin/sh
# Emits terminal-protocol signals on demand: proves T1.75 detection.
# Reads stdin; "ring" ends a turn with BEL, "notify" sends an OSC 9
# notification, "work" marks a command start (OSC 133;C).
echo "ringer: ready"
while IFS= read -r line; do
	case "$line" in
	ring) printf 'turn done\a\n' ;;
	notify) printf '\033]9;agent needs your permission\007\n' ;;
	work) printf '\033]133;C\007working again\n' ;;
	esac
done
