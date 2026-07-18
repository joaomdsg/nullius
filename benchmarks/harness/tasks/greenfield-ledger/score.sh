#!/usr/bin/env bash
# Task-local scorer for greenfield-ledger. Overrides the harness default
# scorer (run.sh prefers $TASK_DIR/score.sh when present).
#
#   score.sh <worktree> <task-dir> [<report-file>]
#
# This is a GREENFIELD task: there is no defects.json (nothing was seeded
# to regress — the agent builds ledger.go from scratch against the
# skeleton's exported contract). Correctness is instead scored two ways:
#
#   visible_dod   build/vet/visible-accept-test — the acceptance floor
#   hidden        the relocated black-box conformance suite: pass rate
#                 over properties the agent never saw (race-enabled check
#                 recorded separately from the functional pass-rate run)
#   groups        per-invariant-group pass/fail, driven by
#                 $TASK_DIR/invariants.json (schema below). This file may
#                 not exist yet at scoring time — in that case groups is
#                 null, not an error.
#   surface       whether the delivered ledger.go still declares exactly
#                 the skeleton's exported surface (types/funcs/vars/consts
#                 unchanged) — the contract the agent was told never to
#                 alter.
#
# invariants.json schema:
#   {"groups": [{"id": "<name>", "catchers": ["TestName1", "TestName2"]}]}
#
# Never trusts the run's self-report for pass/fail: correctness is measured
# by replaying tests against the delivered tree.
set -uo pipefail

WT="$1"; TASK_DIR="$(cd "$2" && pwd)"; REPORT="${3:-}"
HIDDEN_DIR_NAME="hidden"
[ -f "$TASK_DIR/meta.env" ] && HIDDEN_DIR_NAME="$(. "$TASK_DIR/meta.env"; echo "${HIDDEN_DIR:-hidden}")"
HIDDEN_SRC="$TASK_DIR/$HIDDEN_DIR_NAME"
INVARIANTS="$TASK_DIR/invariants.json"
SKELETON_LEDGER="$TASK_DIR/skeleton/ledger.go"

emit_err() { jq -n --arg e "$1" '{error:$e, complete:false}'; exit 0; }
command -v jq >/dev/null || { echo '{"error":"jq missing","complete":false}'; exit 0; }
[ -d "$WT" ] || emit_err "worktree missing"

cd "$WT" || emit_err "cd worktree failed"

# ---- combined cleanup trap -------------------------------------------------
# Must restore the agent's delivered accept/ (see below) AND remove the
# relocated hidden/ copy on ANY exit path, including early/error exits, so
# the worktree run.sh diffs afterward is byte-identical to what was
# delivered. Set the trap before doing anything that needs undoing.
ACCEPT_OVERLAID=false
restore_accept() {
  if $ACCEPT_OVERLAID; then
    rm -rf "$WT/accept"
    mv "$WT/.accept-delivered" "$WT/accept" 2>/dev/null
    ACCEPT_OVERLAID=false
  fi
}
cleanup() { restore_accept; rm -rf "$WT/hidden"; }
trap cleanup EXIT

# ---- visible DoD: build, vet, visible acceptance test ---------------------
# The accept test is the one artifact an agent can see and therefore game
# (gut the assertions, always-pass it). Swap in the TASK's own reference
# copy of accept/ for the duration of this run only, then restore the
# delivered one — the agent is scored on OUR accept test, not its own.
BUILD_OK=false; VET_OK=false; ACCEPT_OK=false
go build ./... >/dev/null 2>&1 && BUILD_OK=true
go vet ./...   >/dev/null 2>&1 && VET_OK=true
if [ -d "$WT/accept" ] && [ -d "$TASK_DIR/skeleton/accept" ]; then
  mv "$WT/accept" "$WT/.accept-delivered"
  cp -r "$TASK_DIR/skeleton/accept" "$WT/accept"
  ACCEPT_OVERLAID=true
