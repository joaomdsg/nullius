#!/usr/bin/env bash
# bank.sh — run a NAMED bank (one arm x one task x n reps) with its env PINNED.
#
#   ./bank.sh --list
#   ./bank.sh <bank> [--reps N] [--dry-run]
#
# Why this exists: run.sh takes its arm config from ~8 environment variables
# with defaults (LEAN_MODEL defaults to claude-fable-5, PLAIN_MODEL to
# claude-opus-4-8). Reconstructing a bank's env by hand from the ledger put a
# fable-5 leader against an all-opus-5 comparison set, and the row did not say
# so. Every comparison set gets a NAME here, once, and reps of it are
# reproducible by that name alone. run.sh also stamps resolved_env into every
# row, so a mismatch is visible in the data rather than inferred later.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
RUN_SH="${RUN_SH:-./run.sh}"

# Every bank: TASK|ARM|LABEL|pinned env (space-separated KEY=VAL).
# Keep LABEL stable — it is the join key for every comparison in results.jsonl.
banks() {
  cat <<'TABLE'
cc-nullius-blind    stats-rolling          cc-nullius  cc-nullius-plugin-governed  LEAN_MODEL=claude-opus-5 LEAN_EFFORT=low
cc-nullius-pointed  stats-rolling-pointed  cc-nullius  cc-nullius-plugin-governed  LEAN_MODEL=claude-opus-5 LEAN_EFFORT=low
plain-blind         stats-rolling          plain       plain-opus-5-low            PLAIN_MODEL=claude-opus-5 PLAIN_EFFORT=low
plain-pointed       stats-rolling-pointed  plain       plain-opus-5-low            PLAIN_MODEL=claude-opus-5 PLAIN_EFFORT=low
TABLE
}

if [[ "${1:-}" == "--list" ]]; then
  printf '%-20s %-22s %-11s %s\n' BANK TASK ARM PINNED
  while read -r name task arm label env; do
    [[ -z "$name" ]] && continue
    printf '%-20s %-22s %-11s %s\n' "$name" "$task" "$arm" "$env"
  done < <(banks)
  exit 0
fi

BANK="${1:-}"; shift || true
REPS=1; DRY=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --reps) REPS="$2"; shift 2;;
    --dry-run) DRY=1; shift;;
    *) echo "bank.sh: unknown option $1" >&2; exit 2;;
  esac
done

ROW="$(banks | awk -v b="$BANK" '$1==b')"
if [[ -z "$BANK" || -z "$ROW" ]]; then
  echo "bank.sh: unknown bank '${BANK:-}' — one of:" >&2
  banks | awk '{print "  "$1}' >&2
  exit 2
fi
read -r _name TASK ARM LABEL PINNED_ENV <<<"$ROW"

[[ -d "tasks/$TASK" ]] || { echo "bank.sh: bank '$BANK' names a missing task dir tasks/$TASK" >&2; exit 3; }

# Container + auth are bank-invariant: every measured row so far is a container
# row, and mixing host and container reps is the same comparability trap.
: "${CONTAINER:=1}"
: "${CRED_FILE:=$HOME/.claude/.credentials.json}"
if [[ "$CONTAINER" == "1" && ! -s "$CRED_FILE" ]]; then
  echo "bank.sh: CONTAINER=1 needs a readable non-empty CRED_FILE (looked at $CRED_FILE)" >&2
  exit 3
fi

echo "bank: $BANK"
echo "  task    tasks/$TASK"
echo "  arm     $ARM"
echo "  label   $LABEL"
echo "  reps    $REPS"
echo "  pinned  $PINNED_ENV CONTAINER=$CONTAINER"
echo "  auth    CRED_FILE=$CRED_FILE ($(wc -c < "$CRED_FILE" 2>/dev/null || echo 0) bytes)"
[[ "$DRY" == 1 ]] && { echo "  (dry run — nothing spent)"; exit 0; }

# shellcheck disable=SC2086
env $PINNED_ENV BANK_NAME="$BANK" LABEL="$LABEL" \
    CONTAINER="$CONTAINER" CRED_FILE="$CRED_FILE" \
    "$RUN_SH" "tasks/$TASK" "$ARM" --reps "$REPS"
