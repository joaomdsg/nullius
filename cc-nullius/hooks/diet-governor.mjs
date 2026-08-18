#!/usr/bin/env node
// nullius diet governor — PreToolUse hook.
//
// The main-thread context is the bill: every absorbed token is re-paid every
// following turn. Starves the ORCHESTRATOR; subagents pass untouched except
// craftsman (tests-first + boundary gates).
//
// Main thread: Grep/Glob/Web*/MCP-bulk denied (delegate). Read: no whole reads
// over NULLIUS_MAX_READ lines, no duplicate reads (path+range+mtime ledger).
// Edit/Write: NO size cap (a post-generation deny double-bills; delegation is
// a doctrine decision made before generating) — only the tests-first ratchet
// (every NULLIUS_EDITS_PER_TEST source edits require a test-file touch). Bash:
// heavy commands and unbounded wide searches denied, the rest tail-bounded.
//
// Craftsman: first Edit/Write must touch a test file (scaffolding exempt);
// edits dropping exported symbols denied (boundary decisions escalate up).
//
// Escapes: NULLIUS_OFF=1, .nullius-off in cwd, `#nullius:ok` in a command.
// QUICK mode (.nullius-quick in cwd, auto-expires after NULLIUS_QUICK_TTL_H
// hours, default 4): diet-lite for trivial day-to-day tasks — sweeps, reads,
// edits, MCP, and heavy Bash all pass; only the tail-bounding rewrite stays.
// Set/cleared by /nullius:quick. Craftsman gates stay active regardless.
// MCP: main-thread mcp__* calls with bulk-verb names are steered to scouts
// (subagents reach MCP tools via ToolSearch); NULLIUS_MCP_OK=1 disables.
// Telemetry: denies/rewrites/dispatches counted per session in
// $TMPDIR/nullius-stats-<session_id>; surfaced by /nullius:diet status.
import { readFileSync, writeFileSync, statSync, existsSync, appendFileSync, readdirSync, openSync, readSync, fstatSync, closeSync } from "node:fs";
import { join, basename } from "node:path";
import { tmpdir } from "node:os";

// writeFileSync(1) not console.log: process.exit right after an async pipe
// write can truncate the decision JSON.
const out = (obj) => { try { writeFileSync(1, JSON.stringify(obj) + "\n"); } catch {} process.exit(0); };
const allow = () => process.exit(0);

let data;
try { data = JSON.parse(readFileSync(0, "utf8")); } catch { allow(); }

// Session telemetry (best-effort): the economics this plugin exists for
// should be visible without the benchmark harness.
const statsFile = join(tmpdir(), `nullius-stats-${data.session_id || "nosession"}`);
const bump = (key) => { try {
  let s = {}; try { s = JSON.parse(readFileSync(statsFile, "utf8")); } catch {}
  s[key] = (s[key] || 0) + 1; writeFileSync(statsFile, JSON.stringify(s));
} catch {} };

const deny = (reason) => { bump("denies"); out({ hookSpecificOutput: {
  hookEventName: "PreToolUse", permissionDecision: "deny",
  permissionDecisionReason: reason } }); };
// No permissionDecision here: pairing updatedInput with "allow" would silently
// auto-approve the command, bypassing the user's permission prompt.
const rewrite = (updatedInput) => { bump("rewrites"); out({ hookSpecificOutput: {
  hookEventName: "PreToolUse", updatedInput } }); };

const cwd = data.cwd || ".";
if (process.env.NULLIUS_OFF === "1" || existsSync(join(cwd, ".nullius-off"))) allow();

// QUICK mode: .nullius-quick marker, auto-expired by mtime so a forgotten
// marker cannot silently disable the diet for tomorrow's defect hunt.
const QUICK_TTL_MS = parseFloat(process.env.NULLIUS_QUICK_TTL_H || "4") * 3600_000;
let quick = false;
try {
  const qs = statSync(join(cwd, ".nullius-quick"));
  quick = (Date.now() - qs.mtimeMs) < QUICK_TTL_MS;
} catch {}

const tool = data.tool_name || "";
const ti = data.tool_input || {};
const MAX_READ = parseInt(process.env.NULLIUS_MAX_READ || "250", 10);
// No Write/Edit size cap: measured 2026-07-19, a post-generation size-deny
// double-bills (the leader already spent the output tokens) and the real
// delegate-vs-write crossover (~1,800 lines cold / ~130 lean for an Opus
// leader) is set by variables the hook can't see. The delegate decision
// lives in the doctrine, before generation.
const EDITS_PER_TEST = parseInt(process.env.NULLIUS_EDITS_PER_TEST || "4", 10);
const TAIL_N = parseInt(process.env.NULLIUS_TAIL_LINES || "30", 10);

