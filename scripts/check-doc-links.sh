#!/bin/sh
set -eu

files=$(mktemp)
links=$(mktemp)
trap 'rm -f "$files" "$links"' EXIT HUP INT TERM
status=0

find . -type f -name '*.md' \
  ! -path './.git/*' ! -path './.codegraph/*' \
  ! -path './.agents/*' ! -path './.codex/*' \
  ! -path './.claude/*' ! -path './vendor/*' | sort >"$files"

while IFS= read -r file; do
  : >"$links"
  grep -oE '\]\([^)]*\)' "$file" 2>/dev/null |
    sed -e 's/^](//' -e 's/)$//' >"$links" || true
  while IFS= read -r link; do
    link=${link#<}
    link=${link%>}
    link=${link%%[[:space:]]*}
    case "$link" in
      ''|'#'*|/*|*://*|mailto:*|data:*) continue ;;
    esac
    target=${link%%#*}
    target=${target%%\?*}
    if [ ! -e "$(dirname "$file")/$target" ]; then
      printf '%s: missing local link target %s\n' "$file" "$link" >&2
      status=1
    fi
  done <"$links"
done <"$files"

exit "$status"
