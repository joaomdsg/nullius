#!/usr/bin/env node
// ctx-sentinel — PostToolUse hook. The model cannot natively see its own
// context size; this makes it visible at the point it starts to matter.
//
// Reads the session transcript's last usage record (input + cache_read +
// cache_creation = live context), and past the attention knee (default
// 128k tokens, override NULLIUS_CTX_KNEE) injects a one-shot nudge via
// additionalContext: finish the mandate, close, then hand the user a
// /compact line that preserves the close ledger. Re-nudges once per 32k
// band beyond the knee. Fail-open everywhere: a sentinel bug must never
// break a session.
import { readFileSync, writeFileSync, existsSync, statSync, openSync, readSync, fstatSync, closeSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";

const KNEE = Number(process.env.NULLIUS_CTX_KNEE) || 128_000;
const BAND = 32_000; // re-nudge granularity beyond the knee
const TAIL = 256 * 1024; // transcript bytes scanned from the end
const LEDGER_FRESH_MS = 10 * 60 * 1000; // a ledger this new already answers the nudge

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
  const ctx = contextTokens(data.transcript_path);
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
  process.stdout.write(
    JSON.stringify({
      hookSpecificOutput: {
        hookEventName: "PostToolUse",
        additionalContext:
          `nullius ctx-sentinel: context ≈${k}k tokens — past the attention knee (${Math.round(KNEE / 1000)}k). ` +
          `Judgment quality degrades from here; do not start new open-ended hunts. Your FIRST action in this ` +
          `turn is Skill(skill: "nullius:compact") — invoke it YOURSELF, before continuing the task, and do ` +
          `not ask the user to type anything. It costs one turn and writes .nullius/ledger.md, which is ` +
          `re-injected verbatim after any compaction. Do not defer it to the end of the mandate: ` +
          `auto-compaction fires between turns without warning, and whatever is not in the ledger is lost.`,
      },
    }),
  );
}

// Live context estimate from the last assistant usage record in the
// transcript tail. Returns 0 when unreadable (fail-open).
function contextTokens(path) {
  if (!path || !existsSync(path)) return 0;
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
    return 0;
  }
  const lines = chunk.split("\n");
  for (let i = lines.length - 1; i >= 0; i--) {
    if (!lines[i].includes('"cache_read_input_tokens"')) continue;
    try {
      const u = JSON.parse(lines[i])?.message?.usage;
      if (!u) continue;
      return (u.input_tokens || 0) + (u.cache_read_input_tokens || 0) + (u.cache_creation_input_tokens || 0);
    } catch {} // truncated first line of the tail window — keep looking
  }
  return 0;
}

// True when .nullius/ledger.md was written within LEDGER_FRESH_MS. Fail-open:
// unreadable/absent counts as stale, so the nudge still fires.
function freshLedger(cwd) {
  if (!cwd) return false;
  try {
    const age = Date.now() - statSync(join(cwd, ".nullius", "ledger.md")).mtimeMs;
    return age >= 0 && age < LEDGER_FRESH_MS;
  } catch {
    return false;
  }
}

function statsPath(sessionId) {
  return join(tmpdir(), `nullius-stats-${sessionId || "unknown"}`);
}

main();
