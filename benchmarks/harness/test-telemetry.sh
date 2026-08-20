#!/usr/bin/env bash
# Telemetry test for the harness results row: the cc-nullius arm must fold the
# diet-governor hook's stats file into each rep's results.jsonl row, and must
# never abort a rep when that file is missing or malformed.
#
# No network, no real claude: `claude` is shimmed to emit a stream-json result
# event carrying a session_id, and (per case) to write the governor stats file
# the real hook writes at $TMPDIR/nullius-stats-<session_id>
# (cc-nullius/hooks/diet-governor.mjs). run.sh runs from a sandboxed COPY with
# a throwaway SEED_DIR task, writing into the sandbox results dir, so the real
# results.jsonl is never touched.
#
#   ./test-telemetry.sh          # exits 0 iff all cases pass
set -euo pipefail

HARNESS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$HARNESS_DIR/../.." && pwd)"

SANDBOX="$(mktemp -d)"
trap 'rm -rf "$SANDBOX"' EXIT

# Sandbox harness: the file under test, copied at run time. run.sh derives
# ROOT_DIR from its own location, so the cc-nullius plugin assets it copies
# into the worktree must exist alongside it.
SBROOT="$SANDBOX/repo"            # run.sh reads ROOT_DIR as <harness>/../..
mkdir -p "$SBROOT/benchmarks/harness"
cp "$HARNESS_DIR/run.sh" "$SBROOT/benchmarks/harness/run.sh"
mkdir -p "$SBROOT/cc-nullius"
cp -r "$ROOT_DIR/cc-nullius/agents" "$SBROOT/cc-nullius/agents"
mkdir -p "$SBROOT/cc-nullius/hooks"
cp "$ROOT_DIR/cc-nullius/hooks/diet-governor.mjs" "$SBROOT/cc-nullius/hooks/"
# Skill body is force-injected by run.sh from cc-nullius/skills/starve/SKILL.md;
# a stub keeps this test about telemetry, not about doctrine text.
mkdir -p "$SBROOT/cc-nullius/skills/starve"
printf -- '---\nname: starve\n---\nstub doctrine\n' > "$SBROOT/cc-nullius/skills/starve/SKILL.md"

# Minimal SEED_DIR task (no external repo) with a trivial task-local scorer.
TASK="$SBROOT/benchmarks/harness/tasks/fake"
mkdir -p "$TASK/seed"
echo hello > "$TASK/seed/file.txt"
printf 'SEED_DIR=seed\nTIMEOUT_S=60\n' > "$TASK/meta.env"
echo "fake prompt" > "$TASK/prompt.md"
printf '#!/usr/bin/env bash\necho '\''{"complete":true}'\''\n' > "$TASK/score.sh"
chmod +x "$TASK/score.sh"

# claude shim: drains stdin, optionally writes the governor stats file exactly
# where diet-governor.mjs does, then emits the stream-json result event.
mkdir -p "$SANDBOX/bin"
cat > "$SANDBOX/bin/claude" <<'EOF'
#!/usr/bin/env bash
cat > /dev/null
statssession="${SHIM_STATS_SESSION:-$SHIM_SESSION}"
[[ -n "${SHIM_STATS_BODY:-}" ]] \
  && printf '%s' "$SHIM_STATS_BODY" > "${TMPDIR:-/tmp}/nullius-stats-$statssession"
if [[ -n "${SHIM_STATS_EXTRA:-}" ]]; then
  extrasession="${SHIM_STATS_EXTRA%%:*}"
  extrabody="${SHIM_STATS_EXTRA#*:}"
  printf '%s' "$extrabody" > "${TMPDIR:-/tmp}/nullius-stats-$extrasession"
fi
echo "{\"type\":\"result\",\"session_id\":\"$SHIM_SESSION\",\"total_cost_usd\":0,\"num_turns\":1}"
EOF
chmod +x "$SANDBOX/bin/claude"

