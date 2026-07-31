#!/bin/sh
# Emits output continuously: proves the `working` status.
i=0
while :; do
	i=$((i + 1))
	echo "tick $i"
	sleep 0.2
done
