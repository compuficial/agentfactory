#!/usr/bin/env bash
# Install agent skill packs used by this template:
#   https://github.com/mattpocock/skills
#   https://github.com/obra/superpowers
#
# Usage:
#   ./scripts/setup-agent-skills.sh           # global install (default)
#   ./scripts/setup-agent-skills.sh --project # install into this repo
#   ./scripts/setup-agent-skills.sh --check   # list installed skills
#
# Requires Node.js (npx). These are host/agent skills, not Go deps.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

SCOPE="-g"
CHECK_ONLY=0

for arg in "$@"; do
  case "$arg" in
    --project) SCOPE="" ;;
    --global) SCOPE="-g" ;;
    --check) CHECK_ONLY=1 ;;
    -h|--help)
      sed -n '2,12p' "$0" | tr -d '#'
      exit 0
      ;;
    *)
      echo "unknown flag: $arg" >&2
      exit 1
      ;;
  esac
done

have() { command -v "$1" >/dev/null 2>&1; }

ok()   { printf '  [ok]   %s\n' "$*"; }
warn() { printf '  [warn] %s\n' "$*"; }
info() { printf '  [..]   %s\n' "$*"; }

if ! have npx; then
  warn "npx not found — install Node.js to use the skills.sh installer"
  warn "Or install via your agent marketplace (see docs/agent-skills.md)"
  exit 1
fi

if [[ "$CHECK_ONLY" -eq 1 ]]; then
  echo "Installed skills (skills list):"
  npx -y skills@latest list || warn "skills list failed"
  exit 0
fi

scope_label="global"
[[ -z "$SCOPE" ]] && scope_label="project"

echo "Installing agent skills (${scope_label}) into agents discovered by skills.sh"
info "mattpocock/skills"
# --all = all skills + all detected agents, non-interactive
# shellcheck disable=SC2086
npx -y skills@latest add mattpocock/skills --all ${SCOPE} -y

info "obra/superpowers"
# shellcheck disable=SC2086
npx -y skills@latest add obra/superpowers --all ${SCOPE} -y

cat <<EOF

Skills installed via skills.sh.

Required once per repo (in your agent chat):
  /setup-matt-pocock-skills

Cursor alternative for Superpowers (marketplace plugin):
  /add-plugin superpowers

Claude Code alternatives:
  claude plugins install mattpocock-skills
  /plugin install superpowers@claude-plugins-official

Details: docs/agent-skills.md
Update later: npx skills update

EOF
