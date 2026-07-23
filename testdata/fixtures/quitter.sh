#!/bin/sh
# Exits cleanly when it reads "quit" on stdin: proves the QuitKeys
# path of af close for harnesses with a quit command.
echo "quitter: ready"
while IFS= read -r line; do
	if [ "$line" = "quit" ]; then
		echo "quitter: bye"
		exit 0
	fi
	echo "quitter: $line"
done
