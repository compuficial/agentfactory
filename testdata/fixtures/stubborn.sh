#!/bin/sh
# Traps and ignores SIGTERM: proves close -> kill escalation on the
# process group.
trap 'echo "ignoring SIGTERM"' TERM
echo "stubborn: ready"
while :; do
	sleep 0.2
done