fi
go test -count=1 ./accept/ >/dev/null 2>&1 && ACCEPT_OK=true
restore_accept

# ---- surface check: exported declarations must match the skeleton --------
# `go doc -all` only ever lists EXPORTED top-level symbols (unexported
# helpers the agent adds freely are invisible to it, exactly the filter we
# want). Declaration headers start at column 0; doc-comment prose is
# indented, so grepping ^(func|type|var|const) isolates signatures from
# doc-comment wording changes. Run it once inside the skeleton module, once
# inside the delivered worktree (same module path), and diff the header
# lines.
extract_surface() { ( cd "$1" && go doc -all . 2>/dev/null | grep -E '^(func|type|var|const)\b' ); }
SURFACE_OK=false
SURFACE_DIFF=""
if [ -f "$WT/ledger.go" ] && [ -f "$SKELETON_LEDGER" ]; then
  A="$(extract_surface "$TASK_DIR/skeleton")"
  B="$(extract_surface "$WT")"
  if [ "$A" = "$B" ]; then
    SURFACE_OK=true
  else
    SURFACE_DIFF="$(diff <(printf '%s\n' "$A") <(printf '%s\n' "$B") || true)"
  fi
else
  SURFACE_DIFF="ledger.go missing from delivered tree or skeleton not found"
fi

# ---- hidden suite: relocate a FRESH copy in, run with -race ---------------
rm -rf "$WT/hidden"
if [ -d "$HIDDEN_SRC" ]; then
  cp -r "$HIDDEN_SRC" "$WT/hidden"
fi

HID_BUILD_OK=false; HID_TOTAL=0; HID_PASS=0; HID_FAILN=0; HID_FAILS=""
RACE_DETECTED=false; RACE_REPORTS=0

if [ -d "$WT/hidden" ]; then
  # ---- hidden pass rate: FULL suite WITHOUT -race (functional denominator)
  # Dropping -race here avoids t.Parallel amplification: one unfixed race
  # detected mid-suite can spuriously fail many innocent parallel tests.
  HID_OUT="$(go test -count=1 -v ./hidden/ 2>&1)"
  HID_TOTAL="$(printf '%s\n' "$HID_OUT" | grep -cE '^=== RUN   Test[^/]*$')"
  HID_FAILS="$(printf '%s\n' "$HID_OUT" | grep -E '^--- FAIL: ' | sed -E 's/^--- FAIL: ([^ ]+).*/\1/' | sort -u)"
  HID_FAILN="$(printf '%s\n' "$HID_FAILS" | grep -c . )"
  HID_BUILD_OK=true
  printf '%s\n' "$HID_OUT" | grep -qE 'build failed|cannot find|undefined:|missing go\.sum|no required module|setup failed|cannot load' && HID_BUILD_OK=false
  # A suite that actually compiled and ran always registers >0 tests; zero
  # total with a hidden/ dir present means the build/setup failed in a way
  # none of the known string patterns above caught (e.g. exotic module
  # resolution errors) — treat that as a build failure too.
  [ "$HID_TOTAL" -eq 0 ] && HID_BUILD_OK=false
  HID_PASS=$(( HID_TOTAL - HID_FAILN ))
  [ "$HID_TOTAL" -gt 0 ] || HID_PASS=0

  # ---- race diagnostic: one full run WITH -race, recorded separately -------
  # GORACE=halt_on_error=0 reports all races instead of aborting on the
  # first. No -v: verbose+race is ~10x slower and risks the 10m go test
  # default timeout truncating the run before the race report prints.
  RACE_OUT="$(GORACE='halt_on_error=0' go test -race -count=1 ./hidden/ 2>&1)"
  RACE_REPORTS="$(printf '%s\n' "$RACE_OUT" | grep -c 'WARNING: DATA RACE')"
  [ "${RACE_REPORTS:-0}" -gt 0 ] && RACE_DETECTED=true
fi

