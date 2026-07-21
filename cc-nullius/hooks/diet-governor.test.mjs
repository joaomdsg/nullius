// Behavioral tests for the diet governor. Run: node --test hooks/
// Each test pipes a synthetic PreToolUse payload into the hook and asserts
// on the decision JSON (or silent allow). Unique session ids keep the
// tmpdir ledger/ratchet state isolated per test.
import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdtempSync, writeFileSync, rmSync, readFileSync, utimesSync } from "node:fs";
import { join, dirname } from "node:path";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";

const HOOK = join(dirname(fileURLToPath(import.meta.url)), "diet-governor.mjs");
const CWD = mkdtempSync(join(tmpdir(), "nullius-test-cwd-"));
let n = 0;
const sid = () => `nullius-test-${process.pid}-${n++}`;

function run(payload) {
  const res = spawnSync("node", [HOOK], {
    input: JSON.stringify({ cwd: CWD, session_id: sid(), ...payload }),
    encoding: "utf8", env: { ...process.env, NULLIUS_OFF: "" },
  });
  const decision = res.stdout.trim() ? JSON.parse(res.stdout).hookSpecificOutput : null;
  return { status: res.status, decision };
}
const bash = (cmd) => spawnSync("bash", ["-c", cmd], { encoding: "utf8" });

// ---- Bash rewrite ----------------------------------------------------------

test("rewrite survives a trailing semicolon (was: `{ x; ; }` syntax error)", () => {
  const { decision } = run({ tool_name: "Bash", tool_input: { command: "echo hi;" } });
  assert.ok(decision.updatedInput, "expected a rewrite");
  const r = bash(decision.updatedInput.command);
  assert.equal(r.status, 0, r.stderr);
  assert.match(r.stdout, /hi/);
});

test("commands with # comments are not rewritten (comment ate the closing brace)", () => {
  const { decision } = run({ tool_name: "Bash", tool_input: { command: "echo hi # done" } });
  assert.equal(decision, null, "must pass through unrewritten");
});

test("rewrite preserves the command's exit code (was: tail masked failures)", () => {
  const { decision } = run({ tool_name: "Bash", tool_input: { command: "false" } });
  assert.ok(decision.updatedInput);
  assert.equal(bash(decision.updatedInput.command).status, 1);
  const ok = run({ tool_name: "Bash", tool_input: { command: "true" } });
  assert.equal(bash(ok.decision.updatedInput.command).status, 0);
});

test("rewrite does not auto-approve: no permissionDecision alongside updatedInput", () => {
  const { decision } = run({ tool_name: "Bash", tool_input: { command: "echo hi" } });
  assert.ok(decision.updatedInput);
  assert.equal(decision.permissionDecision, undefined,
    "permissionDecision:allow would bypass the user's permission prompt");
});

test("grep -R (uppercase) and --recursive are denied as unbounded wide searches", () => {
  for (const cmd of ["grep -R foo /etc", "grep --recursive foo /etc"]) {
    const { decision } = run({ tool_name: "Bash", tool_input: { command: cmd } });
    assert.equal(decision?.permissionDecision, "deny", cmd);
  }
});

test("grep -r in a combined flag cluster or later token is denied (was: -rn evaded)", () => {
  for (const cmd of ["grep -rn func .", "grep -rln TODO src", "grep -n -r x .",
                     "grep --include=*.go -r foo ."]) {
    const { decision } = run({ tool_name: "Bash", tool_input: { command: cmd } });
    assert.equal(decision?.permissionDecision, "deny", cmd);
  }
});

test("bounded recursive grep (piped to head) is allowed, not denied", () => {
  const { decision } = run({ tool_name: "Bash",
    tool_input: { command: "grep -rn func . | head -n 20" } });
  assert.notEqual(decision?.permissionDecision, "deny");
});

test("non-recursive grep to a file is not caught by the wide-search gate", () => {
  const { decision } = run({ tool_name: "Bash",
    tool_input: { command: "grep -n func main.go" } });
  // may be rewritten (tail-bound) but never denied as a wide search
  assert.notEqual(decision?.permissionDecision, "deny");
});

test("modern heavy runners are denied (vitest/jest/bun/deno/pip install)", () => {
  for (const cmd of ["vitest run", "jest --ci", "bun test", "deno test", "pip install requests"]) {
    const { decision } = run({ tool_name: "Bash", tool_input: { command: cmd } });
    assert.equal(decision?.permissionDecision, "deny", cmd);
  }
});

test("#nullius:ok escape still bypasses everything", () => {
  const { decision } = run({ tool_name: "Bash", tool_input: { command: "go test ./... #nullius:ok" } });
  assert.equal(decision, null);
});