const isTestPath = (p) =>
  /(_test\.|\.test\.|\.spec\.|(^|\/)test_[^/]*$|(^|\/)tests?\/)/.test(p || "");
const isSourcePath = (p) =>
  /\.(go|ts|tsx|js|jsx|mjs|cjs|py|rs|java|c|cc|cpp|h|hpp|cs|rb|php|kt|swift|scala|ex|exs)$/.test(p || "");

// Newest mtime among test files under root — TOOL-AGNOSTIC test-touch detection.
// A *_test file written by a Bash heredoc/redirect advances this past the ratchet
// watermark and resets it, exactly as an Edit/Write to a test path does; the hook
// fires per tool call and cannot see a turn's whole batch, so freshness-by-mtime is
// the robust proxy for "a test was touched this turn". Bounded walk (skips VCS/dep/
// build dirs + all dot-dirs, caps entries) so a huge repo can't stall the hook.
// go-nullius's staleness Tracker, in miniature.
const SKIP_DIRS = new Set([
  "node_modules", "vendor", "dist", "build", "target",
  "__pycache__", "coverage", "bin", "obj"]);
function newestTestMtime(root) {
  let newest = 0, budget = 20000;
  const stack = [root];
  while (stack.length) {
    const dir = stack.pop();
    let ents;
    try { ents = readdirSync(dir, { withFileTypes: true }); } catch { continue; }
    for (const e of ents) {
      if (--budget <= 0) return newest;
      const full = join(dir, e.name);
      if (e.isDirectory()) {
        if (!SKIP_DIRS.has(e.name) && !e.name.startsWith(".")) stack.push(full);
      } else if (e.isFile() && isTestPath(full)) {
        try { const m = statSync(full).mtimeMs; if (m > newest) newest = m; } catch {}
      }
    }
  }
  return newest;
}

// Names declared exported in oldS and absent from newS — an edit that drops
// public surface. (Measured: a run scored zero after a "cleanup" deleted a
// public method only hidden consumers used.)
function droppedExports(oldS, newS) {
  const names = new Set();
  const RES = [
    /^func\s+(?:\([^)]*\)\s*)?([A-Z]\w*)/gm,
    /^(?:type|var|const)\s+([A-Z]\w*)/gm,
    /^export\s+(?:default\s+)?(?:async\s+)?(?:function|class|const|let|var|interface|type|enum)\s*(\w+)?/gm,
    /^\s*pub\s+(?:async\s+)?(?:fn|struct|enum|trait|const|static|type)\s+(\w+)/gm,
    /^(?:public|protected)\s+[\w<>,\s[\]]+?\s(\w+)\s*\(/gm,
  ];
  for (const re of RES) for (const m of (oldS || "").matchAll(re)) if (m[1]) names.add(m[1]);
  // `export { a, b as c }` re-export lists: the exported name is the alias
  // after `as`, else the identifier itself.
  for (const m of (oldS || "").matchAll(/export\s*\{([^}]*)\}/g))
    for (const part of m[1].split(","))
      { const n = part.trim().split(/\s+as\s+/).pop().trim(); if (n) names.add(n); }
  // A symbol counts as dropped unless it still appears as a STANDALONE
  // identifier in newS — substring `includes` would wrongly clear a rename
  // (old `Foo` "found" inside new `FooBar`), letting a real boundary change
  // through. Lookarounds exclude identifier chars ($ and _ included).
  const esc = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return [...names].filter(
    (n) => !new RegExp(`(?<![\\w$])${esc(n)}(?![\\w$])`).test(newS || ""));
}

