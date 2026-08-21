// Tests for the batching half of the diet: the ctx-sentinel turn channel,
// the context-sensitive read floor, and the read-ledger reset on compaction.
// Run: node --test hooks/
//
// The turn channel keys off the assistant message id in the transcript tail.
// Transcripts without an id (the older fake transcripts in
// ctx-sentinel.test.mjs) get no turn accounting at all — that is deliberate
// back-compat, pinned by "no message id: turn channel stays silent" below.
import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { writeFileSync, readFileSync, mkdirSync, rmSync, mkdtempSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";

const SENTINEL = new URL("./ctx-sentinel.mjs", import.meta.url).pathname;
const GOVERNOR = new URL("./diet-governor.mjs", import.meta.url).pathname;
const REINJECT = new URL("./compact-reinject.mjs", import.meta.url).pathname;

let n = 0;
const sid = () => `turnch-test-${process.pid}-${++n}`;
const sandbox = () => mkdtempSync(join(tmpdir(), "turnch-"));

// A transcript whose last usage line implies `ctx` tokens and carries `id` as
// the assistant message id — the turn identity the sentinel counts on.
function transcript(dir, ctx, id) {
  const p = join(dir, `t-${id}.jsonl`);
  const usage = {
    input_tokens: 2,
    cache_creation_input_tokens: 100,
    cache_read_input_tokens: ctx - 102,
    output_tokens: 500,
  };
  writeFileSync(p, JSON.stringify({ type: "assistant", message: { id, usage } }) + "\n");
  return p;
}

function runHook(hook, payload, dir) {
  const res = spawnSync("node", [hook], {
    input: JSON.stringify(payload),
    encoding: "utf8",
    env: { ...process.env, NULLIUS_OFF: "", ...(dir ? { TMPDIR: dir } : {}) },
  });
  const out = res.stdout.trim() ? JSON.parse(res.stdout).hookSpecificOutput : null;
  return { status: res.status, out };
}

// One PostToolUse call inside turn `id`.
function tick(dir, session_id, id, { ctx = 40_000, tool = "Read" } = {}) {
  return runHook(SENTINEL, {
    cwd: dir, session_id, tool_name: tool,
    transcript_path: transcript(dir, ctx, id),
  }, dir);
}

// ---- turn identity ---------------------------------------------------------
// What the turn channel DOES with these turns — churn density, repetition,
// re-arming, telemetry — is pinned in turn-churn.test.mjs. The cumulative
// 25-turn target and the consecutive-solo-streak detector that used to live
// here are GONE (user ruling, 2026-08-21): a cap reads a long productive run
// and a short wasteful one identically. What stays here is the turn-identity
// rule every one of those signals is built on.

test("multiple tool calls in ONE turn count as one turn", () => {
  const dir = sandbox(), s = sid();
  // 14 turns' worth of calls, but all inside three message ids.
  for (const id of ["a", "b", "c"]) {
    for (let i = 0; i < 6; i++) {
      assert.equal(tick(dir, s, id).out, null, "a batched turn is still one turn");
    }
  }
  rmSync(dir, { recursive: true, force: true });
});

test("no message id: turn channel stays silent (back-compat with older transcripts)", () => {
  const dir = sandbox(), s = sid();
  const p = join(dir, "noid.jsonl");
  writeFileSync(p, JSON.stringify({
    type: "assistant",
    message: { usage: { input_tokens: 1, cache_read_input_tokens: 40_000, cache_creation_input_tokens: 1 } },
  }) + "\n");
  for (let i = 0; i < 20; i++) {
    const { out } = runHook(SENTINEL, { cwd: dir, session_id: s, tool_name: "Read", transcript_path: p }, dir);
    assert.equal(out, null, "without a turn identity there is nothing to count");
  }
  rmSync(dir, { recursive: true, force: true });
});

// ---- context-sensitive read floor ----------------------------------------

function govRead(dir, file, extra = {}) {
  return runHook(GOVERNOR, {
    cwd: dir, session_id: sid(), tool_name: "Read",
    tool_input: { file_path: file }, ...extra,
  }, dir);
}

test("read floor is 120 lines: a 150-line whole read is denied, 100 passes", () => {
  const dir = sandbox();
  const big = join(dir, "big.txt"), ok = join(dir, "ok.txt");
  writeFileSync(big, "line\n".repeat(150));
  writeFileSync(ok, "line\n".repeat(100));
  const denied = govRead(dir, big).out;
  assert.equal(denied?.permissionDecision, "deny");
  assert.match(denied.permissionDecisionReason, /whole read/, "names the mechanism");
  assert.match(denied.permissionDecisionReason, /120/, "names the effective cap");
  assert.equal(govRead(dir, ok).out, null, "under the floor stays on the main thread");
  rmSync(dir, { recursive: true, force: true });
});

test("past the attention knee the read floor halves to 60", () => {
  const dir = sandbox();
  // A fresh ledger, so the separate ledger gate does not answer first.
  mkdirSync(join(dir, ".nullius"), { recursive: true });
  writeFileSync(join(dir, ".nullius", "ledger.md"), "# nullius ledger\n");
  const f = join(dir, "mid.txt");
  writeFileSync(f, "line\n".repeat(80));
  const fresh = govRead(dir, f, { transcript_path: transcript(dir, 40_000, "cool") }).out;
  assert.equal(fresh, null, "80 lines is under the 120 floor while context is fresh");
  const tight = govRead(dir, f, { transcript_path: transcript(dir, 150_000, "hot") }).out;
  assert.equal(tight?.permissionDecision, "deny", "past the knee attention is scarcer than dollars");
  assert.match(tight.permissionDecisionReason, /60/, "names the halved cap");
  rmSync(dir, { recursive: true, force: true });
});

// ---- read-ledger reset on compaction -------------------------------------

test("compaction clears the read-dedup ledger (post-compact re-reads must not be denied)", () => {
  const dir = sandbox();
  const s = sid();
  mkdirSync(join(dir, ".nullius"), { recursive: true });
  writeFileSync(join(dir, ".nullius", "ledger.md"), "# nullius ledger\ncommit: deadbeef\n");
  const readLedger = join(dir, `nullius-ledger-${s}`);
  writeFileSync(readLedger, "/some/file.go|||\n");
  const { out } = runHook(REINJECT, { cwd: dir, source: "compact", session_id: s }, dir);
  assert.ok(out, "expected the ledger re-injection");
  assert.equal(readFileSync(readLedger, "utf8").trim(), "",
    "the ranges are gone from context, so the dedup record must go too");
  rmSync(dir, { recursive: true, force: true });
});

test("a non-compact SessionStart leaves the read-dedup ledger alone", () => {
  const dir = sandbox();
  const s = sid();
  const readLedger = join(dir, `nullius-ledger-${s}`);
  writeFileSync(readLedger, "/some/file.go|||\n");
  runHook(REINJECT, { cwd: dir, source: "startup", session_id: s }, dir);
  assert.match(readFileSync(readLedger, "utf8"), /file\.go/, "only compaction drops context");
  rmSync(dir, { recursive: true, force: true });
});

// ---- nudge provenance ------------------------------------------------------