// ---- Read gate ---------------------------------------------------------------

test("binary files are exempt from the whole-read line cap", () => {
  const p = join(CWD, "shot.png");
  const buf = Buffer.alloc(200_000);
  for (let i = 0; i < buf.length; i += 7) buf[i] = 10; // plenty of newline bytes
  buf[3] = 0;
  writeFileSync(p, buf);
  const { decision } = run({ tool_name: "Read", tool_input: { file_path: p } });
  assert.equal(decision, null, "a PNG is not a 28000-line file");
});

test("NUL-sniff exempts extensionless binaries too", () => {
  const p = join(CWD, "blob");
  const buf = Buffer.alloc(100_000, 10);
  buf[0] = 0;
  writeFileSync(p, buf);
  const { decision } = run({ tool_name: "Read", tool_input: { file_path: p } });
  assert.equal(decision, null);
});

test("whole read of a big text file is still denied", () => {
  const p = join(CWD, "big.txt");
  writeFileSync(p, "line\n".repeat(400));
  const { decision } = run({ tool_name: "Read", tool_input: { file_path: p } });
  assert.equal(decision?.permissionDecision, "deny");
});

test("duplicate read denied; deny reason names the narrower-range escape", () => {
  const p = join(CWD, "small.txt");
  writeFileSync(p, "a\n".repeat(10));
  const s = sid();
  const payload = { session_id: s, tool_name: "Read", tool_input: { file_path: p } };
  assert.equal(run(payload).decision, null);
  const second = run(payload).decision;
  assert.equal(second?.permissionDecision, "deny");
  assert.match(second.permissionDecisionReason, /offset|narrower|range/i,
    "after compaction the orchestrator needs a documented way out");
});

// ---- Edit/Write caps ---------------------------------------------------------

test("non-source Edit is exempt from the small-edit cap (docs edits stay on main thread)", () => {
  const { decision } = run({ tool_name: "Edit", tool_input: {
    file_path: join(CWD, "README.md"), old_string: "a\n".repeat(20), new_string: "b" } });
  assert.equal(decision, null);
});

test("NO size cap: a large source Edit passes (a post-generation deny double-bills)", () => {
  // Measured 2026-07-19: the crossover is ~1,800 lines cold / ~130 lean for an
  // Opus leader, and a size-deny fires AFTER the content is already generated,
  // so it only forces the craftsman to regenerate the same bytes. Delegation
  // is a doctrine decision, made before generating — not a hook size-cap.
  const big = run({ tool_name: "Edit", tool_input: {
    file_path: join(CWD, "main.go"), old_string: "a\n".repeat(300), new_string: "b" } });
  assert.equal(big.decision, null, "large edit must pass, not deny");
  const bigWrite = run({ tool_name: "Write", tool_input: {
    file_path: join(CWD, "impl.go"), content: "x\n".repeat(400) } });
  assert.equal(bigWrite.decision, null, "large write must pass, not deny");
});

test("tests-first ratchet: 4 source edits then deny until a test touch", () => {
  const s = sid();
  const edit = (f) => run({ session_id: s, tool_name: "Edit",
    tool_input: { file_path: join(CWD, f), old_string: "a", new_string: "b" } });
  for (let i = 0; i < 4; i++) assert.equal(edit("main.go").decision, null, `edit ${i}`);
  assert.equal(edit("main.go").decision?.permissionDecision, "deny");
  assert.equal(run({ session_id: s, tool_name: "Edit", tool_input: {
    file_path: join(CWD, "main_test.go"), old_string: "a", new_string: "b" } }).decision, null);
  assert.equal(edit("main.go").decision, null, "ratchet reset by test touch");
});

// ---- Subagents -----------------------------------------------------------------

test("non-craftsman subagents pass untouched", () => {
  const { decision } = run({ agent_type: "nullius-scout", agent_id: "a1",
    tool_name: "Grep", tool_input: { pattern: "x" } });
  assert.equal(decision, null);
});

test("craftsman: source edit denied before a test touch, allowed after; marker is session-scoped", () => {
  const s = sid();
  const src = { session_id: s, agent_type: "nullius-craftsman", agent_id: "craft1",
    tool_name: "Edit", tool_input: { file_path: join(CWD, "impl.go"), old_string: "a", new_string: "b" } };
  assert.equal(run(src).decision?.permissionDecision, "deny");
  assert.equal(run({ ...src, tool_input: { file_path: join(CWD, "impl_test.go"),
    old_string: "a", new_string: "b" } }).decision, null);
  assert.equal(run(src).decision, null, "test touched → source edit allowed");
  // a different craftsman in a different session must NOT inherit the marker
  assert.equal(run({ ...src, session_id: sid(), agent_id: "craft2" })
    .decision?.permissionDecision, "deny");
});