// ---- craftsman gates --------------------------------------------------------
// agent_id, not agent_type, discriminates subagent calls: agent_type can be
// set on the MAIN thread in `claude --agent` sessions (per CLI docs).
// endsWith, not ===: marketplace install namespaces the agent as
// "nullius:craftsman" while the harness copies it bare as
// "craftsman" — a `===` silently fell through to the subagent
// passthrough below and left the craftsman UNGOVERNED under plugin install
// (verified 2026-07-19: dumped payload agent_type "nullius:scout").
const craftsman = /(^|:)craftsman$/.test(data.agent_type || "");
// craftsman gates require agent_id: a craftsman WITHOUT one is not a dispatched
// subagent but a main-thread orchestrator (e.g. a `nullius-build` nested
// session), which correctly gets full main-thread governance below, not the
// dispatched-craftsman gates. agent_id/agent_type are set by the CLI, not the
// model, so this is not a spoofable bypass.
if (data.agent_id && craftsman) {
  if (tool === "Edit" || tool === "Write") {
    const path = ti.file_path || "";
    // Boundary gate: an Edit OR a whole-file Write that drops/renames an
    // exported symbol is the orchestrator's call, not the craftsman's. For a
    // Write the "old" is the on-disk file (a NEW file has no exports to drop).
    let oldS, newS;
    if (tool === "Edit") { oldS = ti.old_string; newS = ti.new_string; }
    else if (existsSync(path) && isSourcePath(path)) {
      try { oldS = readFileSync(path, "utf8"); } catch { oldS = undefined; }
      newS = ti.content || "";
    }
    if (oldS !== undefined) {
      const dropped = droppedExports(oldS, newS);
      if (dropped.length) deny(
        `nullius boundary gate: removing/renaming exported ${dropped.join(", ")} is the ` +
        "orchestrator's decision. Implement within the pinned mechanism, or report " +
        "needs-orchestrator-ruling naming the symbol.");
    }
    const marker = join(tmpdir(),
      `nullius-craft-${data.session_id || "nosession"}-${data.agent_id}`);
    if (isTestPath(path)) { try { appendFileSync(marker, "t\n"); } catch {} allow(); }
    if (!isSourcePath(path)) allow();
    if (!existsSync(marker)) deny(
      "nullius tests-first gate: touch a test file first — write/extend the failing " +
      "test for the pinned defect, quote its RED verbatim, then edit source.");
  }
  allow();
}

// Other subagents are throwaway read/verify contexts — never starved.
if (data.agent_id) allow();

// ---- main thread ------------------------------------------------------------

// ---- ledger gate ------------------------------------------------------------
// Past the attention knee, the pre-compaction record is the only thing that
// survives. Measured 2026-08-17/18 (two auto-compaction drives): the
// ctx-sentinel nudged, the model kept serving the user's task, auto-compaction
// fired between turns and the whole record was lost — an advisory injection
// does not get the ledger written. So this is a DENIAL, in the same
// deny-then-comply idiom as the rest of the diet: past the knee with no fresh
// .nullius/ledger.md, calls that FILL context are refused. Write/Edit/Bash stay
// open precisely so the ledger can be written without lifting the gate, and
// `#nullius:ok`/NULLIUS_OFF/.nullius-off still escape.
const CTX_KNEE = Number(process.env.NULLIUS_CTX_KNEE) || 128_000;
// 30min, not the sentinel's 10: this gate exists to guarantee a ledger EXISTS
// before compaction, not that it is minutes-old. Measured 2026-08-18 dogfooding
// this very session — a 10-minute window re-gated the leader twice inside one
// task. The sentinel keeps 10min so it still nudges for a refresh.
const LEDGER_FRESH_MS = 30 * 60 * 1000;
const FILLS_CONTEXT = /^(Read|Grep|Glob|WebFetch|WebSearch|Agent|Task|mcp__)/;
if (FILLS_CONTEXT.test(tool)) {
  let ledgerAge = Infinity;
  try { ledgerAge = Date.now() - statSync(join(cwd, ".nullius", "ledger.md")).mtimeMs; } catch {}
  if (!(ledgerAge >= 0 && ledgerAge < LEDGER_FRESH_MS)) {
    const ctx = liveContext(data.transcript_path);
    if (ctx > CTX_KNEE) {
      bump("ledger_gate");
      deny(
        `nullius: context \u2248${Math.round(ctx / 1000)}k tokens is past the attention knee ` +
        `(${Math.round(CTX_KNEE / 1000)}k) and .nullius/ledger.md is missing or stale. ` +
        `Auto-compaction fires between turns without asking, and everything not in the ledger ` +
        `is lost. Invoke Skill(skill: "nullius:compact") NOW \u2014 one turn \u2014 then this call ` +
        `passes unchanged. Write/Edit/Bash are open so you can write it.`,
      );
    }
  }
}

// Live context estimate from the transcript tail's last usage record. Returns 0
// when unreadable, so an unmeasurable context never gates work (fail-open).
function liveContext(path) {
  if (!path || !existsSync(path)) return 0;
  try {
    const fd = openSync(path, "r");
    const size = fstatSync(fd).size;
    const start = Math.max(0, size - 256 * 1024);
    const buf = Buffer.alloc(size - start);
    readSync(fd, buf, 0, buf.length, start);
    closeSync(fd);
    const lines = buf.toString("utf8").split("\n");
    for (let i = lines.length - 1; i >= 0; i--) {
      if (!lines[i].includes('"cache_read_input_tokens"')) continue;
      try {
        const u = JSON.parse(lines[i])?.message?.usage;
        if (!u) continue;
        return (u.input_tokens || 0) + (u.cache_read_input_tokens || 0) + (u.cache_creation_input_tokens || 0);
      } catch {}
    }
  } catch {}
  return 0;
}
// Dispatch telemetry ("Agent" and "Task" are the same tool across CLI versions).
if (tool === "Agent" || tool === "Task") {
  bump("dispatches");
  if (ti.subagent_type) bump(`dispatch:${ti.subagent_type}`);
  allow();
}

