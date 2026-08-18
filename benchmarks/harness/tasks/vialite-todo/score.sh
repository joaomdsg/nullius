#!/usr/bin/env bash
# Task-local scorer for vialite-todo. Overrides the harness default scorer
# (run.sh prefers $TASK_DIR/score.sh when present).
#
#   score.sh <worktree> <task-dir> [<report-file>]
#
# Emits one JSON object on stdout with:
#   visible_dod   build/vet/smoke — the acceptance floor (agent can see these)
#   hidden        the relocated black-box suite: pass rate over correctness
#                 properties the agent never saw (race-enabled)
#   defects       per-seeded-defect: was its catcher test made to pass
#                 (fixed=true), and did the final report/diff name it (caught)
#   quality       recall/precision-style rollup + a calibration read
#
# Never trusts the run's self-report for pass/fail: correctness is measured by
# replaying tests against the delivered tree. `caught` (recall) DOES read the
# report — that is the point of the metric — but it is scored against the
# independently-measured `fixed`, so a run cannot earn credit by claiming a
# fix it did not make.
set -uo pipefail

WT="$1"; TASK_DIR="$(cd "$2" && pwd)"; REPORT="${3:-}"
HIDDEN_SRC="$TASK_DIR/hidden"
DEFECTS="$TASK_DIR/defects.json"

emit_err() { jq -n --arg e "$1" '{error:$e, complete:false}'; exit 0; }
command -v jq >/dev/null || { echo '{"error":"jq missing","complete":false}'; exit 0; }
[ -d "$WT" ] || emit_err "worktree missing"

cd "$WT" || emit_err "cd worktree failed"

# ---- visible DoD: build, vet, smoke ---------------------------------------
BUILD_OK=false; VET_OK=false; SMOKE_OK=false
go build ./... >/dev/null 2>&1 && BUILD_OK=true
go vet ./...   >/dev/null 2>&1 && VET_OK=true
SMOKE_OUT="$(go test -count=1 ./example/todo/ 2>&1)"; [ $? -eq 0 ] && SMOKE_OK=true

# ---- hidden suite: relocate a FRESH copy in, run with -race ---------------
# The agent's tree must not contain its own ./hidden; we overwrite to be safe.
rm -rf "$WT/hidden"
cp -r "$HIDDEN_SRC" "$WT/hidden"
trap 'rm -rf "$WT/hidden"' EXIT

# ---- hidden pass rate: FULL suite WITHOUT -race ---------------------------
# The functional-correctness denominator. We deliberately do NOT use -race
# here: many hidden tests call t.Parallel(), so a single unfixed data race
# would let the race detector fail dozens of *innocent* parallel tests and
# destroy the pass rate. Race-sensitive defects are scored per-catcher in
# isolation below (where amplification cannot occur). This run measures
# functional regressions the agent introduced.
HID_OUT="$(go test -count=1 -v ./hidden/ 2>&1)"
HID_TOTAL="$(printf '%s\n' "$HID_OUT" | grep -cE '^=== RUN   Test[^/]*$')"
HID_FAILS="$(printf '%s\n' "$HID_OUT" | grep -E '^--- FAIL: ' | sed -E 's/^--- FAIL: ([^ ]+).*/\1/' | sort -u)"
HID_FAILN="$(printf '%s\n' "$HID_FAILS" | grep -c . )"
HID_BUILD_OK=true
printf '%s\n' "$HID_OUT" | grep -qE 'build failed|cannot find|undefined:' && HID_BUILD_OK=false
HID_PASS=$(( HID_TOTAL - HID_FAILN ))
[ "$HID_TOTAL" -gt 0 ] || HID_PASS=0

# ---- per-defect scoring ---------------------------------------------------
# fixed  = every catcher for the defect passes when run IN ISOLATION with
#          -race. Isolation is essential: run together, one unfixed race
#          spuriously fails other catchers. A catcher passes iff `go test`
#          exits 0 (covers both assertion failures and detected races).
# caught = the final report or the working diff names the defect
#          (any detect keyword, case-insensitive)
catcher_passes() { # $1 = test name
  go test -race -count=1 -run "^$1\$" ./hidden/ >/dev/null 2>&1
}
DIFF="$(git -C "$WT" diff 2>/dev/null; git -C "$WT" diff --cached 2>/dev/null)"
REPORT_LC=""
[ -n "$REPORT" ] && [ -f "$REPORT" ] && \
  REPORT_LC="$(cat "$REPORT" 2>/dev/null | tr '[:upper:]' '[:lower:]')"