test("craftsman boundary gate: dropping a symbol from an export {...} re-export list is denied", () => {
  const { decision } = run({ agent_type: "nullius-craftsman", agent_id: "c10",
    tool_name: "Edit", tool_input: { file_path: join(CWD, "index.ts"),
      old_string: "export { foo, bar as baz }", new_string: "export { foo }" } });
  assert.equal(decision?.permissionDecision, "deny");
  assert.match(decision.permissionDecisionReason, /baz/);
});

test("craftsman boundary gate: dropping an exported symbol is denied", () => {
  const { decision } = run({ agent_type: "nullius-craftsman", agent_id: "c9",
    tool_name: "Edit", tool_input: { file_path: join(CWD, "api.go"),
      old_string: "func AppendToHead(x int) {}", new_string: "// gone" } });
  assert.equal(decision?.permissionDecision, "deny");
  assert.match(decision.permissionDecisionReason, /AppendToHead/);
});

test("craftsman boundary gate: RENAMING an exported symbol is denied (was: substring `includes` cleared it)", () => {
  // old `AppendToHead` renamed to `AppendToHeadV2`: substring matching found
  // "AppendToHead" inside "AppendToHeadV2" and wrongly cleared the drop.
  const { decision } = run({ agent_type: "nullius-craftsman", agent_id: "c11",
    tool_name: "Edit", tool_input: { file_path: join(CWD, "api.go"),
      old_string: "func AppendToHead(x int) {}",
      new_string: "func AppendToHeadV2(x int) {}" } });
  assert.equal(decision?.permissionDecision, "deny");
  assert.match(decision.permissionDecisionReason, /AppendToHead/);
});

test("craftsman boundary gate: a whole-file WRITE dropping an exported symbol is denied (was: Write skipped the gate)", () => {
  const f = join(CWD, "boundary_write.go");
  writeFileSync(f, "package p\nfunc ExportedThing() {}\n");
  const { decision } = run({ agent_type: "nullius-craftsman", agent_id: "c12",
    tool_name: "Write", tool_input: { file_path: f, content: "package p\n// gone\n" } });
  assert.equal(decision?.permissionDecision, "deny");
  assert.match(decision.permissionDecisionReason, /ExportedThing/);
});

test("craftsman gate binds under the PLUGIN-NAMESPACED agent_type (was: === missed the prefix)", () => {
  // marketplace install delivers agent_type "nullius:nullius-craftsman";
  // a `=== nullius-craftsman` check fell through to the subagent passthrough
  // and left the craftsman ungoverned. endsWith/regex must catch both forms.
  for (const at of ["nullius-craftsman", "nullius:nullius-craftsman"]) {
    const { decision } = run({ agent_type: at, agent_id: "c-" + at,
      tool_name: "Edit", tool_input: { file_path: join(CWD, "impl.go"),
        old_string: "a", new_string: "b" } });
    assert.equal(decision?.permissionDecision, "deny",
      `${at}: source-first edit must hit the tests-first gate`);
    assert.match(decision.permissionDecisionReason, /tests-first/);
  }
});

test("a NON-craftsman namespaced subagent still passes untouched", () => {
  const { decision } = run({ agent_type: "nullius:nullius-scout", agent_id: "s1",
    tool_name: "Edit", tool_input: { file_path: join(CWD, "x.go"),
      old_string: "a", new_string: "b" } });
  assert.equal(decision, null);
});

// ---- matcher wiring guard (the gap the unit tests structurally can't see) ----

test("hooks.json matcher routes MCP, Agent/Task, and the core tools to the hook", () => {
  const hj = JSON.parse(readFileSync(join(dirname(HOOK), "hooks.json"), "utf8"));
  const matcher = hj.hooks.PreToolUse[0].matcher;
  const re = new RegExp(`^(${matcher})$`);
  // MCP bulk + dispatch telemetry + gated core tools must all match, or the
  // in-script logic for them is dead code (measured: MCP gate never fired).
  for (const tool of ["mcp__drive__read_file_content", "Agent", "Task",
                      "Bash", "Read", "Edit", "Write", "Grep"]) {
    assert.ok(re.test(tool), `matcher must route ${tool} to the hook`);
  }
});

// ---- QUICK mode ---------------------------------------------------------------

