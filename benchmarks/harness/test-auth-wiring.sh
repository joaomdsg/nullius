#!/usr/bin/env bash
# Auth-wiring test for the harness credential resolution:
#   - run.sh (CONTAINER=1): which single env var enters the container
#   - judge.sh: which credential vars the host `claude` invocation sees
#
# No network, no real docker, no real claude: `docker` is shimmed to record
# its argv (one element per line) plus which credential vars were visible in
# its environment, and emit a minimal stream-json result event so run.sh's
# scoring path completes; `claude` is shimmed to record credential set-ness
# (${VAR+x}, so an exported-but-empty var is caught) and emit parseable judge
# output. run.sh cases run a sandboxed COPY of run.sh with a throwaway
# SEED_DIR task, so the real results.jsonl is never touched; every credential
# value is a fake.
#
#   ./test-auth-wiring.sh        # exits 0 iff all cases pass
set -euo pipefail

HARNESS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

SANDBOX="$(mktemp -d)"
trap 'rm -rf "$SANDBOX"' EXIT

# Sandbox harness: the file under test, copied at run time.
mkdir -p "$SANDBOX/harness"
cp "$HARNESS_DIR/run.sh" "$SANDBOX/harness/run.sh"

# Minimal SEED_DIR task (no external repo) with a trivial task-local scorer.
TASK="$SANDBOX/harness/tasks/fake"
mkdir -p "$TASK/seed"
echo hello > "$TASK/seed/file.txt"
printf 'SEED_DIR=seed\nTIMEOUT_S=60\n' > "$TASK/meta.env"
echo "fake prompt" > "$TASK/prompt.md"
printf '#!/usr/bin/env bash\necho '\''{"complete":true}'\''\n' > "$TASK/score.sh"
chmod +x "$TASK/score.sh"

# docker shim: records argv + credential set-ness, drains stdin, emits a
# result event. APIKEY_IN_ENV/OAUTH_IN_ENV prove the chosen var was actually
# EXPORTED (docker -e VAR reads the docker client's environment).
mkdir -p "$SANDBOX/bin"
cat > "$SANDBOX/bin/docker" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$@" > "$DOCKER_ARGS_FILE"
[[ -n "${ANTHROPIC_API_KEY+x}" ]] && echo APIKEY_IN_ENV >> "$DOCKER_ARGS_FILE"
[[ -n "${CLAUDE_CODE_OAUTH_TOKEN+x}" ]] && echo OAUTH_IN_ENV >> "$DOCKER_ARGS_FILE"
# Record the per-rep home mount and whether a credential COPY landed in it —
# by path and mode only; the shim never reads the file's contents.
for a in "$@"; do
  if [[ "$a" == *:/home/agent ]]; then
    h="${a%:/home/agent}"
    echo "CHOME_MOUNT" >> "$DOCKER_ARGS_FILE"
    if [[ -f "$h/.claude/.credentials.json" ]]; then
      echo "CRED_COPY_PRESENT" >> "$DOCKER_ARGS_FILE"
      echo "CRED_COPY_MODE=$(stat -c '%a' "$h/.claude/.credentials.json")" >> "$DOCKER_ARGS_FILE"
    fi
  fi
done
cat > /dev/null
echo '{"type":"result","result":"ok","total_cost_usd":0}'
EOF
chmod +x "$SANDBOX/bin/docker"

# claude shim (for judge.sh): records credential SET-NESS — ${VAR+x} is true
# for an exported-but-empty var, exactly the shadowing case judge.sh must
# avoid — then emits a parseable judge result.
cat > "$SANDBOX/bin/claude" <<'EOF'
#!/usr/bin/env bash
{ [[ -n "${ANTHROPIC_API_KEY+x}" ]] && echo APIKEY_SET
  [[ -n "${CLAUDE_CODE_OAUTH_TOKEN+x}" ]] && echo OAUTH_SET
  : ; } > "$CLAUDE_ENV_FILE"
cat > /dev/null
echo '{"result":"{\"disclosures\":[]}"}'
EOF
chmod +x "$SANDBOX/bin/claude"

# Fixtures for judge.sh.
echo report > "$SANDBOX/report.txt"
echo diff > "$SANDBOX/diff.txt"
echo '{"defects":[{"id":"d1","summary":"s","todo_impact":"i"}]}' > "$SANDBOX/defects.json"

PASS=0; FAIL=0

