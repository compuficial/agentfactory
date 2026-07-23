#!/bin/sh
# Exits with a known code after a short delay: proves exited(code)
# via remain-on-exit. Usage: exiter.sh <code> [delay-seconds]
echo "about to exit with ${1:-0}"
sleep "${2:-0.3}"
exit "${1:-0}"
