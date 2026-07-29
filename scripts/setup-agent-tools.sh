#!/usr/bin/env bash
# Install / wire host agent tools for this repo:
#   rtk       — compress shell/tool output for LLMs
#   codegraph — local code knowledge graph (MCP)
#   ast-grep  — structural (AST) search & rewrite
#
# Usage:
#   ./scripts/setup-agent-tools.sh
#   ./scripts/setup-agent-tools.sh --check   # status only
#
# These are host installs (not Go module deps). Safe to re-run.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

CHECK_ONLY=0
if [[ "${1:-}" == "--check" ]]; then
  CHECK_ONLY=1
fi

have() { command -v "$1" >/dev/null 2>&1; }

# `sg` is ast-grep's short alias, but on Linux `sg` is also setgid(1)
# (a symlink to newgrp). Only trust `sg` when it's really ast-grep.
have_astgrep() {
  have ast-grep && return 0
  have sg && sg --version 2>/dev/null | grep -qi ast-grep
}

ok()   { printf '  [ok]   %s\n' "$*"; }
warn() { printf '  [warn] %s\n' "$*"; }
info() { printf '  [..]   %s\n' "$*"; }

install_rtk() {
  if have rtk; then
    ok "rtk $(rtk --version 2>/dev/null | head -n1 || echo present)"
    return 0
  fi
  [[ "$CHECK_ONLY" -eq 1 ]] && { warn "rtk not found"; return 1; }

  info "installing rtk"
  if have brew; then
    brew install rtk
  else
    curl -fsSL https://raw.githubusercontent.com/rtk-ai/rtk/refs/heads/master/install.sh | sh
    export PATH="${HOME}/.local/bin:${PATH}"
  fi
  have rtk || { warn "rtk install finished but binary not on PATH; open a new shell"; return 1; }
  ok "rtk installed"
}

wire_rtk() {
  have rtk || return 0
  [[ "$CHECK_ONLY" -eq 1 ]] && return 0

  # Prefer Cursor when present; also try Claude Code defaults.
  if [[ -d "${HOME}/.cursor" ]] || [[ -d "${ROOT}/.cursor" ]]; then
    info "wiring rtk for Cursor (rtk init -g --agent cursor --auto-patch)"
    rtk init -g --agent cursor --auto-patch || warn "rtk cursor init failed (non-fatal)"
  fi
  if have claude || [[ -d "${HOME}/.claude" ]]; then
    info "wiring rtk for Claude Code (rtk init -g --auto-patch)"
    rtk init -g --auto-patch || warn "rtk claude init failed (non-fatal)"
  fi
}

install_codegraph() {
  if have codegraph; then
    ok "codegraph $(codegraph --version 2>/dev/null | head -n1 || echo present)"
    return 0
  fi
  [[ "$CHECK_ONLY" -eq 1 ]] && { warn "codegraph not found"; return 1; }

  info "installing codegraph"
  if have npm; then
    npm i -g @colbymchenry/codegraph
  else
    curl -fsSL https://raw.githubusercontent.com/colbymchenry/codegraph/main/install.sh | sh
    export PATH="${HOME}/.local/bin:${PATH}"
  fi
  have codegraph || { warn "codegraph install finished but binary not on PATH; open a new shell"; return 1; }
  ok "codegraph installed"
}

wire_codegraph() {
  have codegraph || return 0

  if [[ "$CHECK_ONLY" -eq 1 ]]; then
    if [[ -d "${ROOT}/.codegraph" ]]; then
      ok "codegraph project index present (.codegraph/)"
    else
      warn "codegraph project not initialized (run: codegraph init)"
    fi
    return 0
  fi

  info "wiring codegraph into detected agents (codegraph install --yes)"
  codegraph install --yes || warn "codegraph install failed (non-fatal)"

  if [[ ! -d "${ROOT}/.codegraph" ]]; then
    info "building project graph (codegraph init)"
    codegraph init
  else
    ok "codegraph project already initialized"
  fi
}

install_ast_grep() {
  if have_astgrep; then
    local bin=ast-grep
    have ast-grep || bin=sg
    ok "$bin $($bin --version 2>/dev/null | head -n1 || echo present)"
    return 0
  fi
  [[ "$CHECK_ONLY" -eq 1 ]] && { warn "ast-grep not found"; return 1; }

  info "installing ast-grep"
  if have brew; then
    brew install ast-grep
  elif have cargo; then
    cargo install ast-grep --locked
  elif have npm; then
    npm i -g @ast-grep/cli
  else
    warn "install ast-grep manually: https://ast-grep.github.io/guide/quick-start.html"
    return 1
  fi
  have_astgrep || { warn "ast-grep not on PATH after install"; return 1; }
  ok "ast-grep installed"
}

echo "Agent tools ($( [[ "$CHECK_ONLY" -eq 1 ]] && echo check || echo setup )) in ${ROOT}"

install_rtk || true
wire_rtk || true
install_codegraph || true
wire_codegraph || true
install_ast_grep || true

cat <<EOF

Next:
  - Restart Cursor / Claude Code so MCP + hooks reload
  - Prefer codegraph MCP for navigation; ast-grep for structural find/rewrite
  - Shell output is compressed via rtk hooks when wired
  - Status anytime: make agent-tools-check
  - Details: docs/agent-tools.md

EOF
