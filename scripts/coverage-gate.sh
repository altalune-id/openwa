#!/usr/bin/env bash
# Fails when total statement coverage in $1 is below $2 percent.
set -euo pipefail
profile="$1"; min="$2"
filtered=$(mktemp)
trap 'rm -f "$filtered"' EXIT
# Generated code (templ, buf, jet) is excluded so the gate measures hand-written code.
grep -vE '(_templ\.go:|/gen/|/internal/platform/db/entity/postgres/)' "$profile" > "$filtered"
total=$(go tool cover -func="$filtered" | awk '/^total:/ {gsub("%","",$3); print $3}')
echo "coverage: total ${total}% (gate ${min}%)"
awk -v t="$total" -v m="$min" 'BEGIN { exit (t+0 >= m+0) ? 0 : 1 }' \
  || { echo "coverage gate failed: ${total}% < ${min}%" >&2; exit 1; }