# ---- per-invariant-group scoring -------------------------------------------
# For each group in invariants.json, run its catcher tests by exact name in
# isolation (own `go test` invocation) so one failing catcher cannot mask or
# amplify-fail another via shared process state. A group passes iff every
# one of its catchers passes.
GROUPS_JSON="null"
if [ -f "$INVARIANTS" ] && [ -d "$WT/hidden" ]; then
  N=$(jq '.groups | length' "$INVARIANTS" 2>/dev/null || echo 0)
  results='[]'
  if [ "${N:-0}" -gt 0 ]; then
    for i in $(seq 0 $((N-1))); do
      gid=$(jq -r ".groups[$i].id" "$INVARIANTS")
      catchers=$(jq -r ".groups[$i].catchers[]" "$INVARIANTS")
      # Catcher names are interpolated unquoted into `go test -run
      # '^(a|b)$'`, a regex alternation, not shelled out — so this isn't
      # shell-injection, but any character outside [A-Za-z0-9_] (regex
      # metacharacters, `|`, `$`, whitespace, etc.) lets a name distort the
      # alternation and match/exclude tests it wasn't meant to. Reject
      # (skip, don't run) any name that doesn't match ^[A-Za-z0-9_]+$ and
      # record why in the group's "note" field instead of silently
      # swallowing it.
      safe_catchers="$(printf '%s\n' "$catchers" | grep -E '^[A-Za-z0-9_]+$' || true)"
      unsafe_catchers="$(printf '%s\n' "$catchers" | grep -vE '^[A-Za-z0-9_]+$' | grep -v '^$' || true)"
      names="$(printf '%s\n' "$safe_catchers" | paste -sd'|' -)"
      pass=false
      if [ -n "$names" ]; then
        go test -race -count=1 -run "^($names)\$" ./hidden/ >/dev/null 2>&1 && pass=true
      fi
      note=""
      if [ -n "$unsafe_catchers" ]; then
        note="skipped catcher name(s) with characters outside [A-Za-z0-9_] (unsafe to interpolate into go test -run regex): $(printf '%s' "$unsafe_catchers" | paste -sd', ' -)"
      fi
      results=$(jq -c --arg id "$gid" --argjson pass "$pass" --arg note "$note" \
        'if $note != "" then . += [{id:$id, pass:$pass, note:$note}] else . += [{id:$id, pass:$pass}] end' \
        <<<"$results")
    done
  fi
  GROUPS_JSON="$results"
fi

COMPLETE=false
{ $BUILD_OK && $VET_OK && $ACCEPT_OK; } && COMPLETE=true

jq -n \
  --argjson build "$BUILD_OK" --argjson vet "$VET_OK" --argjson accept "$ACCEPT_OK" \
  --argjson complete "$COMPLETE" \
  --argjson hbuild "$HID_BUILD_OK" --argjson htotal "$HID_TOTAL" \
  --argjson hpass "$HID_PASS" --argjson hfail "$HID_FAILN" \
  --arg hfails "$(printf '%s' "$HID_FAILS" | paste -sd'|' -)" \
  --argjson racedet "$RACE_DETECTED" --argjson racereports "$RACE_REPORTS" \
  --argjson groups "$GROUPS_JSON" \
  --argjson surface_ok "$SURFACE_OK" --arg surface_diff "$SURFACE_DIFF" \
  '{
     complete: $complete,
     visible_dod: {build:$build, vet:$vet, accept:$accept},
     hidden: {build_ok:$hbuild, total:$htotal, passed:$hpass, failed:$hfail,
              pass_rate: (if $htotal>0 then ($hpass/$htotal) else 0 end),
              failing: ($hfails | split("|") | map(select(length>0))),
              race_check: {data_race_detected: $racedet, race_reports: $racereports}},
     groups: $groups,
     surface: {surface_ok: $surface_ok,
               diff: ($surface_diff | split("\n") | map(select(length>0)))}
   }'
