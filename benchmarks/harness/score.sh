#!/usr/bin/env bash
# Independent scorer — replays the task's DONE-WHEN against the worktree.
# Never trusts the run's self-report. Emits one JSON object on stdout.
#
#   ./score.sh <worktree> <tests.txt>
#
# tests.txt: one Go test name per line (exact -run anchors).
set -euo pipefail
WT="$1"; TESTS_FILE="$2"

pass=(); fail=()
while IFS= read -r t; do
  [[ -z "$t" || "$t" == \#* ]] && continue
  # `go test -run` exits 0 when NO test matches — require an explicit PASS
  # line so an absent test scores as failed, not vacuously green.
  out="$(cd "$WT" && timeout 300 go test -race -run "^${t}\$" -count=1 -v ./ 2>&1 || true)"
  if grep -q "^--- PASS: ${t}\b" <<<"$out" && ! grep -q "^--- FAIL:" <<<"$out"; then
    pass+=("$t")
  else
    fail+=("$t")
  fi
done < "$TESTS_FILE"

vet_ok=false
(cd "$WT" && go vet ./ >/dev/null 2>&1) && vet_ok=true
suite_ok=false
(cd "$WT" && timeout 900 go test -race -count=1 ./ >/dev/null 2>&1) && suite_ok=true

jq -n -c \
  --argjson passed "$(printf '%s\n' "${pass[@]:-}" | jq -R . | jq -s 'map(select(.!=""))')" \
  --argjson failed "$(printf '%s\n' "${fail[@]:-}" | jq -R . | jq -s 'map(select(.!=""))')" \
  --argjson vet "$vet_ok" --argjson suite "$suite_ok" \
  '{tests_passed:$passed, tests_failed:$failed, vet_clean:$vet,
    full_suite_green:$suite,
    complete: (($failed|length)==0 and $vet and $suite)}'
