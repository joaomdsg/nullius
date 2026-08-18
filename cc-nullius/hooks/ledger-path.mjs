// Where is .nullius/ledger.md?
//
// Every hook that consults the ledger used to answer this with
// join(data.cwd, ".nullius/ledger.md") — correct only when the session was
// started at the repo root. Started one directory down, the reinject announced
// "there is NO ledger" over a perfectly good record, and the governor's gate
// denied every context-filling call because a stat on an impossible path reads
// as age Infinity. Both were the same missing upward walk.
//
// Confinement: the walk stops at the directory holding .git. An outer ledger
// belongs to some other repo's mandate; importing it would be worse than
// finding none. No repo marker anywhere → the cwd is all we know.
import { existsSync, statSync } from "node:fs";
import { join, dirname } from "node:path";

const REL = join(".nullius", "ledger.md");
const MAX_UP = 40; // a filesystem depth no real checkout reaches; loop backstop

// Walk cwd upward, yielding each directory, and stop after the one with .git.
function* upward(cwd) {
  let dir = cwd;
  for (let i = 0; i < MAX_UP; i++) {
    yield dir;
    if (existsSync(join(dir, ".git"))) return;
    const up = dirname(dir);
    if (up === dir) return; // filesystem root
    dir = up;
  }
}

// Absolute path to the ledger, or null if this repo has none. Never throws:
// an unreadable ledger must read as absent, so callers can fail open.
export function findLedger(cwd) {
  if (!cwd) return null;
  try {
    for (const dir of upward(cwd)) {
      const p = join(dir, REL);
      if (existsSync(p)) return p;
    }
  } catch {}
  return null;
}

// Age in ms of the ledger, or Infinity when there is none to measure.
export function ledgerAge(cwd, now = Date.now()) {
  const p = findLedger(cwd);
  if (!p) return Infinity;
  try {
    return now - statSync(p).mtimeMs;
  } catch {
    return Infinity;
  }
}
