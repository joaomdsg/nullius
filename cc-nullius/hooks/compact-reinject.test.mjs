// Tests for compact-reinject.mjs — the SessionStart("compact") ledger
// re-injection. Behavioral: spawn the hook, feed a payload, assert stdout.
// Run: node --test compact-reinject.test.mjs
import { test } from "node:test";
import assert from "node:assert";
import { spawnSync } from "node:child_process";
import { writeFileSync, mkdirSync, rmSync, mkdtempSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";

const HOOK = new URL("./compact-reinject.mjs", import.meta.url).pathname;
let n = 0;
const sid = () => `compact-test-${process.pid}-${++n}`;

function sandbox() {
  return mkdtempSync(join(tmpdir(), "compact-reinject-"));
}

function ledger(dir, body) {
  mkdirSync(join(dir, ".nullius"), { recursive: true });
  writeFileSync(join(dir, ".nullius", "ledger.md"), body);
}

// dir doubles as cwd AND as TMPDIR, so the stats file lands inside the
// sandbox and each test reads its own counters.
function run(dir, payload, env = {}) {
  const res = spawnSync("node", [HOOK], {
    input: JSON.stringify({ cwd: dir, source: "compact", session_id: sid(), ...payload }),
    encoding: "utf8",
    env: { ...process.env, NULLIUS_OFF: "", TMPDIR: dir, ...env },
  });
  const out = res.stdout.trim() ? JSON.parse(res.stdout).hookSpecificOutput : null;
  return { status: res.status, out, stats: () => stats(dir) };
}

function stats(dir) {
  const hit = spawnSync("sh", ["-c", `cat ${dir}/nullius-stats-* 2>/dev/null`], { encoding: "utf8" });
  try {
    return JSON.parse(hit.stdout);
  } catch {
    return {};
  }
}

test("source=compact with a ledger: re-injects the body as SessionStart context", () => {
  const dir = sandbox();
  ledger(dir, "# nullius ledger\ncommit: deadbeef\n\n## RULED\nFIXED lost-update at store.go:88\n");
  const { status, out, stats } = run(dir);
  assert.equal(status, 0);
  assert.ok(out, "expected an injection");
  assert.equal(out.hookEventName, "SessionStart");
  assert.match(out.additionalContext, /FIXED lost-update at store\.go:88/, "carries the ledger body");
  assert.match(out.additionalContext, /ledger wins/, "states the precedence over the summary");
  assert.equal(stats()["compact:reinject"], 1);
  rmSync(dir, { recursive: true, force: true });
});

test("non-compact sources stay silent (a finished mandate must not leak into a new session)", () => {
  const dir = sandbox();
  ledger(dir, "# nullius ledger\nstale\n");
  for (const source of ["startup", "resume", "clear", "fork", ""]) {
    const { status, out } = run(dir, { source });
    assert.equal(status, 0);
    assert.equal(out, null, `source ${source || "(empty)"} must not re-inject`);
  }
  rmSync(dir, { recursive: true, force: true });
});

test("session_start_reason is honored as the source field alias", () => {
  const dir = sandbox();
  ledger(dir, "# nullius ledger\nRULED: nothing\n");
  const { out } = run(dir, { source: undefined, session_start_reason: "compact" });
  assert.ok(out, "expected an injection via the alias field");
  rmSync(dir, { recursive: true, force: true });
});

test("no ledger: warns that the record was LOST instead of staying silent", () => {
  const dir = sandbox();
  const { status, out, stats } = run(dir);
  assert.equal(status, 0);
  assert.ok(out, "a missing ledger is a finding, not silence");
  assert.match(out.additionalContext, /NO \.nullius\/ledger\.md/);
  assert.match(out.additionalContext, /Do not assume prior findings survived/);
  assert.match(out.additionalContext, /nullius:compact/, "tells the model how to stop the repeat");
  assert.equal(stats()["compact:noledger"], 1);
  assert.equal(stats()["compact:reinject"], undefined);
  rmSync(dir, { recursive: true, force: true });
});

test("empty/whitespace ledger is treated as missing, not as an empty record", () => {
  const dir = sandbox();
  ledger(dir, "   \n\n");
  const { out, stats } = run(dir);
  assert.match(out.additionalContext, /NO \.nullius\/ledger\.md/);
  assert.equal(stats()["compact:noledger"], 1);
  rmSync(dir, { recursive: true, force: true });
});

test("oversized ledger is truncated with a pointer to the file (context floor holds)", () => {
  const dir = sandbox();
  const body = Array.from({ length: 500 }, (_, i) => `line ${i}`).join("\n");
  ledger(dir, body);
  const { out } = run(dir);
  assert.match(out.additionalContext, /line 0\b/);
  assert.match(out.additionalContext, /truncated at 200 lines/);
  assert.ok(!out.additionalContext.includes("line 499"), "tail must be dropped, not injected");
  rmSync(dir, { recursive: true, force: true });
});

test("a single enormous line is truncated by chars, not left unbounded", () => {
  const dir = sandbox();
  ledger(dir, "x".repeat(40_000));
  const { out } = run(dir);
  assert.match(out.additionalContext, /truncated at 16000 chars/);
  assert.ok(out.additionalContext.length < 17_500, "injection stays bounded");
  rmSync(dir, { recursive: true, force: true });
});

test("off switches: NULLIUS_OFF=1 and .nullius-off both silence the hook", () => {
  const dir = sandbox();
  ledger(dir, "# nullius ledger\nRULED: something\n");
  assert.equal(run(dir, {}, { NULLIUS_OFF: "1" }).out, null);

  writeFileSync(join(dir, ".nullius-off"), "");
  const { status, out } = run(dir);
  assert.equal(status, 0);
  assert.equal(out, null);
  rmSync(dir, { recursive: true, force: true });
});

test("fail-open: malformed stdin exits 0 and injects nothing", () => {
  const res = spawnSync("node", [HOOK], { input: "not json", encoding: "utf8" });
  assert.equal(res.status, 0);
  assert.equal(res.stdout.trim(), "");
});

test("hooks.json routes the compact matcher to this hook, leaving the startup hook unmatched", () => {
  const cfg = JSON.parse(readFileSync(new URL("./hooks.json", import.meta.url).pathname, "utf8"));
  const entries = cfg.hooks.SessionStart;
  const reinject = entries.find((e) => e.matcher === "compact");
  assert.ok(reinject, "expected a SessionStart entry matching compact");
  assert.match(reinject.hooks[0].command, /compact-reinject\.mjs/);
  const always = entries.find((e) => !e.matcher);
  assert.match(always.hooks[0].command, /session-start\.mjs/, "doctrine pointer still fires on every source");
});

test("missing-ledger warning demands the ledger as the FIRST action, not a someday", () => {
  // Measured 2026-08-17 (auto-drive): a polite "run it before the next
  // compaction" produced zero compliance — the model kept doing the user's
  // task and the ceiling arrived first. The directive has to be immediate.
  const dir = sandbox();
  const { out } = run(dir);
  assert.match(out.additionalContext, /FIRST action/i, "names when, not just what");
  assert.match(out.additionalContext, /Skill\(skill: "nullius:compact"\)/, "names the exact call");
  rmSync(dir, { recursive: true, force: true });
});

test("re-injected ledger tells the model to REFRESH a ledger that no longer matches reality", () => {
  // Measured 2026-08-18 (gate drive, 3 compactions on one ledger): the session
  // reported "NEXT is stale — it still holds the turn-2 plan". A ledger read
  // back unchanged across several compactions decays into misinformation.
  const dir = sandbox();
  ledger(dir, "# nullius ledger\n## NEXT\n1. do the thing\n");
  const { out } = run(dir);
  assert.match(out.additionalContext, /no longer match|stale/i, "must name the staleness duty");
  assert.match(out.additionalContext, /nullius:compact/, "must name how to refresh it");
  rmSync(dir, { recursive: true, force: true });
});
