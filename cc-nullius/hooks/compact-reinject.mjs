#!/usr/bin/env node
// compact-reinject — SessionStart hook, matcher "compact". The one lever
// that survives compaction.
//
// Compaction cannot be triggered or shaped by a plugin (upstream: #37307
// and #58538, both closed "not planned"; PreCompact can only block, never
// rewrite the summary). What a plugin CAN do is speak first in the fresh
// context: SessionStart fires with source "compact" after a compaction,
// and its additionalContext lands before the model acts again.
//
// So nullius records state on the way out (`/nullius:compact` writes
// .nullius/ledger.md) and reads it back on the way in — here. This works
// for AUTO-compaction too, which a prose nudge to the user cannot.
//
// Fail-open everywhere: a re-inject bug must never break a session. A
// missing ledger is not an error — it is itself the finding, and gets a
// short "you have no record" warning instead of silence.
import { readFileSync, writeFileSync, existsSync, statSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { findLedger } from "./ledger-path.mjs";

const LEDGER = ".nullius/ledger.md";
const MAX_LINES = 200; // ledger is meant to be ~120; this is the backstop
const MAX_CHARS = 16_000;

function main() {
  if (process.env.NULLIUS_OFF === "1") return;
  let data;
  try {
    data = JSON.parse(readFileSync(0, "utf8"));
  } catch {
    return;
  }
  // Only after a compaction. Startup/resume/clear get the doctrine pointer
  // from session-start.mjs; re-injecting a ledger there would resurrect a
  // finished mandate into an unrelated session.
  const src = data.source || data.session_start_reason || "";
  if (src !== "compact") return;

  const cwd = data.cwd || process.cwd();
  if (existsSync(join(cwd, ".nullius-off"))) return;

  // The governor's read-dedup ledger records ranges the leader has already
  // absorbed, and denies re-reading them. Compaction just dropped those ranges
  // out of context, so every entry is now a lie that costs a turn to route
  // around. Clear it here — this is the one hook that knows a compaction
  // happened. Truncate rather than unlink: the governor appends to it and must
  // not care whether the file exists.
  //
  // This is fail-safe under an UNVERIFIED assumption. If the CLI preserves
  // session_id across a compaction, this clears the very file the governor
  // reads — the intended effect. If it does NOT, the governor starts reading a
  // fresh, empty ledger under the new id anyway, so re-reads pass regardless
  // and this truncation merely tidies an orphan. Either branch satisfies the
  // intent; neither can deny a legitimate re-read.
  try {
    writeFileSync(join(tmpdir(), `nullius-ledger-${data.session_id || "nosession"}`), "");
  } catch {} // best-effort, exactly like the ledger itself

  // findLedger walks up to the repo root. Caught live 2026-08-18: with the
  // session cwd one directory below the root, this hook announced "there is NO
  // ledger" over a fresh record and told the model its findings were gone.
  const path = findLedger(cwd);
  // The counter must follow the CONTENT decision, not the file's existence:
  // an unreadable or blank ledger is a lost record, however present the file.
  const ctx = path ? ledgerContext(path) : null;
  if (!ctx) {
    bump(data.session_id, "compact:noledger");
    process.stdout.write(
      JSON.stringify({
        hookSpecificOutput: { hookEventName: "SessionStart", additionalContext: missingContext() },
      }),
    );
    return;
  }

  bump(data.session_id, "compact:reinject");
  process.stdout.write(
    JSON.stringify({
      hookSpecificOutput: { hookEventName: "SessionStart", additionalContext: ctx },
    }),
  );
}

function ledgerContext(path) {
  let body, age;
  try {
    body = readFileSync(path, "utf8");
    age = Math.round((Date.now() - statSync(path).mtimeMs) / 60_000);
  } catch {
    return null;
  }
  if (!body.trim()) return null;

  let lines = body.split("\n");
  let truncated = "";
  if (lines.length > MAX_LINES) {
    lines = lines.slice(0, MAX_LINES);
    truncated = `\n[…truncated at ${MAX_LINES} lines — read ${LEDGER} for the rest]`;
  }
  let text = lines.join("\n");
  if (text.length > MAX_CHARS) {
    text = text.slice(0, MAX_CHARS);
    truncated = `\n[…truncated at ${MAX_CHARS} chars — read ${LEDGER} for the rest]`;
  }

  return (
    `nullius compact-reinject: this context was just compacted. The pre-compaction ` +
    `record below is from ${LEDGER} (written ${age} min ago) and is the AUTHORITY on ` +
    `what was established — the compaction summary is lossy, this is not. Read it before ` +
    `acting. Treat its FACTS as already-verified testimony (do not re-scout them); treat ` +
    `its UNRULED/NEXT lines as the live worklist; treat UNKNOWN/ASSUMED as still open. ` +
    `Verify the commit stamp against \`git rev-parse HEAD\` before trusting path:line ` +
    `anchors. If the ledger contradicts the compaction summary, the ledger wins. If its ` +
    `UNRULED or NEXT lines no longer match where the work actually is — a ledger can be read ` +
    `back across several compactions and go stale — re-run Skill(skill: "nullius:compact") to ` +
    `refresh it before acting on those lines.\n\n` +
    `--- ${LEDGER} ---\n${text}${truncated}\n--- end ledger ---`
  );
}

function missingContext() {
  return (
    `nullius compact-reinject: this context was just compacted and there is NO ` +
    `${LEDGER} — the pre-compaction state was never recorded, so anything not in the ` +
    `compaction summary is gone. Do not assume prior findings survived. Re-establish ` +
    `terrain with a scout dispatch before ruling on anything, and state plainly to the user ` +
    `that the pre-compact record was lost. Your FIRST action in this turn is ` +
    `Skill(skill: "nullius:compact") — write the ledger from what you hold NOW, before ` +
    `resuming the task. Deferring it is how the record was lost in the first place: ` +
    `auto-compaction fires between turns, without warning and without asking.`
  );
}

function bump(sessionId, key) {
  const file = join(tmpdir(), `nullius-stats-${sessionId || "unknown"}`);
  let s = {};
  try {
    s = JSON.parse(readFileSync(file, "utf8"));
  } catch {}
  s[key] = (s[key] || 0) + 1;
  try {
    writeFileSync(file, JSON.stringify(s));
  } catch {}
}

main();