test("quick mode: sweeps, whole reads, heavy bash pass; tail-bounding stays", () => {
  const qcwd = mkdtempSync(join(tmpdir(), "nullius-test-quick-"));
  writeFileSync(join(qcwd, ".nullius-quick"), "");
  const q = (p) => run({ cwd: qcwd, ...p });
  assert.equal(q({ tool_name: "Grep", tool_input: { pattern: "x" } }).decision, null);
  const big = join(qcwd, "big.go");
  writeFileSync(big, "x\n".repeat(400));
  assert.equal(q({ tool_name: "Read", tool_input: { file_path: big } }).decision, null);
  const heavy = q({ tool_name: "Bash", tool_input: { command: "go test ./..." } });
  assert.notEqual(heavy.decision?.permissionDecision, "deny", "heavy bash passes in quick");
  assert.ok(heavy.decision?.updatedInput, "tail-bounding rewrite still applies");
  rmSync(qcwd, { recursive: true, force: true });
});

test("quick marker expires by mtime (stale marker cannot unstarve tomorrow's hunt)", () => {
  const qcwd = mkdtempSync(join(tmpdir(), "nullius-test-quickexp-"));
  const marker = join(qcwd, ".nullius-quick");
  writeFileSync(marker, "");
  const old = (Date.now() - 5 * 3600_000) / 1000; // 5h > default 4h TTL
  utimesSync(marker, old, old);
  const { decision } = run({ cwd: qcwd, tool_name: "Grep", tool_input: { pattern: "x" } });
  assert.equal(decision?.permissionDecision, "deny", "expired marker → diet applies");
  rmSync(qcwd, { recursive: true, force: true });
});

// ---- MCP gate -------------------------------------------------------------------

test("main-thread MCP bulk verbs are denied; subagents and NULLIUS_MCP_OK pass", () => {
  const denied = run({ tool_name: "mcp__drive__read_file_content", tool_input: {} });
  assert.equal(denied.decision?.permissionDecision, "deny");
  assert.match(denied.decision.permissionDecisionReason, /scout/);
  const sub = run({ agent_id: "s1", tool_name: "mcp__drive__read_file_content", tool_input: {} });
  assert.equal(sub.decision, null, "subagents reach MCP untouched");
  const res = spawnSync("node", [HOOK], {
    input: JSON.stringify({ cwd: CWD, session_id: sid(),
      tool_name: "mcp__drive__read_file_content", tool_input: {} }),
    encoding: "utf8", env: { ...process.env, NULLIUS_OFF: "", NULLIUS_MCP_OK: "1" },
  });
  assert.equal(res.stdout.trim(), "", "NULLIUS_MCP_OK=1 disables the gate");
});

test("non-bulk MCP tools pass on the main thread", () => {
  const { decision } = run({ tool_name: "mcp__figma__authenticate", tool_input: {} });
  assert.equal(decision, null);
});

// ---- telemetry ------------------------------------------------------------------

test("stats: denies and dispatches are counted per session", () => {
  const s = sid();
  run({ session_id: s, tool_name: "Grep", tool_input: { pattern: "x" } });
  run({ session_id: s, tool_name: "Agent",
    tool_input: { subagent_type: "nullius-scout", prompt: "x" } });
  const stats = JSON.parse(readFileSync(join(tmpdir(), `nullius-stats-${s}`), "utf8"));
  assert.equal(stats.denies, 1);
  assert.equal(stats.dispatches, 1);
  assert.equal(stats["dispatch:nullius-scout"], 1);
});

// ---- SessionStart doctrine nudge ------------------------------------------------

test("SessionStart hook injects the doctrine pointer as additionalContext", () => {
  const START = join(dirname(HOOK), "session-start.mjs");
  const res = spawnSync("node", [START], {
    input: JSON.stringify({ cwd: CWD, session_id: sid(), source: "startup" }),
    encoding: "utf8", env: { ...process.env, NULLIUS_OFF: "" },
  });
  const o = JSON.parse(res.stdout).hookSpecificOutput;
  assert.equal(o.hookEventName, "SessionStart");
  assert.match(o.additionalContext, /invoke the `nullius:nullius` skill/);
  assert.match(o.additionalContext, /two-turn hunt/);
  // a pointer, not the full body — keep it cheap
  assert.ok(o.additionalContext.length < 700, "nudge must stay a pointer, not the doctrine");
});

test("SessionStart stays silent under the off switches", () => {
  const START = join(dirname(HOOK), "session-start.mjs");
  const off = spawnSync("node", [START], {
    input: JSON.stringify({ cwd: CWD, session_id: sid() }),
    encoding: "utf8", env: { ...process.env, NULLIUS_OFF: "1" },
  });
  assert.equal(off.stdout.trim(), "", "NULLIUS_OFF=1 → no nudge");
});

test("cleanup", () => { rmSync(CWD, { recursive: true, force: true }); });
