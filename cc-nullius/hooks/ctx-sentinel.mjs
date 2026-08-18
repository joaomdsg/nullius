#!/usr/bin/env node
// ctx-sentinel — PostToolUse hook. The model cannot natively see either of the
// two bills it pays, so this makes both visible at the point they matter.
//
// CONTEXT: reads the session transcript's last usage record (input +
// cache_read + cache_creation = live context) and, past the attention knee
// (default 128k, override NULLIUS_CTX_KNEE), injects a one-shot nudge via
// additionalContext. Re-nudges once per 32k band beyond the knee.
//
// TURNS: cost ≈ turns × residency, and the context floor is enforced
// mechanically while the turn budget was, until now, pure prose. Turn identity
// comes from the assistant message id in the transcript tail: every tool call
// inside one message shares it, so a batched turn counts once and a turn spent
// on a single dispatch is visible as such. Two channels, one injection —
// separate counters, so neither can silence the other.
//
// Fail-open everywhere: a sentinel bug must never break a session. A transcript
// with no message id gets no turn accounting at all rather than a guess.
import { readFileSync, writeFileSync, existsSync, openSync, readSync, fstatSync, closeSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { ledgerAge } from "./ledger-path.mjs";

const KNEE = Number(process.env.NULLIUS_CTX_KNEE) || 128_000;
const BAND = 32_000; // re-nudge granularity beyond the knee
const TAIL = 256 * 1024; // transcript bytes scanned from the end
const LEDGER_FRESH_MS = 10 * 60 * 1000; // a ledger this new already answers the nudge
const TURN_TARGET = 25; // the doctrine's budget; recitation starts at 60% of it
const RECITE_FROM = 15;
const SOLO_STREAK = 2; // consecutive one-dispatch turns before it is a pattern

function main() {
  if (process.env.NULLIUS_OFF === "1") return;
  let data;
  try {
    data = JSON.parse(readFileSync(0, "utf8"));
  } catch {
    return;
  }
  if (data.agent_id || data.agent_type) return; // main thread only
  if (data.cwd && existsSync(join(data.cwd, ".nullius-off"))) return;

  const rec = lastUsage(data.transcript_path);
  const notes = [];
  turnNote(data, rec, notes);
  ctxNote(data, rec.ctx, notes);
  if (!notes.length) return;

  process.stdout.write(
    JSON.stringify({
      hookSpecificOutput: {
        hookEventName: "PostToolUse",
        additionalContext: notes.join("\n\n"),
      },
    }),
  );
}

// --- the turn channel -------------------------------------------------------
// Counts turns by assistant message id and judges the turn that just ENDED:
// within a turn there is no way to know whether more tool calls are coming, so
// the boundary is the only honest place to evaluate one.
function turnNote(data, rec, notes) {
  if (!rec.id) return; // no turn identity → count nothing
  const stats = statsPath(data.session_id);
  let s = {};
  try {
    s = JSON.parse(readFileSync(stats, "utf8"));
  } catch {}

  let solo = s["turn:solo"] || 0;
  if (s["turn:id"] !== rec.id) {
    const calls = s["turn:calls"] || 0;
    const dispatches = s["turn:dispatches"] || 0;
    // A turn whose ONLY action was one dispatch is a wasted context pass: the
    // independent work that could have ridden along was left for a later turn.
    solo = calls === 1 && dispatches === 1 ? solo + 1 : 0;
    s["turn:id"] = rec.id;
    s["turn:n"] = (s["turn:n"] || 0) + 1;
    s["turn:calls"] = 0;
    s["turn:dispatches"] = 0;
  }
  s["turn:calls"] = (s["turn:calls"] || 0) + 1;
  const tool = data.tool_name || "";
  if (tool === "Agent" || tool === "Task") {
    s["turn:dispatches"] = (s["turn:dispatches"] || 0) + 1;
  }

  if (solo >= SOLO_STREAK) {
    notes.push(
      `nullius: ${solo} consecutive turns spent exactly one dispatch each. Every turn ` +
      `re-pays a full pass over everything resident, so a turn carrying one small action ` +
      `is a wasted pass. Batch independent dispatches into ONE message — six in one turn ` +
      `beat two in three — and batch the edits and reads that do not depend on them.`,
    );
    s["turn:nudge"] = (s["turn:nudge"] || 0) + 1;
    solo = 0; // re-arm rather than nag every turn
  }
  s["turn:solo"] = solo;

  const n = s["turn:n"] || 0;
  const due = n >= RECITE_FROM && (n % 5 === 0 || n >= TURN_TARGET - 3);
  if (due && s["turn:recited"] !== n) {
    s["turn:recited"] = n;
    notes.push(
      `nullius: turn ${n}/${TURN_TARGET}. ` +
      (n >= TURN_TARGET
        ? `You are over the turn budget — close now, or state plainly why this mandate needs more.`
        : `Batch what remains, and restate the open checklist items when you next update the todos.`),
    );
  }

  try {
    writeFileSync(stats, JSON.stringify(s));
  } catch {}
}

// --- the context channel ----------------------------------------------------
function ctxNote(data, ctx, notes) {
  if (!ctx || ctx <= KNEE) return;

  // A ledger written in the last few minutes means the session already did
  // what this nudge asks. Nagging past that spends context to say nothing.
  if (freshLedger(data.cwd)) return;

  const band = Math.floor((ctx - KNEE) / BAND) + 1;
  const stats = statsPath(data.session_id);
  let s = {};
  try {
    s = JSON.parse(readFileSync(stats, "utf8"));
  } catch {}
  if ((s["ctx:band"] || 0) >= band) return; // already nudged this band
  s["ctx:band"] = band;
  s["ctx:nudge"] = (s["ctx:nudge"] || 0) + 1;
  try {
    writeFileSync(stats, JSON.stringify(s));
  } catch {}

  const k = Math.round(ctx / 1000);
  notes.push(
    `nullius ctx-sentinel: context ≈${k}k tokens — past the attention knee (${Math.round(KNEE / 1000)}k). ` +
    `Judgment quality degrades from here; do not start new open-ended hunts. Your FIRST action in this ` +
    `turn is Skill(skill: "nullius:compact") — invoke it YOURSELF, before continuing the task, and do ` +
    `not ask the user to type anything. It costs one turn and writes .nullius/ledger.md, which is ` +
    `re-injected verbatim after any compaction. Do not defer it to the end of the mandate: ` +
    `auto-compaction fires between turns without warning, and whatever is not in the ledger is lost.`,
  );
}

// Live context estimate and turn identity, from the last assistant usage record
// in the transcript tail. Returns {ctx: 0, id: null} when unreadable
// (fail-open).
function lastUsage(path) {
  const none = { ctx: 0, id: null };
  if (!path || !existsSync(path)) return none;
  let chunk;
  try {
    const fd = openSync(path, "r");
    const size = fstatSync(fd).size;
    const start = Math.max(0, size - TAIL);
    const buf = Buffer.alloc(size - start);
    readSync(fd, buf, 0, buf.length, start);
    closeSync(fd);
    chunk = buf.toString("utf8");
  } catch {
    return none;
  }
  const lines = chunk.split("\n");
  for (let i = lines.length - 1; i >= 0; i--) {
    if (!lines[i].includes('"cache_read_input_tokens"')) continue;
    try {
      const rec = JSON.parse(lines[i]);
      const u = rec?.message?.usage;
      if (!u) continue;
      return {
        ctx: (u.input_tokens || 0) + (u.cache_read_input_tokens || 0) + (u.cache_creation_input_tokens || 0),
        id: rec?.message?.id || rec?.requestId || null,
      };
    } catch {} // truncated first line of the tail window — keep looking
  }
  return none;
}

// True when .nullius/ledger.md was written within LEDGER_FRESH_MS. Fail-open:
// unreadable/absent counts as stale, so the nudge still fires.
function freshLedger(cwd) {
  // ledgerAge walks up to the repo root: a session started in a subdirectory
  // still sees the record, so the nudge does not nag over a fresh ledger.
  const age = ledgerAge(cwd);
  return age >= 0 && age < LEDGER_FRESH_MS;
}

function statsPath(sessionId) {
  return join(tmpdir(), `nullius-stats-${sessionId || "unknown"}`);
}

main();
