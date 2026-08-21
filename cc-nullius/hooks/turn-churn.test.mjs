// Tests for the CHURN WINDOW: the turn signal that replaced the cumulative
// 25-turn target. Run: node --test hooks/
//
// Why a window and not a cap: a cumulative cap reads a long PRODUCTIVE run and
// a short wasteful one identically. Measured on stats-rolling-pointed, plain's
// two 6-of-6 reps burned 54 and 55 turns while its 4-of-6 rep burned 18 — under
// a 25-turn target the best runs look like the failures. Churn density is
// scale-free: it judges how turns are spent, never how many are left.
//
// Why a window and not a streak: the old detector was
// `solo = calls===1 && dispatches===1 ? solo+1 : 0`, so ONE good turn reset it.
// Alternating churn — solo, batch, solo, batch — never fired, and that is the
// commonest real shape.
import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { writeFileSync, readFileSync, mkdtempSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";

const SENTINEL = new URL("./ctx-sentinel.mjs", import.meta.url).pathname;
let n = 0;
const sid = () => `churn-test-${process.pid}-${++n}`;
const sandbox = () => mkdtempSync(join(tmpdir(), "churn-"));

function transcript(dir, ctx, id) {
  const p = join(dir, `t-${id}.jsonl`);
  const usage = {
    input_tokens: 2, cache_creation_input_tokens: 100,
    cache_read_input_tokens: ctx - 102, output_tokens: 500,
  };
  writeFileSync(p, JSON.stringify({ type: "assistant", message: { id, usage } }) + "\n");
  return p;
}

function runHook(payload, dir) {
  const res = spawnSync("node", [SENTINEL], {
    input: JSON.stringify(payload), encoding: "utf8",
    env: { ...process.env, NULLIUS_OFF: "", TMPDIR: dir },
  });
  const out = res.stdout.trim() ? JSON.parse(res.stdout).hookSpecificOutput : null;
  return { status: res.status, out };
}

// One PostToolUse call inside turn `id`. `target` feeds repetition detection.
function tick(dir, session_id, id, { ctx = 40_000, tool = "Read", target } = {}) {
  return runHook({
    cwd: dir, session_id, tool_name: tool,
    tool_input: target ? { file_path: target } : {},
    transcript_path: transcript(dir, ctx, id),
  }, dir);
}

const stats = (dir, s) => JSON.parse(readFileSync(join(dir, `nullius-stats-${s}`), "utf8"));
const said = (r) => (r.out && r.out.additionalContext) || "";

// A low-yield turn: exactly one call, no edit, nothing batched.
const lowYield = (dir, s, id, target) => tick(dir, s, id, { tool: "Read", target });
// A productive turn: two calls, one of them an edit. BOTH notes are kept — the
// turn line lands on the FIRST call of a turn (the boundary is detected there),
// so returning only the last call's note silently discarded the very output
// these tests assert on.
function busy(dir, s, id, target) {
  const a = said(tick(dir, s, id, { tool: "Read", target }));
  const b = said(tick(dir, s, id, { tool: "Edit", target }));
  return { out: { additionalContext: a + b } };
}

// ---- the shape the streak detector missed -----------------------------------

test("ALTERNATING churn fires — one good turn no longer resets the signal", () => {
  const dir = sandbox(), s = sid();
  let spoke = "";
  // solo, busy, solo, busy, ... 5 low-yield turns inside a window of 8+
  for (let i = 1; i <= 10 && !spoke; i++) {
    const r = i % 2 ? lowYield(dir, s, `m${i}`, `/f${i}.go`) : busy(dir, s, `m${i}`, `/g${i}.go`);
    spoke = said(r);
  }
  assert.match(spoke, /churn/i, "alternating low-yield turns must be called out");
});

test("a window of productive turns stays silent, however many turns pass", () => {
  const dir = sandbox(), s = sid();
  const heard = [];
  for (let i = 1; i <= 40; i++) {
    const r = busy(dir, s, `m${i}`, `/f${i}.go`);
    if (said(r)) heard.push(`turn ${i}: ${said(r)}`);
  }
  assert.deepEqual(heard, [], "productive turns must never be nudged");
});

// BELOW the knee on purpose. Above it the sentinel emits its compaction demand
// instead of the turn line, so an above-knee version of this test would pass
// with the cap fully intact — measured, not assumed.
test("40 productive turns draw no cumulative-budget warning — the cap is gone", () => {
  const dir = sandbox(), s = sid();
  let all = "";
  for (let i = 1; i <= 40; i++) all += said(busy(dir, s, `m${i}`, `/f${i}.go`));
  assert.doesNotMatch(all, /\/25|over the turn budget|close now/i,
    "the cumulative 25-turn pressure must be gone");
});

// ---- classification --------------------------------------------------------

test("a single-call turn that EDITS is productive, not churn", () => {
  const dir = sandbox(), s = sid();
  let all = "";
  for (let i = 1; i <= 12; i++) all += said(tick(dir, s, `m${i}`, { tool: "Edit", target: `/f${i}.go` }));
  assert.doesNotMatch(all, /churn/i, "an edit moves the work even alone");
});

test("a batched turn is productive even with no edit", () => {
  const dir = sandbox(), s = sid();
  let all = "";
  for (let i = 1; i <= 12; i++) {
    all += said(tick(dir, s, `m${i}`, { tool: "Agent", target: `/a${i}` }));
    all += said(tick(dir, s, `m${i}`, { tool: "Agent", target: `/b${i}` }));
    all += said(tick(dir, s, `m${i}`, { tool: "Agent", target: `/c${i}` }));
  }
  assert.doesNotMatch(all, /churn/i, "three dispatches in one turn is the doctrine, not churn");
});

// ---- repetition ------------------------------------------------------------

test("RE-READING the same target inside the window is churn, even on busy turns", () => {
  const dir = sandbox(), s = sid();
  let spoke = "";
  for (let i = 1; i <= 8 && !spoke; i++) spoke = said(busy(dir, s, `m${i}`, "/same.go"));
  assert.match(spoke, /again|re-read|repeat/i, "re-absorbing a held file must be called out");
});

test("distinct targets on busy turns are not repetition", () => {
  const dir = sandbox(), s = sid();
  let all = "";
  for (let i = 1; i <= 12; i++) all += said(busy(dir, s, `m${i}`, `/f${i}.go`));
  assert.doesNotMatch(all, /again|re-read|repeat/i);
});

// ---- re-arming and telemetry ------------------------------------------------

test("firing clears the window — it does not nag on the next turn", () => {
  const dir = sandbox(), s = sid();
  let firedAt = 0;
  for (let i = 1; i <= 20 && !firedAt; i++) if (said(lowYield(dir, s, `m${i}`, `/f${i}.go`))) firedAt = i;
  assert.ok(firedAt, "must fire on sustained low yield");
  const next = said(lowYield(dir, s, `m${firedAt + 1}`, "/next.go"));
  assert.doesNotMatch(next, /churn/i, "must re-arm, not nag every turn");
});

test("the run records its churn ratio, so a bank can correlate it with fix rate", () => {
  const dir = sandbox(), s = sid();
  for (let i = 1; i <= 12; i++) lowYield(dir, s, `m${i}`, `/f${i}.go`);
  const st = stats(dir, s);
  assert.ok(typeof st["turn:churn:low"] === "number", "counts low-yield turns");
  assert.ok(typeof st["turn:churn:n"] === "number", "counts judged turns");
  assert.ok(st["turn:churn:low"] >= 5, `low-yield count too small: ${st["turn:churn:low"]}`);
  assert.ok(Array.isArray(st["turn:nudge:at"]), "keeps an auditable fired-at list");
});

test("no message id: the churn channel stays silent (back-compat)", () => {
  const dir = sandbox(), s = sid();
  const p = join(dir, "noid.jsonl");
  writeFileSync(p, JSON.stringify({ type: "assistant", message: { usage: { output_tokens: 1 } } }) + "\n");
  const r = runHook({ cwd: dir, session_id: s, tool_name: "Read", tool_input: {}, transcript_path: p }, dir);
  assert.doesNotMatch(said(r), /churn/i);
});
