#!/bin/sh
# Reads stdin and echoes each line back: proves `af send`.
echo "ready"
while IFS= read -r line; do
	echo "echo: $line"
done
