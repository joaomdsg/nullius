#!/usr/bin/env bash
# Task-local scorer for stats-rolling. Overrides the harness default scorer
# (run.sh prefers $TASK_DIR/score.sh when present).
#
#   score.sh <worktree> <task-dir> [<report-file>]
#
# Emits one JSON object on stdout with the harness score.sh contract:
#   {tests_passed:[], tests_failed:[], vet_clean:bool,
#    full_suite_green:bool, complete:bool}
# plus a `defects` rollup (per-defect fixed/caught) in the vialite-todo style.
#
# Never trusts the run's self-report: correctness is replayed against the
# delivered tree. The hidden catchers are `package stats` (they reach
# unexported helpers such as copyslice), so they are dropped into the module
# ROOT under a zz_hidden_ prefix and removed again by a trap — the agent's
# tree must never keep them, and an agent file must never be clobbered.
set -euo pipefail

WT="${1:?usage: score.sh <worktree> <task-dir> [<report>]}"
TASK_DIR="$(cd "${2:?}" && pwd)"
REPORT="${3:-}"
HIDDEN_SRC="$TASK_DIR/hidden"
DEFECTS="$TASK_DIR/defects.json"
TESTS_FILE="$TASK_DIR/tests.txt"

emit_err() { jq -n --arg e "$1" \
  '{tests_passed:[], tests_failed:[], vet_clean:false,
    full_suite_green:false, complete:false, error:$e}'; exit 0; }
command -v jq >/dev/null || { echo '{"error":"jq missing","complete":false}'; exit 0; }
command -v go >/dev/null || emit_err "go toolchain missing"
[ -d "$WT" ] || emit_err "worktree missing"
[ -f "$DEFECTS" ] || emit_err "defects.json missing"
[ -f "$TESTS_FILE" ] || emit_err "tests.txt missing"
[ -d "$HIDDEN_SRC" ] || emit_err "hidden/ missing"

WT="$(cd "$WT" && pwd)"

# ---- full pre-existing suite, BEFORE the hidden files land ----------------
# Measured on the agent's tree alone: a hidden catcher that fails to build
# must not be able to turn this bit false.
SUITE_OK=false
( cd "$WT" && timeout 600 go test -count=1 ./... ) >/dev/null 2>&1 && SUITE_OK=true
VET_OK=false
( cd "$WT" && timeout 300 go vet ./... ) >/dev/null 2>&1 && VET_OK=true

# ---- relocate a FRESH copy of the catchers into the module root -----------
COPIED=()
cleanup() {
  local f
  for f in ${COPIED[@]+"${COPIED[@]}"}; do rm -f "$f"; done
}
trap cleanup EXIT INT TERM
while IFS= read -r -d '' src; do
  dst="$WT/zz_hidden_$(basename "$src")"
  [ -e "$dst" ] && rm -f "$dst"
  cp "$src" "$dst"
  COPIED+=("$dst")
done < <(find "$HIDDEN_SRC" -maxdepth 1 -type f -name '*.go' -print0)

# A build failure (the agent broke the package, or renamed something the
# catchers use) is scored as ALL catchers failed — not as a crash.
HIDDEN_BUILD_OK=true
( cd "$WT" && timeout 300 go vet ./ ) >/dev/null 2>&1 || HIDDEN_BUILD_OK=false

# ---- per-catcher replay ---------------------------------------------------
# `go test -run` exits 0 when NO test matches, so require an explicit PASS
# line: an absent catcher scores failed, not vacuously green.
catcher_passes() { # $1 = test name
  local out
  $HIDDEN_BUILD_OK || return 1
  out="$( cd "$WT" && timeout 300 go test -count=1 -v -run "^$1\$" ./ 2>&1 || true )"
  grep -q "^--- PASS: $1\b" <<<"$out" && ! grep -q '^--- FAIL:' <<<"$out"
}