# docker shim: stands in for `docker run` in CONTAINER=1 mode. Records the full
# argv for assertion, then plays the container's part — writes the governor
# stats file into whatever host dir is bind-mounted at the container TMPDIR,
# and emits the stream-json result event.
cat > "$SANDBOX/bin/docker" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$@" > "$DOCKER_ARGV_FILE"
cat > /dev/null
ctmpdir=""; for a in "$@"; do [[ "$a" == -e ]] && continue
  [[ "$a" == TMPDIR=* ]] && ctmpdir="${a#TMPDIR=}"; done
host=""; prev=""
for a in "$@"; do
  [[ "$prev" == -v && -n "$ctmpdir" && "$a" == *":$ctmpdir" ]] && host="${a%":$ctmpdir"}"
  prev="$a"
done
[[ -n "${SHIM_STATS_BODY:-}" && -n "$host" ]] \
  && printf '%s' "$SHIM_STATS_BODY" > "$host/nullius-stats-$SHIM_SESSION"
echo "{\"type\":\"result\",\"session_id\":\"$SHIM_SESSION\",\"total_cost_usd\":0,\"num_turns\":1}"
EOF
chmod +x "$SANDBOX/bin/docker"

PASS=0; FAIL=0
ok() { echo "PASS $1"; PASS=$((PASS+1)); }
bad() { echo "FAIL $1: $2"; FAIL=$((FAIL+1)); }

# run_case <name> <session> <stats-body> — one cc-nullius rep on the host
# path; echoes the rep's exit status and appends one row to the sandbox
# results.jsonl (RESULTS_DIR is derived from run.sh's own location).
run_case() {
  local name="$1" session="$2" body="$3"
  local tmp="$SANDBOX/tmp-$name"; mkdir -p "$tmp"
  local rc=0
  env PATH="$SANDBOX/bin:$PATH" TMPDIR="$tmp" \
      SHIM_SESSION="$session" ${body:+SHIM_STATS_BODY="$body"} \
      ${SHIM_STATS_SESSION:+SHIM_STATS_SESSION="$SHIM_STATS_SESSION"} \
      ${SHIM_STATS_EXTRA:+SHIM_STATS_EXTRA="$SHIM_STATS_EXTRA"} \
      bash "$SBROOT/benchmarks/harness/run.sh" "$TASK" cc-nullius \
      >"$SANDBOX/out-$name" 2>"$SANDBOX/err-$name" || rc=$?
  echo "$rc"
}

# row — the last results.jsonl row written (cases run one at a time).
row() { tail -1 "$SBROOT/benchmarks/harness/results/results.jsonl" 2>/dev/null || echo '{}'; }

# A. Stats file present and well-formed → its counters land under
# governor_stats, distinct from any go-nullius field.
STATS='{"denies":7,"dispatches":3,"dispatch:scout":2}'
RC="$(run_case good sess-good "$STATS")"
if [[ "$RC" != 0 ]]; then
  bad good "rep exited $RC ($(tail -3 "$SANDBOX/err-good" | tr '\n' ' '))"
elif [[ "$(jq -c '.governor_stats' <<<"$(row)")" != "$STATS" ]]; then
  bad good "governor_stats = $(jq -c '.governor_stats' <<<"$(row)"), want $STATS"
else
  ok good
fi

# B. Stats file absent → the rep still completes and still records its row,
# with governor_stats null. Telemetry must never kill a rep.
RC="$(run_case missing sess-missing "")"
if [[ "$RC" != 0 ]]; then
  bad missing "rep exited $RC ($(tail -3 "$SANDBOX/err-missing" | tr '\n' ' '))"
elif [[ "$(jq -r '.governor_stats' <<<"$(row)")" != "null" ]]; then
  bad missing "governor_stats = $(jq -c '.governor_stats' <<<"$(row)"), want null"
elif ! grep -q 'governor stats file missing' "$SANDBOX/err-missing"; then
  bad missing "no one-line note about the missing stats file"
else
  ok missing
fi

# C. Stats file malformed → same: row recorded, governor_stats null, note.
RC="$(run_case bad sess-bad 'not json{')"
if [[ "$RC" != 0 ]]; then
  bad bad "rep exited $RC ($(tail -3 "$SANDBOX/err-bad" | tr '\n' ' '))"
