#!/bin/sh
set -eu

workflow_dir=${1:-.github/workflows}

if [ ! -d "$workflow_dir" ]; then
  echo "workflow directory not found: $workflow_dir" >&2
  exit 1
fi

set --
for workflow in "$workflow_dir"/*.yml "$workflow_dir"/*.yaml; do
  [ -f "$workflow" ] || continue
  set -- "$@" "$workflow"
done

if [ "$#" -eq 0 ]; then
  echo "no workflow files found in $workflow_dir" >&2
  exit 1
fi

awk '
function report(message) {
  print FILENAME ":" FNR ": " message > "/dev/stderr"
  failed = 1
}

{
  line = $0
  if (line !~ /^[[:space:]]*(-[[:space:]]*)?uses:[[:space:]]*/) {
    next
  }

  value = line
  sub(/^[[:space:]]*(-[[:space:]]*)?uses:[[:space:]]*/, "", value)
  labeled = value ~ /[[:space:]]+#[[:space:]]*v[0-9]/
  sub(/[[:space:]]+#.*$/, "", value)
  sub(/[[:space:]]+$/, "", value)
  seen++

  if (value ~ /^\.\//) {
    next
  }

  separator = index(value, "@")
  ref = separator == 0 ? "" : substr(value, separator + 1)
  if (length(ref) != 40 || ref !~ /^[0-9a-f]+$/) {
    report("remote action must use a full 40-character commit SHA")
  }
  if (!labeled) {
    report("SHA-pinned action must include a version comment")
  }
}

END {
  if (seen == 0) {
    print "no workflow action references found" > "/dev/stderr"
    exit 1
  }
  exit failed
}
' "$@"
