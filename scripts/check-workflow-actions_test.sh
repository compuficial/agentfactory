#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checker="${root}/scripts/check-workflow-actions.sh"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

mkdir -p "${work}/valid" "${work}/mutable" "${work}/unlabeled"

cat >"${work}/valid/ci.yml" <<'EOF'
steps:
  - uses: actions/checkout@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa # v7.0.1
  - uses: ./local-action
EOF

cat >"${work}/mutable/ci.yml" <<'EOF'
steps:
  - uses: actions/checkout@v7
EOF

cat >"${work}/unlabeled/ci.yml" <<'EOF'
steps:
  - uses: actions/checkout@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
EOF

"$checker" "${work}/valid"

if "$checker" "${work}/mutable" >"${work}/mutable.out" 2>&1; then
  echo "expected mutable action ref to fail" >&2
  exit 1
fi
grep -q "full 40-character commit SHA" "${work}/mutable.out"

if "$checker" "${work}/unlabeled" >"${work}/unlabeled.out" 2>&1; then
  echo "expected unlabeled action ref to fail" >&2
  exit 1
fi
grep -q "version comment" "${work}/unlabeled.out"