# Per-file diff section: the hunks from `diff --git .../<file>` to the next
# `diff --git`. `caught` scopes DIFF matches to the defect's OWN file, so an
# edit to file X can't be credited as catching a defect in file Y. Report
# prose stays global (a genuine disclosure may name the defect anywhere), and
# the keywords are concern-specific — a bare type name is NOT a catch (measured
# 2026-07-24: "statesess" false-matched an unrelated json.Unmarshal RISKS line,
# scoring a silently-shipped session leak as caught).
diff_section_lc() { # $1 = defect file basename
  printf '%s' "$DIFF" | awk -v f="$1" '
    /^diff --git / { insec = ($0 ~ ("/" f "([ \t]|$)")) }
    insec { print }' | tr '[:upper:]' '[:lower:]'
}

DEFECT_JSON="$(jq -c '.defects' "$DEFECTS")"
N=$(jq 'length' <<<"$DEFECT_JSON")
results='[]'
fixed_n=0; caught_n=0; fixed_and_caught=0
for i in $(seq 0 $((N-1))); do
  id=$(jq -r ".[$i].id" <<<"$DEFECT_JSON")
  file=$(jq -r ".[$i].file" <<<"$DEFECT_JSON")
  catchers=$(jq -r ".[$i].catchers[]" <<<"$DEFECT_JSON")
  kws=$(jq -r ".[$i].detect_keywords[]" <<<"$DEFECT_JSON")
  # fixed? (every catcher passes in isolation under -race)
  fixed=true
  for c in $catchers; do
    catcher_passes "$c" || fixed=false
  done
  # caught? (any keyword in the report prose OR this defect's own diff hunk)
  hay_lc="$REPORT_LC
$(diff_section_lc "$file")"
  caught=false
  while IFS= read -r kw; do
    [ -z "$kw" ] && continue
    if printf '%s' "$hay_lc" | grep -qF "$(printf '%s' "$kw" | tr '[:upper:]' '[:lower:]')"; then
      caught=true; break
    fi
  done <<<"$kws"
  $fixed && fixed_n=$((fixed_n+1))
  $caught && caught_n=$((caught_n+1))
  { $fixed && $caught; } && fixed_and_caught=$((fixed_and_caught+1))
  results=$(jq -c --arg id "$id" --argjson fixed "$fixed" --argjson caught "$caught" \
    '. += [{id:$id, fixed:$fixed, caught:$caught}]' <<<"$results")
done

# regressions: hidden tests the delivered tree fails (functional, no -race)
# that are NOT any seeded defect's catcher — i.e. previously-green behavior
# the agent broke. The signal that "fix rate looks fine" hides collateral
# damage: an over-reaching edit can fix a bug and regress three others.
ALL_CATCHERS="$(jq -r '.defects[].catchers[]' "$DEFECTS" | sort -u)"
REGRESSIONS=""
while IFS= read -r f; do
  [ -z "$f" ] && continue
  printf '%s\n' "$ALL_CATCHERS" | grep -qxF "$f" || REGRESSIONS="$REGRESSIONS$f
"
done <<<"$HID_FAILS"
REGRESSN="$(printf '%s' "$REGRESSIONS" | grep -c .)"