# run_case <name> <expected-exit> [VAR=val ...] — runs the sandboxed run.sh
# with a clean slate of every credential variable, then the given ones set.
run_case() {
  local name="$1" want_rc="$2"; shift 2
  local args_file="$SANDBOX/args-$name"
  : > "$args_file"
  local rc=0
  env -u ANTHROPIC_API_KEY -u NULLIUS_ANTHROPIC_API_KEY -u BYPROXY_ANTHROPIC_API_KEY \
      -u CLAUDE_CODE_OAUTH_TOKEN -u NULLIUS_CLAUDE_CODE_OAUTH_TOKEN \
      -u NULLIUS_CLAUDE_CODE_OAUTH_TOKEN_FILE -u CRED_FILE \
      PATH="$SANDBOX/bin:$PATH" DOCKER_ARGS_FILE="$args_file" CONTAINER=1 "$@" \
      bash "$SANDBOX/harness/run.sh" "$TASK" plain \
      >"$SANDBOX/out-$name" 2>"$SANDBOX/err-$name" || rc=$?
  if [[ "$rc" -ne "$want_rc" ]]; then
    echo "FAIL $name: exit $rc, want $want_rc ($(tail -3 "$SANDBOX/err-$name" | tr '\n' ' '))"
    FAIL=$((FAIL+1)); return 1
  fi
  return 0
}

# run_judge_case <name> [VAR=val ...] — runs the REAL judge.sh (writes nothing)
# with the shimmed claude recording what it sees.
run_judge_case() {
  local name="$1"; shift
  local envf="$SANDBOX/claude-env-$name"
  : > "$envf"
  local rc=0
  env -u ANTHROPIC_API_KEY -u NULLIUS_ANTHROPIC_API_KEY -u BYPROXY_ANTHROPIC_API_KEY \
      -u CLAUDE_CODE_OAUTH_TOKEN -u NULLIUS_CLAUDE_CODE_OAUTH_TOKEN \
      -u NULLIUS_CLAUDE_CODE_OAUTH_TOKEN_FILE \
      PATH="$SANDBOX/bin:$PATH" CLAUDE_ENV_FILE="$envf" "$@" \
      bash "$HARNESS_DIR/judge.sh" "$SANDBOX/report.txt" "$SANDBOX/diff.txt" "$SANDBOX/defects.json" \
      >"$SANDBOX/judge-out-$name" 2>&1 || rc=$?
  if [[ "$rc" -ne 0 ]]; then
    echo "FAIL $name: judge.sh exit $rc ($(tail -2 "$SANDBOX/judge-out-$name" | tr '\n' ' '))"
    FAIL=$((FAIL+1)); return 1
  fi
  return 0
}

# assert_marks <file> <name> <must-have|-> <must-not-have|-> — exact-line greps.
assert_marks() {
  local file="$1" name="$2" yes="$3" no="$4"
  if [[ "$yes" != "-" ]] && ! grep -qxF "$yes" "$file"; then
    echo "FAIL $name: missing '$yes'"; FAIL=$((FAIL+1)); return 1
  fi
  if [[ "$no" != "-" ]] && grep -qxF "$no" "$file"; then
    echo "FAIL $name: unexpectedly contains '$no'"; FAIL=$((FAIL+1)); return 1
  fi
  return 0
}

ok() { echo "PASS $1"; PASS=$((PASS+1)); }

## ---- run.sh CONTAINER=1 credential resolution -----------------------------
# A bare `-e VAR` shows up in the argv file as a line holding exactly VAR
# (VAR=value forms like HOME=/home/agent don't match an exact-line grep).

# A. API key → container gets ANTHROPIC_API_KEY only, and it is exported.
if run_case apikey 0 ANTHROPIC_API_KEY=sk-ant-api03-fake; then
  assert_marks "$SANDBOX/args-apikey" apikey ANTHROPIC_API_KEY CLAUDE_CODE_OAUTH_TOKEN \
    && assert_marks "$SANDBOX/args-apikey" apikey APIKEY_IN_ENV - \
    && ok apikey
fi

# B. OAuth via CLAUDE_CODE_OAUTH_TOKEN → container gets the oauth var only.
if run_case oauth 0 CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-fake; then
  assert_marks "$SANDBOX/args-oauth" oauth CLAUDE_CODE_OAUTH_TOKEN ANTHROPIC_API_KEY \
    && assert_marks "$SANDBOX/args-oauth" oauth OAUTH_IN_ENV - \
    && ok oauth
fi