// QUICK mode: diet-lite. Everything passes except Bash, which keeps only the
// harmless tail-bounding below (context stays lean even on trivial tasks).
if (quick) { bump("quick_passes"); if (tool !== "Bash") allow(); }

// MCP responses are ungoverned bulk — one browser-HTML or document fetch can
// out-bloat everything the rest of this file prevents. Bulk-verb calls are
// steered to scouts (subagents reach the session's MCP tools via ToolSearch).
const isMcpBulk = /^mcp__/.test(tool) &&
  /(read|fetch|search|list|query|download|export|get_|history|logs|messages|threads|files|content|html|eval)/i.test(tool);
if (isMcpBulk && process.env.NULLIUS_MCP_OK === "1") bump("escape:mcp_ok");
if (isMcpBulk && process.env.NULLIUS_MCP_OK !== "1") deny(
  "nullius: MCP bulk lands unbounded in your context. Dispatch scout " +
  "with the question; it can reach the same MCP tools and returns a capped, " +
  "anchored report. (NULLIUS_MCP_OK=1 or /nullius:quick to disable this gate.)");

if (tool === "Grep" || tool === "Glob") deny(
  "nullius: sweeps are bulk. Dispatch scout or lens-hunter " +
  "(Agent tool), batched in parallel when independent.");

if (tool === "WebFetch" || tool === "WebSearch") deny(
  "nullius: dispatch scout with the URL/question; it returns a capped, " +
  "anchored report.");

// Tests-first ratchet state: { n: source edits since the last test touch,
// w: newest test-file mtime at that reset }. The watermark makes the reset
// tool-agnostic AND idempotent-on-retry: a test written by ANY tool advances
// newestTestMtime past w and resets n on the next source edit, so a denied
// batch's edits pass verbatim once the test lands (no re-shaping needed).
const ratchet = join(tmpdir(), `nullius-ratchet-${data.session_id || "nosession"}`);
const rLoad = () => { try {
  const o = JSON.parse(readFileSync(ratchet, "utf8"));
  return typeof o === "number" ? { n: o, w: 0 } : { n: o.n || 0, w: o.w || 0 };
} catch { return null; } };
const rSave = (s) => { try { writeFileSync(ratchet, JSON.stringify(s)); } catch {} };

if (tool === "Edit" || tool === "Write") {
  const path = ti.file_path || "";
  const newestTest = newestTestMtime(cwd);
  let st = rLoad();
  if (st === null) st = { n: 0, w: newestTest };
  if (newestTest > st.w) st = { n: 0, w: newestTest };

  if (isTestPath(path)) { rSave({ n: 0, w: st.w }); allow(); }
  if (tool === "Write" && !isSourcePath(path)) { rSave(st); allow(); } // scaffolding/config/docs

  if (isSourcePath(path) && st.n >= EDITS_PER_TEST) { rSave(st); deny(
    `nullius tests-first ratchet: ${st.n} source edits since the last test touch. ` +
    "Write/extend the test that pins the behavior you changed, THEN re-send these exact " +
    "edits unchanged — a test touch by ANY tool (an Edit/Write to a *_test/spec file, or a " +
    "Bash heredoc/redirect writing one) resets the ratchet, so the blocked edits pass on retry."); }

  // NO size cap on Write/Edit. A PreToolUse hook fires AFTER the model has
  // generated the tool call — the file content is already spent output tokens
  // by the time we could deny it, so a size-deny only forces the craftsman to
  // REGENERATE the same bytes: pure double-billing (measured 2026-07-19). And
  // the delegate-vs-write crossover is set by the leader/craftsman output-rate
  // gap and the craftsman's absorption tax A — both invisible here. With an
  // Opus leader (out ~$26/M vs sonnet $15/M, gap ~$11/M; A_cold ~$0.41) the
  // crossover is ~1,800 lines cold / ~130 lean, so a hook cap would be wrong
  // by 10-100x anyway. The delegate decision lives in the doctrine, made
  // BEFORE generation. The boundary gate (dropped exports) stays — it catches
  // a regression, which is worth its one wasted generation.
  if (isSourcePath(path)) { rSave({ n: st.n + 1, w: st.w }); allow(); }
  rSave(st);
  allow();
}