elif [[ "$(jq -r '.governor_stats' <<<"$(row)")" != "null" ]]; then
  bad bad "governor_stats = $(jq -c '.governor_stats' <<<"$(row)"), want null"
elif ! grep -q 'governor stats file malformed' "$SANDBOX/err-bad"; then
  bad bad "no one-line note about the malformed stats file"
else
  ok bad
fi

# D. CONTAINER=1 → the docker invocation must bind-mount a host dir and point
# the container's TMPDIR at it, so the hook's stats file (written inside the
# container) is readable here and lands in the row.
DARGV="$SANDBOX/docker-argv"
RC="$(DOCKER_ARGV_FILE="$DARGV" CONTAINER=1 ANTHROPIC_API_KEY=sk-ant-api03-fake \
        run_case container sess-ctr "$STATS")"
CTMPDIR="$(grep -m1 '^TMPDIR=' "$DARGV" 2>/dev/null | sed 's/^TMPDIR=//' || true)"
if [[ "$RC" != 0 ]]; then
  bad container "rep exited $RC ($(tail -3 "$SANDBOX/err-container" | tr '\n' ' '))"
elif [[ -z "$CTMPDIR" ]]; then
  bad container "docker argv carries no -e TMPDIR=..."
elif ! grep -qx -- "[^:]*:$CTMPDIR" "$DARGV"; then
  bad container "docker argv has no host bind mount at $CTMPDIR"
elif [[ "$(jq -c '.governor_stats' <<<"$(row)")" != "$STATS" ]]; then
  bad container "governor_stats = $(jq -c '.governor_stats' <<<"$(row)"), want $STATS"
else
  ok container
fi

# E. Stats file lands under a session id DIFFERENT from the one the result
# JSON reports (the known divergence between hook payload id and CLI result
# id) → the harness must still find and use it, not report it missing.
RC="$(SHIM_STATS_SESSION="probe-real-e" run_case mismatch sess-mismatch-e "$STATS")"
if [[ "$RC" != 0 ]]; then
  bad mismatch "rep exited $RC ($(tail -3 "$SANDBOX/err-mismatch" | tr '\n' ' '))"
elif [[ "$(jq -c '.governor_stats' <<<"$(row)")" != "$STATS" ]]; then
  bad mismatch "governor_stats = $(jq -c '.governor_stats' <<<"$(row)"), want $STATS"
elif ! grep -q 'different session id' "$SANDBOX/err-mismatch"; then
  bad mismatch "no one-line note about the session id mismatch"
else
  ok mismatch
fi

# F. TWO stats files land under different (non-matching) session ids →
# the harness must merge their flat integer counters by summing.
# String-valued keys are live in the real stats file (diet-governor writes
# turn:id), and summing them made jq error out and silently null the whole
# row. Numbers add; non-numbers take the last file's value.
STATS_A='{"denies":2,"dispatches":1,"turn:id":"msg-a"}'
STATS_B='{"denies":5,"dispatches":3,"escapes":1,"turn:id":"msg-b"}'
RC="$(SHIM_STATS_SESSION="probe-merge-a" SHIM_STATS_EXTRA="probe-merge-b:$STATS_B" \
        run_case merge sess-merge-f "$STATS_A")"
WANT='{"denies":7,"dispatches":4,"turn:id":"msg-b","escapes":1}'
if [[ "$RC" != 0 ]]; then
  bad merge "rep exited $RC ($(tail -3 "$SANDBOX/err-merge" | tr '\n' ' '))"
elif [[ "$(jq -c '.governor_stats' <<<"$(row)")" != "$WANT" ]]; then
  bad merge "governor_stats = $(jq -c '.governor_stats' <<<"$(row)"), want $WANT"
elif ! grep -q 'merged 2 governor stats files' "$SANDBOX/err-merge"; then
  bad merge "no one-line note about the merge"
else
  ok merge
fi

echo "----"
echo "telemetry: $PASS passed, $FAIL failed"
[[ "$FAIL" -eq 0 ]]
