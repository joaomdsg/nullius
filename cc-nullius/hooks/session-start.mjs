#!/usr/bin/env node
// nullius SessionStart hook — the doctrine nudge.
//
// The diet-governor hook is always-on (it enforces CONTEXT mechanically), but
// the nullius SKILL body loads only when invoked, so a session can be starved
// yet not run the method. This injects a ~70-token POINTER (not the ~5k body)
// so the orchestrator reliably reaches for the skill on real work. Respects
// the same off switches as the governor; stays silent under them.
import { readFileSync, writeFileSync, existsSync } from "node:fs";
import { join } from "node:path";

let data = {};
try { data = JSON.parse(readFileSync(0, "utf8")); } catch {}
const cwd = data.cwd || ".";
if (process.env.NULLIUS_OFF === "1" || existsSync(join(cwd, ".nullius-off"))) process.exit(0);

const ctx =
  "nullius is active in this session. The diet governor enforces the context " +
  "floor mechanically; YOU supply the method. For any nontrivial coding task, " +
  "invoke the `nullius:nullius` skill BEFORE acting — it governs the two-turn hunt " +
  "(terrain scouts, then a load-bearing gate ruling FULL vs BUILD), the " +
  "post-terrain gap check, the capped ruled checklist, and the scout close. " +
  "Trivial one-offs: `/nullius:quick`. Governor denials are the doctrine — " +
  "route around them, never fight them into a denial.";

try {
  writeFileSync(1, JSON.stringify({
    hookSpecificOutput: { hookEventName: "SessionStart", additionalContext: ctx },
  }) + "\n");
} catch {}
process.exit(0);