# C. Namespaced NULLIUS_CLAUDE_CODE_OAUTH_TOKEN maps to the container's oauth
# var — and must be exported by run.sh (the namespaced var never is).
if run_case oauth-ns 0 NULLIUS_CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-fake; then
  assert_marks "$SANDBOX/args-oauth-ns" oauth-ns CLAUDE_CODE_OAUTH_TOKEN ANTHROPIC_API_KEY \
    && assert_marks "$SANDBOX/args-oauth-ns" oauth-ns OAUTH_IN_ENV - \
    && ok oauth-ns
fi

# D. OAuth token in the API-key slot is ROUTED to the oauth var, not rejected.
if run_case oauth-routed 0 ANTHROPIC_API_KEY=sk-ant-oat01-fake; then
  assert_marks "$SANDBOX/args-oauth-routed" oauth-routed CLAUDE_CODE_OAUTH_TOKEN ANTHROPIC_API_KEY \
    && assert_marks "$SANDBOX/args-oauth-routed" oauth-routed OAUTH_IN_ENV - \
    && ok oauth-routed
fi

# E. Both set → the API key wins; exactly one credential enters the container.
if run_case both 0 ANTHROPIC_API_KEY=sk-ant-api03-fake CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-fake; then
  assert_marks "$SANDBOX/args-both" both ANTHROPIC_API_KEY CLAUDE_CODE_OAUTH_TOKEN && ok both
fi

# K. Token by FILE REFERENCE: only a path sits in the environment; the token
# is read at runtime. Trailing newline must be stripped ($(<file) semantics).
echo sk-ant-oat01-fromfile > "$SANDBOX/token-file"
if run_case oauth-file 0 NULLIUS_CLAUDE_CODE_OAUTH_TOKEN_FILE="$SANDBOX/token-file"; then
  assert_marks "$SANDBOX/args-oauth-file" oauth-file CLAUDE_CODE_OAUTH_TOKEN ANTHROPIC_API_KEY \
    && assert_marks "$SANDBOX/args-oauth-file" oauth-file OAUTH_IN_ENV - \
    && ok oauth-file
fi

# L. Unreadable token file → fail fast (exit 3), docker never invoked.
if run_case oauth-file-missing 3 NULLIUS_CLAUDE_CODE_OAUTH_TOKEN_FILE="$SANDBOX/no-such-file"; then
  if [[ -s "$SANDBOX/args-oauth-file-missing" ]]; then
    echo "FAIL oauth-file-missing: docker was invoked despite unreadable token file"; FAIL=$((FAIL+1))
  else
    ok oauth-file-missing
  fi
fi

# F. No credential → fail fast (exit 3), docker never invoked.
if run_case none 3; then
  if [[ -s "$SANDBOX/args-none" ]]; then
    echo "FAIL none: docker was invoked despite missing credentials"; FAIL=$((FAIL+1))
  else
    ok none
  fi
fi

## ---- CRED_FILE: host OAuth login copied into the throwaway home -----------
# Fixture only: a DUMMY credentials JSON in the sandbox, never the real one.
echo '{"claudeAiOauth":{"accessToken":"fake"}}' > "$SANDBOX/cred.json"
: > "$SANDBOX/cred-empty.json"

# N. CRED_FILE + CONTAINER=1 → a mode-600 copy lands in $CHOME/.claude/, and
# the docker argv still mounts $CHOME.
if run_case credfile 0 CRED_FILE="$SANDBOX/cred.json"; then
  assert_marks "$SANDBOX/args-credfile" credfile CHOME_MOUNT - \
    && assert_marks "$SANDBOX/args-credfile" credfile CRED_COPY_PRESENT - \
    && assert_marks "$SANDBOX/args-credfile" credfile CRED_COPY_MODE=600 - \
    && ok credfile
fi

# O. CRED_FILE with NO env credential → the run proceeds (AUTH_ENV empty), so
# neither credential var is passed into the container.
if run_case credfile-noenv 0 CRED_FILE="$SANDBOX/cred.json"; then
  assert_marks "$SANDBOX/args-credfile-noenv" credfile-noenv - ANTHROPIC_API_KEY \
    && assert_marks "$SANDBOX/args-credfile-noenv" credfile-noenv - CLAUDE_CODE_OAUTH_TOKEN \
    && ok credfile-noenv
fi

