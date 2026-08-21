#!/usr/bin/env bash
# test-bank.sh — bank.sh resolves each named bank to a PINNED env, and run.sh
# stamps that env into the row. Both halves matter: the pin is worthless if the
# row does not record what actually ran (a fable-5 leader once slipped into an
# all-opus-5 comparison set unrecorded).
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
ROOT_DIR="$(git rev-parse --show-toplevel)"
PASS=0; FAIL=0
ok()  { echo "PASS $1"; PASS=$((PASS+1)); }
bad() { echo "FAIL $1: ${2:-}"; FAIL=$((FAIL+1)); }

SANDBOX="$(mktemp -d)"; trap 'rm -rf "$SANDBOX"' EXIT
FAKECRED="$SANDBOX/cred.json"; echo '{"fake":true}' > "$FAKECRED"

# A. --list names every bank exactly once.
LIST="$(./bank.sh --list)"
for b in cc-nullius-blind cc-nullius-pointed plain-blind plain-pointed; do
  [[ "$(grep -c "^$b " <<<"$LIST")" == 1 ]] && ok "list-$b" || bad "list-$b" "not listed exactly once"
done

# B. an unknown bank refuses with exit 2 and prints the valid names.
out="$(CRED_FILE="$FAKECRED" ./bank.sh no-such-bank 2>&1)"; rc=$?
[[ "$rc" == 2 && "$out" == *"cc-nullius-pointed"* ]] \
  && ok "unknown-bank-refused" || bad "unknown-bank-refused" "exit $rc: $out"

# C. every bank's dry run pins a leader model explicitly — never a run.sh default.
while read -r name _task _arm _label _env; do
  [[ -z "$name" ]] && continue
  d="$(CRED_FILE="$FAKECRED" ./bank.sh "$name" --dry-run 2>&1)"
  if [[ "$d" == *"claude-opus-5"* && "$d" == *"CONTAINER=1"* ]]; then
    ok "pinned-$name"
  else
    bad "pinned-$name" "$(tr '\n' ' ' <<<"$d")"
  fi
done < <(./bank.sh --list | tail -n +2)

# D. a dry run spends nothing: it must not invoke the runner at all.
cat > "$SANDBOX/loud-run.sh" <<'EOF'
#!/usr/bin/env bash
echo "RUNNER-INVOKED"
EOF
chmod +x "$SANDBOX/loud-run.sh"
d="$(RUN_SH="$SANDBOX/loud-run.sh" CRED_FILE="$FAKECRED" ./bank.sh plain-pointed --dry-run 2>&1)"
[[ "$d" != *RUNNER-INVOKED* ]] && ok "dry-run-spends-nothing" || bad "dry-run-spends-nothing" "runner ran"

# E. a real run forwards the pinned env and the bank name to the runner.
cat > "$SANDBOX/echo-run.sh" <<'EOF'
#!/usr/bin/env bash
echo "ARGS=$*"
echo "PLAIN_MODEL=${PLAIN_MODEL:-unset} LABEL=${LABEL:-unset} BANK_NAME=${BANK_NAME:-unset} CONTAINER=${CONTAINER:-unset}"
EOF
chmod +x "$SANDBOX/echo-run.sh"
r="$(RUN_SH="$SANDBOX/echo-run.sh" CRED_FILE="$FAKECRED" ./bank.sh plain-pointed --reps 2 2>&1)"
if [[ "$r" == *"ARGS=tasks/stats-rolling-pointed plain --reps 2"* \
   && "$r" == *"PLAIN_MODEL=claude-opus-5"* \
   && "$r" == *"LABEL=plain-opus-5-low"* \
   && "$r" == *"BANK_NAME=plain-pointed"* ]]; then
  ok "forwards-pinned-env"
else
  bad "forwards-pinned-env" "$(tr '\n' ' ' <<<"$r")"
fi

# F. a bank naming a missing task dir fails BEFORE spending (exit 3).
cp bank.sh "$SANDBOX/bank-badtask.sh"
sed -i 's|stats-rolling-pointed  plain |does-not-exist         plain |' "$SANDBOX/bank-badtask.sh"
out="$(cd "$SANDBOX" && ln -sf "$PWD" x 2>/dev/null; CRED_FILE="$FAKECRED" bash "$SANDBOX/bank-badtask.sh" plain-pointed --dry-run 2>&1)"; rc=$?
[[ "$rc" == 3 && "$out" == *"missing task dir"* ]] \
  && ok "missing-task-refused" || bad "missing-task-refused" "exit $rc: $(tr '\n' ' ' <<<"$out")"

# G. run.sh stamps resolved_env into the row, with the leader it really used.
#    Shim claude so no model is ever contacted.
SB="$SANDBOX/repo"; mkdir -p "$SB/benchmarks/harness" "$SB/cc-nullius/hooks" "$SB/bin"
cp run.sh "$SB/benchmarks/harness/"
cp "$ROOT_DIR/cc-nullius/hooks/diet-governor.mjs" "$SB/cc-nullius/hooks/" 2>/dev/null || true
mkdir -p "$SB/cc-nullius/skills/starve"
printf -- '---\nname: starve\n---\nstub doctrine\n' > "$SB/cc-nullius/skills/starve/SKILL.md"
T="$SB/benchmarks/harness/tasks/fake"; mkdir -p "$T/seed"
echo hi > "$T/seed/f.txt"
printf 'SEED_DIR=seed\nTIMEOUT_S=60\n' > "$T/meta.env"
echo "fake prompt" > "$T/prompt.md"
printf '#!/usr/bin/env bash\necho '\''{"complete":true}'\''\n' > "$T/score.sh"; chmod +x "$T/score.sh"
cat > "$SB/bin/claude" <<'EOF'
#!/usr/bin/env bash
cat > /dev/null
echo '{"type":"result","session_id":"sess-bank-g","total_cost_usd":0,"num_turns":1}'
EOF
chmod +x "$SB/bin/claude"
( cd "$SB" && git init -q . && git add -A >/dev/null 2>&1 && \
  git -c user.email=t@t -c user.name=t commit -qm init >/dev/null 2>&1 ) || true
row="$(cd "$SB/benchmarks/harness" && PATH="$SB/bin:$PATH" \
       PLAIN_MODEL=claude-opus-5 PLAIN_EFFORT=low BANK_NAME=probe-bank \
       bash run.sh tasks/fake plain --reps 1 2>/dev/null | grep '^{' | tail -1)"
if [[ -z "$row" ]]; then
  bad "resolved-env-stamped" "no row emitted"
else
  got="$(jq -c '.resolved_env|{bank,leader,container}' <<<"$row" 2>/dev/null)"
  if [[ "$got" == *'"bank":"probe-bank"'* && "$got" == *'"model":"claude-opus-5"'* && "$got" == *'"effort":"low"'* ]]; then
    ok "resolved-env-stamped"
  else
    bad "resolved-env-stamped" "resolved_env=$got"
  fi
fi

echo "----"
echo "bank: $PASS passed, $FAIL failed"
[[ "$FAIL" -eq 0 ]]
