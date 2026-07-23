#!/bin/sh
# Emits output continuously: proves the `active` status.
i=0
while :; do
	i=$((i + 1))
	echo "tick $i"
	sleep 0.2
done