if (tool === "Read") {
  const path = ti.file_path || "";
  let mtime = null, lines = null;
  const BIN_RE = /\.(png|jpe?g|gif|webp|bmp|ico|pdf|zip|gz|tgz|tar|7z|woff2?|ttf|otf|eot|mp[34]|wav|ogg|webm|avif|heic|bin|dat|db|sqlite3?|exe|dll|so|dylib|wasm|pyc|class|jar)$/i;
  try {
    const st = statSync(path);
    mtime = st.mtimeMs;
    if (st.isFile() && st.size < 10_000_000 && !BIN_RE.test(path)) {
      const buf = readFileSync(path);
      let nul = false;
      lines = 0;
      for (let i = 0; i < buf.length; i++) {
        if (buf[i] === 10) lines++;
        else if (buf[i] === 0) { nul = true; break; }
      }
      if (nul) lines = null; // binary: newline bytes are not lines
      else if (buf.length && buf[buf.length - 1] !== 10) lines++;
    }
  } catch { allow(); }

  const bounded = ti.pages != null || (ti.limit != null && ti.limit <= MAX_READ);
  if (!bounded && lines != null && lines > MAX_READ) deny(
    `nullius: whole read of ${basename(path)} (${lines} lines > ${MAX_READ}). Read the ` +
    "decisive region (offset+limit) or dispatch scout to distill it.");

  const ledger = join(tmpdir(), `nullius-ledger-${data.session_id || "nosession"}`);
  const key = `${path}|${ti.offset ?? ""}|${ti.limit ?? ""}|${ti.pages ?? ""}|${mtime}`;
  try {
    const seen = existsSync(ledger) ? readFileSync(ledger, "utf8").split("\n") : [];
    if (seen.includes(key)) deny(
      "nullius: you already read exactly this range and it is unchanged. Use what " +
      "your context holds; if compaction dropped it, re-read a narrower decisive " +
      "range with offset+limit (a different range is a new ledger key).");
    appendFileSync(ledger, key + "\n");
  } catch { /* ledger is best-effort */ }
  allow();
}

if (tool === "Bash") {
  const cmd = ti.command || "";
  if (cmd.includes("#nullius:ok")) { bump("escape:ok"); allow(); }

  const HEAVY_RE = /\b(go\s+(test|build|vet)|npm\s+(test|run|ci|install)|pnpm|yarn|pytest|vitest|jest|bun\s+(test|install|run)|deno\s+(test|task|check)|pip3?\s+install|uv\s+(sync|run|pip)|cargo\s+(test|build|check|clippy)|make\b|tsc\b|eslint|ruff|mypy|mvn\b|gradle|dotnet\s+(test|build)|ctest)\b/;
  if (!quick && HEAVY_RE.test(cmd)) deny(
    "nullius: builds/tests flood the orchestrator. Dispatch scout " +
    "(quick check or close-out record). #nullius:ok only if it truly must run here.");

  // grep -r must match in ANY short-flag cluster (`-rn`, `-rln`, `-n -r`) and
  // with args before the flag (`grep --include=*.go -r`). `[^|;&]*` keeps the
  // scan inside the one grep invocation so a later piped command can't leak.
  const WIDE_RE = /\brg\b|\bag\b|find\s+[./]|grep\b[^|;&]*\s-[a-zA-Z]*[rR][a-zA-Z]*\b|grep\b[^|;&]*\s--recursive\b/;
  const BOUND_RE = /\|\s*(tail|head)\b|\bwc\b|-l\b|--count|--files-with-matches|-m\s*\d/;
  if (!quick && WIDE_RE.test(cmd) && !BOUND_RE.test(cmd)) deny(
    "nullius: unbounded wide search. Delegate to scout or bound it " +
    "(| head -n 20 / -l / --count).");

  // Trailing `;` would make `{ x; ; }` a syntax error; a `#` comment would
  // swallow the closing `; }`; `exit "${PIPESTATUS[0]}"` keeps the command's
  // real exit code (tail alone reports success for failed commands).
  const trimmed = cmd.replace(/[;\s]+$/, "");
  if (trimmed && !trimmed.includes("\n") && !trimmed.includes("#") &&
      !BOUND_RE.test(trimmed) && trimmed.length < 500 &&
      !/<<|\bfor\b|\bwhile\b|\bif\b|\bcd\b|\bexport\b|&$/.test(trimmed)) {
    rewrite({ command:
      `{ ${trimmed} ; } 2>&1 | tail -n ${TAIL_N}; exit "\${PIPESTATUS[0]}"` });
  }
}

allow();
