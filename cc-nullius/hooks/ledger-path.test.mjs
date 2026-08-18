// Tests for ledger-path.mjs — finding .nullius/ledger.md from ANY cwd.
// Run: node --test ledger-path.test.mjs
//
// Caught live, 2026-08-18: this session's cwd was <repo>/cc-nullius/hooks, so
// compact-reinject looked for hooks/.nullius/ledger.md, announced "there is NO
// ledger" while a fresh one sat at the repo root — and the governor's ledger
// gate denied context-filling calls for the same reason (a stat on a path that
// cannot exist reads as age Infinity). One resolver, walking up, fixes both.
import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { findLedger } from "./ledger-path.mjs";

// A repo root (has .git and .nullius/ledger.md) with a nested subdirectory.
function repo() {
  const root = mkdtempSync(join(tmpdir(), "ledgerpath-"));
  mkdirSync(join(root, ".git"), { recursive: true });
  mkdirSync(join(root, ".nullius"), { recursive: true });
  writeFileSync(join(root, ".nullius", "ledger.md"), "# nullius ledger\n");
  const deep = join(root, "plugin", "hooks");
  mkdirSync(deep, { recursive: true });
  return { root, deep };
}

test("finds the ledger at the cwd itself", () => {
  const { root } = repo();
  assert.equal(findLedger(root), join(root, ".nullius", "ledger.md"));
  rmSync(root, { recursive: true, force: true });
});

test("finds the repo-root ledger from a nested cwd (the live failure)", () => {
  const { root, deep } = repo();
  assert.equal(findLedger(deep), join(root, ".nullius", "ledger.md"),
    "a session started in a subdirectory must still see the record");
  rmSync(root, { recursive: true, force: true });
});

test("no ledger anywhere: null, not a throw", () => {
  const bare = mkdtempSync(join(tmpdir(), "ledgerpath-bare-"));
  mkdirSync(join(bare, ".git"), { recursive: true });
  assert.equal(findLedger(bare), null);
  rmSync(bare, { recursive: true, force: true });
});

test("unknown cwd is unknowable, not empty: findLedger(null) is null", () => {
  assert.equal(findLedger(null), null);
  assert.equal(findLedger(undefined), null);
});

test("the walk stops at the repo boundary — an outer ledger is not this mandate's", () => {
  const outer = mkdtempSync(join(tmpdir(), "ledgerpath-outer-"));
  mkdirSync(join(outer, ".nullius"), { recursive: true });
  writeFileSync(join(outer, ".nullius", "ledger.md"), "# someone else's mandate\n");
  const inner = join(outer, "repo", "pkg");
  mkdirSync(inner, { recursive: true });
  mkdirSync(join(outer, "repo", ".git"), { recursive: true });
  assert.equal(findLedger(inner), null,
    "crossing out of the repo would import an unrelated session's record");
  rmSync(outer, { recursive: true, force: true });
});

// The readers walk up and take the FIRST ledger they find, so a ledger written
// into a subdirectory silently shadows the real one. Only prose governs the
// WRITE, so pin the prose: the commands must name the repo root explicitly.
test("the writing commands name the repo root, not the cwd", () => {
  const doc = (p) => readFileSync(new URL(p, import.meta.url).pathname, "utf8");
  for (const p of ["../commands/compact.md", "../commands/close.md"]) {
    assert.match(doc(p), /git rev-parse --show-toplevel/,
      `${p} must resolve .nullius/ to the repo root`);
  }
});