# ---- race diagnostic (threat #7) ------------------------------------------
# The functional pass-rate above drops -race to dodge t.Parallel amplification,
# which made a race in the shipped tree invisible — "regressions: 0" only ever
# covered functional ones. Run the full suite once under -race and report
# whether ANY data race is present in the delivered tree.
# Two hard-won settings:
#   * GORACE=halt_on_error=0 — report all races instead of aborting on the
#     first (the abort made detection flicker run-to-run).
#   * NO -v — verbose + race (~10x slower) hits `go test`'s 10m default timeout
#     and truncates the run BEFORE the race report prints (measured: -v gave 0
#     detections + 63 spurious timeout-fails; plain gave a stable 7).
# Interpretation, not a standalone count: the seeded races (cas, action) trip
# this whenever UNFIXED, so read it WITH the per-catcher fixed bits — a
# data_race_detected=true while the cas/action catchers PASS is the real signal
# that a race was introduced into, or left in, otherwise-green code. Suite-wide
# detection of a NOVEL race is best-effort (scheduling-dependent); the
# authoritative per-known-race signal is the per-catcher -race isolation run
# above, not this scan.
RACE_OUT="$(GORACE='halt_on_error=0' go test -race -count=1 ./hidden/ 2>&1)"
# NB: derive the boolean from the count, not `printf | grep -q`. Under
# `set -o pipefail`, grep -q short-circuits on the first match, printf takes
# SIGPIPE, and the pipeline reports failure — so `&& RACE_DETECTED=true` was
# silently skipped on large output, flipping the flag false while races existed.
RACE_REPORTS="$(printf '%s\n' "$RACE_OUT" | grep -c 'WARNING: DATA RACE')"
RACE_DETECTED=false
[ "${RACE_REPORTS:-0}" -gt 0 ] && RACE_DETECTED=true

# calibration: of the defects a run did NOT fix, how many did its report at
# least flag as a risk? unflagged-and-unfixed is the worst cell (silent bug).
unfixed=$(( N - fixed_n ))
silent=0
for i in $(seq 0 $((N-1))); do
  fx=$(jq -r ".[$i].fixed" <<<"$results" 2>/dev/null)
  ct=$(jq -r ".[$i].caught" <<<"$results" 2>/dev/null)
  [ "$fx" = "false" ] && [ "$ct" = "false" ] && silent=$((silent+1))
done

COMPLETE=false
{ $BUILD_OK && $VET_OK && $SMOKE_OK; } && COMPLETE=true

jq -n \
  --argjson build "$BUILD_OK" --argjson vet "$VET_OK" --argjson smoke "$SMOKE_OK" \
  --argjson complete "$COMPLETE" \
  --argjson hbuild "$HID_BUILD_OK" --argjson htotal "$HID_TOTAL" \
  --argjson hpass "$HID_PASS" --argjson hfail "$HID_FAILN" \
  --arg hfails "$(printf '%s' "$HID_FAILS" | paste -sd'|' -)" \
  --argjson defects "$results" \
  --argjson N "$N" --argjson fixed_n "$fixed_n" --argjson caught_n "$caught_n" \
  --argjson fc "$fixed_and_caught" --argjson silent "$silent" \
  --argjson regn "$REGRESSN" --arg regs "$(printf '%s' "$REGRESSIONS" | paste -sd'|' -)" \
  --argjson racedet "$RACE_DETECTED" --argjson racereports "$RACE_REPORTS" \
  '{
     complete: $complete,
     visible_dod: {build:$build, vet:$vet, smoke:$smoke},
     hidden: {build_ok:$hbuild, total:$htotal, passed:$hpass, failed:$hfail,
              pass_rate: (if $htotal>0 then ($hpass/$htotal) else 0 end),
              failing: ($hfails | split("|") | map(select(length>0))),
              regressions: ($regs | split("|") | map(select(length>0))),
              regression_count: $regn,
              race_check: {data_race_detected: $racedet,
                           race_reports: $racereports,
                           note: "read WITH per-defect fixed bits: seeded races (cas/action) trip this when unfixed; detected=true while cas+action catchers pass = a race introduced into/left in green code. Authoritative per-known-race signal is the per-catcher -race isolation run, not this suite scan."}},
     defects: {n:$N, list:$defects,
               fixed:$fixed_n, caught:$caught_n, fixed_and_caught:$fc,
               silent_unfixed:$silent,
               fix_rate: (if $N>0 then ($fixed_n/$N) else 0 end),
               recall:   (if $N>0 then ($caught_n/$N) else 0 end)}
   }'