# P. CRED_FILE + env credential → env wins, and one line says so.
if run_case credfile-both 0 CRED_FILE="$SANDBOX/cred.json" ANTHROPIC_API_KEY=sk-ant-api03-fake; then
  assert_marks "$SANDBOX/args-credfile-both" credfile-both ANTHROPIC_API_KEY CLAUDE_CODE_OAUTH_TOKEN \
    && { grep -q 'env credential wins' "$SANDBOX/err-credfile-both" \
         || { echo "FAIL credfile-both: no 'env credential wins' log line"; FAIL=$((FAIL+1)); false; }; } \
    && ok credfile-both
fi

# Q. CRED_FILE pointing at a missing file → fail fast, docker never invoked.
if run_case credfile-missing 3 CRED_FILE="$SANDBOX/no-such-cred.json"; then
  if [[ -s "$SANDBOX/args-credfile-missing" ]]; then
    echo "FAIL credfile-missing: docker was invoked despite a missing CRED_FILE"; FAIL=$((FAIL+1))
  else
    ok credfile-missing
  fi
fi

# Q2. CRED_FILE pointing at an EMPTY file → fail fast.
if run_case credfile-empty 3 CRED_FILE="$SANDBOX/cred-empty.json"; then
  if [[ -s "$SANDBOX/args-credfile-empty" ]]; then
    echo "FAIL credfile-empty: docker was invoked despite an empty CRED_FILE"; FAIL=$((FAIL+1))
  else
    ok credfile-empty
  fi
fi

# R. CRED_FILE with CONTAINER=0 is unsupported → fail fast.
if run_case credfile-nocontainer 3 CRED_FILE="$SANDBOX/cred.json" CONTAINER=0; then
  ok credfile-nocontainer
fi

## ---- judge.sh credential handling ------------------------------------------

# G. API key flows through to the judge's claude untouched.
if run_judge_case judge-apikey ANTHROPIC_API_KEY=sk-ant-api03-fake; then
  assert_marks "$SANDBOX/claude-env-judge-apikey" judge-apikey APIKEY_SET OAUTH_SET && ok judge-apikey
fi

# H. OAuth token in the API-key slot reaches claude as CLAUDE_CODE_OAUTH_TOKEN;
# ANTHROPIC_API_KEY must be gone entirely (not left exported-but-empty).
if run_judge_case judge-oauth-routed ANTHROPIC_API_KEY=sk-ant-oat01-fake; then
  assert_marks "$SANDBOX/claude-env-judge-oauth-routed" judge-oauth-routed OAUTH_SET APIKEY_SET && ok judge-oauth-routed
fi

# I. Namespaced oauth var maps through to claude.
if run_judge_case judge-oauth-ns NULLIUS_CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-fake; then
  assert_marks "$SANDBOX/claude-env-judge-oauth-ns" judge-oauth-ns OAUTH_SET APIKEY_SET && ok judge-oauth-ns
fi

# M. judge.sh honors the file reference too.
if run_judge_case judge-oauth-file NULLIUS_CLAUDE_CODE_OAUTH_TOKEN_FILE="$SANDBOX/token-file"; then
  assert_marks "$SANDBOX/claude-env-judge-oauth-file" judge-oauth-file OAUTH_SET APIKEY_SET && ok judge-oauth-file
fi

# J. No credential → claude sees NEITHER var (host-login path; an exported-
# but-empty credential could shadow the login).
if run_judge_case judge-none; then
  if [[ -s "$SANDBOX/claude-env-judge-none" ]]; then
    echo "FAIL judge-none: claude saw a credential var that should be absent"; FAIL=$((FAIL+1))
  else
    ok judge-none
  fi
fi

# N. Retired arms (fable-lean, nullius, nullius-rev1, nullius-rev2 — their agent files were
# deleted, see the pointer comment in run.sh) must be REJECTED, not run.
for retired in fable-lean nullius nullius-rev1 nullius-rev2; do
  rc=0
  out="$(PATH="$SANDBOX/bin:$PATH" bash "$SANDBOX/harness/run.sh" "$TASK" "$retired" 2>&1)" || rc=$?
  if [[ "$rc" -eq 2 && "$out" == *"arm must be"* ]]; then
    ok "retired-arm-$retired"
  else
    echo "FAIL retired-arm-$retired: exit $rc (want 2), out: $(printf '%s' "$out" | tail -2 | tr '\n' ' ')"
    FAIL=$((FAIL+1))
  fi
done

echo "----"
echo "auth-wiring: $PASS passed, $FAIL failed"
[[ "$FAIL" -eq 0 ]]
