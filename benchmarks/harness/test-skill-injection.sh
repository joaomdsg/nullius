#!/usr/bin/env bash
# Skill-injection test for the cc-nullius arm: the doctrine body force-injected
# via --append-system-prompt must be NON-EMPTY, and a missing/empty skill file
# must FAIL THE REP LOUDLY instead of dispatching a doctrine-less run.
#
# No network, no real claude: `claude` is shimmed to record its argv and emit a
# stream-json result event. run.sh runs from a sandboxed COPY with a throwaway
# SEED_DIR task, so the real results.jsonl is never touched.
#
#   ./test-skill-injection.sh    # exits 0 iff all cases pass
set -euo pipefail

HARNESS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$HARNESS_DIR/../.." && pwd)"

SANDBOX="$(mktemp -d)"
trap 'rm -rf "$SANDBOX"' EXIT

SBROOT="$SANDBOX/repo"            # run.sh reads ROOT_DIR as <harness>/../..
mkdir -p "$SBROOT/benchmarks/harness"
cp "$HARNESS_DIR/run.sh" "$SBROOT/benchmarks/harness/run.sh"
# The REAL plugin assets: this test is about the arm finding the real doctrine.
cp -r "$ROOT_DIR/cc-nullius" "$SBROOT/cc-nullius"

TASK="$SBROOT/benchmarks/harness/tasks/fake"
mkdir -p "$TASK/seed"
echo hello > "$TASK/seed/file.txt"
printf 'SEED_DIR=seed\nTIMEOUT_S=60\n' > "$TASK/meta.env"
echo "fake prompt" > "$TASK/prompt.md"
printf '#!/usr/bin/env bash\necho '\''{"complete":true}'\''\n' > "$TASK/score.sh"
chmod +x "$TASK/score.sh"

mkdir -p "$SANDBOX/bin"
cat > "$SANDBOX/bin/claude" <<'EOF'
#!/usr/bin/env bash
cat > /dev/null
printf '%s\n' "$@" > "$SHIM_ARGV"
echo '{"type":"result","session_id":"s","total_cost_usd":0,"num_turns":1}'
EOF
chmod +x "$SANDBOX/bin/claude"

PASS=0; FAIL=0
ok() { echo "PASS $1"; PASS=$((PASS+1)); }
bad() { echo "FAIL $1: $2"; FAIL=$((FAIL+1)); }

run_case() {
  local name="$1" rc=0
  local tmp="$SANDBOX/tmp-$name"; mkdir -p "$tmp"
  rm -f "$SANDBOX/argv-$name"
  env PATH="$SANDBOX/bin:$PATH" TMPDIR="$tmp" SHIM_ARGV="$SANDBOX/argv-$name" \
      bash "$SBROOT/benchmarks/harness/run.sh" "$TASK" cc-nullius \
      >"$SANDBOX/out-$name" 2>"$SANDBOX/err-$name" || rc=$?
  echo "$rc"
}

# The doctrine body run.sh should have injected: the real skill's body, sans
# frontmatter. Its first heading is the invariant we assert reached the CLI.
SKILL_MD="$ROOT_DIR/cc-nullius/skills/starve/SKILL.md"
MARKER="$(awk 'f && NF {print; exit} /^---[[:space:]]*$/{c++; if(c==2) f=1}' "$SKILL_MD")"

# A. Real assets → rep completes and the injected system prompt carries the
#    doctrine body (not an empty tail after "The doctrine:").
RC="$(run_case real)"
if [[ "$RC" != 0 ]]; then
  bad real "rep exited $RC ($(tail -3 "$SANDBOX/err-real" | tr '\n' ' '))"
elif ! grep -qF -- "$MARKER" "$SANDBOX/argv-real"; then
  bad real "injected prompt lacks the skill body marker: $MARKER"
else
  ok real
fi

# B. Skill file missing → FAIL FAST: nonzero exit, explicit error naming the
#    path, and claude never invoked (no doctrine-less rep).
mv "$SBROOT/cc-nullius/skills" "$SBROOT/cc-nullius/skills-off"
RC="$(run_case missing)"
if [[ "$RC" == 0 ]]; then
  bad missing "rep exited 0 with no skill file"
elif [[ -e "$SANDBOX/argv-missing" ]]; then
  bad missing "claude was invoked despite the missing skill"
elif ! grep -qi 'skill' "$SANDBOX/err-missing"; then
  bad missing "no clear error naming the skill: $(tail -2 "$SANDBOX/err-missing")"
else
  ok missing
fi
mv "$SBROOT/cc-nullius/skills-off" "$SBROOT/cc-nullius/skills"

# C. Skill present but body empty (frontmatter only) → same fail-fast.
: > "$SBROOT/cc-nullius/skills/starve/SKILL.md"
printf -- '---\nname: starve\n---\n' > "$SBROOT/cc-nullius/skills/starve/SKILL.md"
RC="$(run_case empty)"
if [[ "$RC" == 0 ]]; then
  bad empty "rep exited 0 with an empty skill body"
elif [[ -e "$SANDBOX/argv-empty" ]]; then
  bad empty "claude was invoked with an empty doctrine body"
else
  ok empty
fi

echo "----"
echo "skill-injection: $PASS passed, $FAIL failed"
[[ "$FAIL" -eq 0 ]]
