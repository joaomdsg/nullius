// Tests for ctx-sentinel.mjs — the PostToolUse context-size nudge.
// Run: node --test ctx-sentinel.test.mjs
import { test } from "node:test";
import assert from "node:assert";
import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { writeFileSync, rmSync, mkdtempSync, mkdirSync, utimesSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";

const HOOK = new URL("./ctx-sentinel.mjs", import.meta.url).pathname;
let n = 0;
const sid = () => `ctxsent-test-${process.pid}-${++n}`;

// Build a fake transcript whose last usage line implies `ctx` tokens.
function fakeTranscript(dir, ctx) {
  const p = join(dir, "transcript.jsonl");
  const usage = {
    input_tokens: 2,
    cache_creation_input_tokens: 100,
    cache_read_input_tokens: ctx - 102,
    output_tokens: 500,
  };
  const lines = [
    JSON.stringify({ type: "user", message: { content: "hi" } }),
    JSON.stringify({ type: "assistant", message: { usage: { input_tokens: 1, cache_read_input_tokens: 10, cache_creation_input_tokens: 1, output_tokens: 5 } } }),
    JSON.stringify({ type: "assistant", message: { usage } }),
  ];
  writeFileSync(p, lines.join("\n") + "\n");
  return p;
}

// ORACLE finding, 2026-08-18: this used to pass `cwd: BARE_CWD`, so every
// nudge-expecting test silently depended on the DEVELOPER's repo state — a fresh
// .nullius/ledger.md at the repo root legitimately suppresses the nudge
// (freshLedger, 10-minute window), so the suite failed 4/4 on any machine where
// someone had just run /nullius:compact. No correct implementation can nudge
// through a fresh ledger, so the test was wrong, not the hook. An empty sandbox
// cwd keeps ledger freshness a thing tests opt INTO.
const BARE_CWD = mkdtempSync(join(tmpdir(), "ctxsent-bare-"));

function run(payload) {
  const res = spawnSync("node", [HOOK], {
    input: JSON.stringify({ cwd: BARE_CWD, ...payload }),
    encoding: "utf8",
    env: { ...process.env, NULLIUS_OFF: "" },
  });
  const out = res.stdout.trim() ? JSON.parse(res.stdout).hookSpecificOutput : null;
  return { status: res.status, out };
}

test("below knee: silent allow", () => {
  const dir = mkdtempSync(join(tmpdir(), "ctxsent-"));
  const session_id = sid();
  const { status, out } = run({ session_id, transcript_path: fakeTranscript(dir, 60_000) });
  assert.equal(status, 0);
  assert.equal(out, null);
  rmSync(dir, { recursive: true, force: true });
});

test("above knee: nudges with PostToolUse additionalContext naming the size", () => {
  const dir = mkdtempSync(join(tmpdir(), "ctxsent-"));
  const session_id = sid();
  const { status, out } = run({ session_id, transcript_path: fakeTranscript(dir, 150_000) });
  assert.equal(status, 0);
  assert.ok(out, "expected a nudge");
  assert.equal(out.hookEventName, "PostToolUse");
  assert.match(out.additionalContext, /146k|147k|150k|14[0-9]k/, "names the ctx size in k");
  assert.match(out.additionalContext, /compact/i, "points at compaction");
  assert.ok(out.additionalContext.length < 700, "nudge stays lean");
  rmSync(dir, { recursive: true, force: true });
});

test("same band: nudges once, then silent", () => {
  const dir = mkdtempSync(join(tmpdir(), "ctxsent-"));
  const session_id = sid();
  const t = fakeTranscript(dir, 150_000);
  assert.ok(run({ session_id, transcript_path: t }).out, "first crossing nudges");
  assert.equal(run({ session_id, transcript_path: t }).out, null, "same band is silent");
  rmSync(dir, { recursive: true, force: true });
});

test("next band (+32k): nudges again", () => {
  const dir = mkdtempSync(join(tmpdir(), "ctxsent-"));
  const session_id = sid();
  assert.ok(run({ session_id, transcript_path: fakeTranscript(dir, 150_000) }).out);
  assert.equal(run({ session_id, transcript_path: fakeTranscript(dir, 155_000) }).out, null);
  assert.ok(run({ session_id, transcript_path: fakeTranscript(dir, 185_000) }).out, "crossing +32k band re-nudges");
  rmSync(dir, { recursive: true, force: true });
});

test("missing/garbled transcript: fail-open silent", () => {
  const dir = mkdtempSync(join(tmpdir(), "ctxsent-"));
  const session_id = sid();
  assert.equal(run({ session_id, transcript_path: join(dir, "nope.jsonl") }).out, null);
  const bad = join(dir, "bad.jsonl");
  writeFileSync(bad, "not json at all\n{broken\n");
  assert.equal(run({ session_id: sid(), transcript_path: bad }).out, null);
  rmSync(dir, { recursive: true, force: true });
});

test("subagent context (agent_id set): silent", () => {
  const dir = mkdtempSync(join(tmpdir(), "ctxsent-"));
  const { out } = run({ session_id: sid(), transcript_path: fakeTranscript(dir, 150_000), agent_id: "a123" });
  assert.equal(out, null);
  rmSync(dir, { recursive: true, force: true });
});

test("NULLIUS_OFF: silent even above knee", () => {
  const dir = mkdtempSync(join(tmpdir(), "ctxsent-"));
  const res = spawnSync("node", [HOOK], {
    input: JSON.stringify({ cwd: BARE_CWD, session_id: sid(), transcript_path: fakeTranscript(dir, 150_000) }),
    encoding: "utf8",
    env: { ...process.env, NULLIUS_OFF: "1" },
  });
  assert.equal(res.stdout.trim(), "");
  rmSync(dir, { recursive: true, force: true });
});

test("NULLIUS_CTX_KNEE override lowers the threshold", () => {
  const dir = mkdtempSync(join(tmpdir(), "ctxsent-"));
  const res = spawnSync("node", [HOOK], {
    input: JSON.stringify({ cwd: BARE_CWD, session_id: sid(), transcript_path: fakeTranscript(dir, 60_000) }),
    encoding: "utf8",
    env: { ...process.env, NULLIUS_OFF: "", NULLIUS_CTX_KNEE: "50000" },
  });
  const out = res.stdout.trim() ? JSON.parse(res.stdout).hookSpecificOutput : null;
  assert.ok(out, "60k > 50k knee should nudge");
  rmSync(dir, { recursive: true, force: true });
});

// --- ledger-aware nudging (0.2.2): the nudge asks the model to invoke
// nullius:compact itself; once a ledger exists it has nothing left to say.

test("nudge tells the model to invoke the skill ITSELF, not to ask the user", () => {
  const dir = mkdtempSync(join(tmpdir(), "ctxsent-"));
  const { out } = run({ session_id: sid(), transcript_path: fakeTranscript(dir, 150_000), cwd: dir });
  assert.ok(out, "expected a nudge");
  assert.match(out.additionalContext, /Skill\(skill: "nullius:compact"\)/, "names the self-invocation");
  assert.match(out.additionalContext, /do not ask the user to type anything/i);
  rmSync(dir, { recursive: true, force: true });
});

test("a FRESH .nullius/ledger.md suppresses the nudge (no nagging after the work is done)", () => {
  const dir = mkdtempSync(join(tmpdir(), "ctxsent-"));
  mkdirSync(join(dir, ".nullius"), { recursive: true });
  writeFileSync(join(dir, ".nullius", "ledger.md"), "# nullius ledger\nRULED: x\n");
  const { status, out } = run({ session_id: sid(), transcript_path: fakeTranscript(dir, 150_000), cwd: dir });
  assert.equal(status, 0);
  assert.equal(out, null, "a ledger written moments ago already answers the nudge");
  rmSync(dir, { recursive: true, force: true });
});

test("a STALE ledger does not suppress the nudge", () => {
  const dir = mkdtempSync(join(tmpdir(), "ctxsent-"));
  mkdirSync(join(dir, ".nullius"), { recursive: true });
  const p = join(dir, ".nullius", "ledger.md");
  writeFileSync(p, "# nullius ledger\nRULED: old\n");
  const old = Date.now() / 1000 - 3600; // an hour ago
  utimesSync(p, old, old);
  const { out } = run({ session_id: sid(), transcript_path: fakeTranscript(dir, 150_000), cwd: dir });
  assert.ok(out, "an hour-old ledger is not the current state");
  rmSync(dir, { recursive: true, force: true });
});

test("hooks.json fires the sentinel on context-FILLING tools, not just agent dispatches", () => {
  // Measured 2026-08-17 (auto-compaction drive): a session that read six large
  // files itself and dispatched nothing never got a single nudge — the matcher
  // was Agent|Task, so the sentinel was blind to exactly the sessions that
  // fill their own context.
  const cfg = JSON.parse(readFileSync(new URL("./hooks.json", import.meta.url).pathname, "utf8"));
  const entry = cfg.hooks.PostToolUse.find((e) => /ctx-sentinel/.test(e.hooks[0].command));
  assert.ok(entry, "expected a PostToolUse entry for ctx-sentinel");
  // The rest added 2026-08-18: the turn channel judges a turn by how many calls
  // it made, so any tool the matcher cannot see is invisible to that count.
  // Blind to Edit/Write it misread every "dispatch + edits" turn as wasteful
  // (caught live); blind to TodoWrite, every "dispatch + todo update" turn.
  for (const tool of ["Agent", "Task", "Read", "Bash", "Grep", "Glob", "Edit", "Write",
                      "TodoWrite", "Skill", "AskUserQuestion", "NotebookEdit"]) {
    assert.match(tool, new RegExp(`^(?:${entry.matcher})$`), `${tool} must reach the sentinel`);
  }
});
