#!/bin/sh
# Spawns a child that would outlive it unless the whole process group
# is signaled: proves PGID signaling. Writes the child's PID to the
# file given as $1 so tests can verify the child died too.
(
	while :; do
		sleep 0.2
	done
) &
echo "$!" > "$1"
echo "spawner: child $! started"
wait
