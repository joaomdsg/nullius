#!/usr/bin/env node
// ctx-sentinel — PostToolUse hook. The model cannot natively see either of the
// two bills it pays, so this makes both visible at the point they matter.
//
// CONTEXT: reads the session transcript's last usage record (input +
// cache_read + cache_creation = live context) and, past the attention knee
// (default 128k, override NULLIUS_CTX_KNEE), injects a one-shot nudge via
// additionalContext. Re-nudges once per 32k band beyond the knee.
//
// TURNS: cost ≈ turns × residency. What that buys, though, is not readable
// from a turn COUNT — measured on stats-rolling-pointed, plain's two 6-of-6
// reps burned 54 and 55 turns while its 4-of-6 rep burned 18, so a cumulative
// target flags the best runs and clears the worst. This channel therefore
// judges CHURN DENSITY over a sliding window of the last 8 ended turns, which
// is scale-free: how turns are spent, never how many are left. Two shapes
// count — LOW-YIELD turns (one call, no edit, nothing batched) and REPETITION
// (the same tool re-aimed at the same target inside the window).
//
// Turn identity comes from the assistant message id in the transcript tail:
// every tool call inside one message shares it, so a batched turn counts once
// and a turn spent on a single dispatch is visible as such. Two channels, one
// injection — separate counters, so neither can silence the other.
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
// The churn window. WINDOW=8 ended turns; LOW_FIRE=4 of them low-yield is half
// the window, which is exactly the ALTERNATING shape (solo, batch, solo, batch)
// the old consecutive-streak detector could never see — one good turn reset it.
// REPEAT_FIRE=3 sightings of one (tool, target) pair: a second read of a file is
// ordinary, a third inside eight turns is re-absorbing something already held.
const WINDOW = 8;
const LOW_FIRE = 4;
const REPEAT_FIRE = 3;
// Pure ABSORPTION: a lone Read, Grep or dispatch buys a full context pass for
// one lookup. A turn is low-yield only when its single call is one of these —
// anything else (an edit, a Bash command, a todo update) is credited as moving
// the work, because a hook cannot tell a mutating `python3 - <<EOF` from a
// read-only `grep`, and crediting it is the failure that costs least: a
// wrongly-credited turn is a missed nudge, a wrongly-flagged one is noise
// injected into the judgment tier. Repeated identical Bash commands are still
// caught by the repetition rule below.
const ABSORB = new Set(["Read", "Grep", "Glob", "WebFetch", "WebSearch", "Agent", "Task", "NotebookRead"]);

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

// --- the turn channel (churn density) ---------------------------------------
// Judges the turn that just ENDED: within a turn there is no way to know
// whether more tool calls are coming, so the boundary is the only honest place
// to evaluate one. Judged turns land in a sliding window; the window, not a
// running total, is what speaks.
function turnNote(data, rec, notes) {
  if (!rec.id) return; // no turn identity → count nothing
  const stats = statsPath(data.session_id);
  let s = {};
  try {
    s = JSON.parse(readFileSync(stats, "utf8"));
  } catch {}

  let win = Array.isArray(s["turn:win"]) ? s["turn:win"] : [];
  let hist = Array.isArray(s["turn:hist"]) ? s["turn:hist"] : [];

  if (s["turn:id"] !== rec.id) {
    if (s["turn:id"]) {
      const calls = s["turn:calls"] || 0;
      const low = calls === 1 && (s["turn:absorb"] || 0) === 1;
      win = [...win, low ? 1 : 0].slice(-WINDOW);
      hist = [...hist, s["turn:pairs"] || []].slice(-WINDOW);
      s["turn:churn:n"] = (s["turn:churn:n"] || 0) + 1;
      if (low) s["turn:churn:low"] = (s["turn:churn:low"] || 0) + 1;
    }
    s["turn:id"] = rec.id;
    s["turn:n"] = (s["turn:n"] || 0) + 1;
    s["turn:calls"] = 0;
    s["turn:absorb"] = 0;
    s["turn:pairs"] = [];
  }

  const tool = data.tool_name || "";
  s["turn:calls"] = (s["turn:calls"] || 0) + 1;
  if (ABSORB.has(tool)) s["turn:absorb"] = (s["turn:absorb"] || 0) + 1;
  // Repetition is a CROSS-turn signal, so a pair counts once per turn: three
  // dispatches batched into one message is the doctrine, not a repeat. Pairs
  // with no readable target are dropped — every Agent call would otherwise
  // collide on the same empty target and read as churn.
  const at = target(data.tool_input);
  const pair = `${tool}|${at}`;
  const pairs = s["turn:pairs"] || [];
  if (at && !pairs.includes(pair)) s["turn:pairs"] = [...pairs, pair].slice(-24);

  const lows = win.reduce((a, b) => a + b, 0);
  const repeat = topRepeat(hist);
  let fired = false;

  if (win.length >= LOW_FIRE && lows >= LOW_FIRE) {
    notes.push(
      `nullius: churn — ${lows} of the last ${win.length} turns carried one tool call, no edit, and ` +
      `nothing batched alongside. Every turn re-pays a full pass over everything resident, so a turn ` +
      `bought for one lookup is a wasted pass. Batch independent actions into ONE message — six ` +
      `dispatches in one turn beat two in three — and ride the reads and edits that do not depend on ` +
      `them along with it.`,
    );
    fired = true;
  }
  if (repeat) {
    notes.push(
      `nullius: churn — ${repeat.tool} hit \`${repeat.at}\` ${repeat.n} times in the last ` +
      `${hist.length} turns. You are re-reading what you already hold; absorbing it again costs the ` +
      `same as the first time and adds nothing. Rule from what you have, or send a scout for the ` +
      `narrow thing still missing.`,
    );
    fired = true;
  }
  if (fired) {
    s["turn:nudge"] = (s["turn:nudge"] || 0) + 1;
    // Which turns nudged, not just how many. A bare count cannot be audited
    // after the fact — the first false positive was only caught because it
    // happened to fire while a human was watching. Keep the last few.
    s["turn:nudge:at"] = [...(s["turn:nudge:at"] || []), s["turn:n"] || 0].slice(-8);
    win = []; // re-arm on a fresh window rather than nag every turn
    hist = [];
  }
  s["turn:win"] = win;
  s["turn:hist"] = hist;

  try {
    writeFileSync(stats, JSON.stringify(s));
  } catch {}
}

// The most-repeated (tool, target) pair in the window, once it crosses the bar.
function topRepeat(hist) {
  const seen = new Map();
  for (const turn of hist) for (const p of turn || []) seen.set(p, (seen.get(p) || 0) + 1);
  let best = null;
  for (const [p, n] of seen) {
    if (n < REPEAT_FIRE || (best && n <= best.n)) continue;
    const i = p.indexOf("|");
    best = { tool: p.slice(0, i) || "tool", at: p.slice(i + 1), n };
  }
  return best;
}

// What a call was aimed AT. Absent (a bare Bash, an inline prompt) → "", which
// still pairs with the tool name, so repetition of an unaimed tool is visible
// without pretending to know its target.
function target(input) {
  if (!input || typeof input !== "object") return "";
  const v = input.file_path || input.path || input.notebook_path || input.pattern || input.command || "";
  return typeof v === "string" ? v.slice(0, 120) : "";
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