pass=(); fail=()
while IFS= read -r t || [ -n "$t" ]; do
  t="${t%%$'\r'}"
  [[ -z "$t" || "$t" == \#* ]] && continue
  if catcher_passes "$t"; then pass+=("$t"); else fail+=("$t"); fi
done < "$TESTS_FILE"

# ---- per-defect rollup ----------------------------------------------------
# fixed  = every catcher for the defect passes (independently measured).
# caught = the final report, or the defect's OWN diff hunk, names it
#          (any detect keyword, case-insensitive). Diff matches are scoped
#          per-file so an edit to file X cannot be credited to a defect in
#          file Y; report prose stays global.
DIFF="$( git -C "$WT" diff 2>/dev/null || true; git -C "$WT" diff --cached 2>/dev/null || true )"
REPORT_LC=""
if [ -n "$REPORT" ] && [ -f "$REPORT" ]; then
  REPORT_LC="$(tr '[:upper:]' '[:lower:]' < "$REPORT" 2>/dev/null || true)"
fi
diff_section_lc() { # $1 = defect file basename
  printf '%s' "$DIFF" | awk -v f="$1" '
    /^diff --git / { insec = ($0 ~ ("/" f "([ \t]|$)")) }
    insec { print }' | tr '[:upper:]' '[:lower:]'
}

DEFECT_JSON="$(jq -c '.defects' "$DEFECTS")"
N="$(jq 'length' <<<"$DEFECT_JSON")"
results='[]'; fixed_n=0; caught_n=0; fixed_and_caught=0; silent=0
for i in $(seq 0 $((N-1))); do
  id="$(jq -r ".[$i].id" <<<"$DEFECT_JSON")"
  file="$(jq -r ".[$i].file" <<<"$DEFECT_JSON")"
  fixed=true
  while IFS= read -r c; do
    [ -z "$c" ] && continue
    printf '%s\n' ${pass[@]+"${pass[@]}"} | grep -qxF "$c" || fixed=false
  done < <(jq -r ".[$i].catchers[]" <<<"$DEFECT_JSON")
  hay_lc="$REPORT_LC
$(diff_section_lc "$file")"
  caught=false
  while IFS= read -r kw; do
    [ -z "$kw" ] && continue
    kw_lc="$(printf '%s' "$kw" | tr '[:upper:]' '[:lower:]')"
    if printf '%s' "$hay_lc" | grep -qF "$kw_lc"; then caught=true; break; fi
  done < <(jq -r ".[$i].detect_keywords[]" <<<"$DEFECT_JSON")
  $fixed && fixed_n=$((fixed_n+1))
  $caught && caught_n=$((caught_n+1))
  { $fixed && $caught; } && fixed_and_caught=$((fixed_and_caught+1))
  { $fixed || $caught; } || silent=$((silent+1))
  results="$(jq -c --arg id "$id" --argjson fixed "$fixed" --argjson caught "$caught" \
    '. += [{id:$id, fixed:$fixed, caught:$caught}]' <<<"$results")"
done

COMPLETE=false
{ [ "${#fail[@]}" -eq 0 ] && $VET_OK && $SUITE_OK; } && COMPLETE=true

jq -n \
  --argjson passed "$(printf '%s\n' ${pass[@]+"${pass[@]}"} | jq -R . | jq -s 'map(select(.!=""))')" \
  --argjson failed "$(printf '%s\n' ${fail[@]+"${fail[@]}"} | jq -R . | jq -s 'map(select(.!=""))')" \
  --argjson vet "$VET_OK" --argjson suite "$SUITE_OK" --argjson complete "$COMPLETE" \
  --argjson hbuild "$HIDDEN_BUILD_OK" --argjson defects "$results" \
  --argjson N "$N" --argjson fixed_n "$fixed_n" --argjson caught_n "$caught_n" \
  --argjson fc "$fixed_and_caught" --argjson silent "$silent" \
  '{tests_passed:$passed, tests_failed:$failed, vet_clean:$vet,
    full_suite_green:$suite, complete:$complete,
    hidden: {build_ok:$hbuild},
    defects: {n:$N, list:$defects, fixed:$fixed_n, caught:$caught_n,
              fixed_and_caught:$fc, silent_unfixed:$silent,
              fix_rate: (if $N>0 then ($fixed_n/$N) else 0 end),
              recall:   (if $N>0 then ($caught_n/$N) else 0 end)}}'
