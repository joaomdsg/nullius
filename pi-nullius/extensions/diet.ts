/**
 * Diet-governor command classifiers — pure, testable (diet.test.ts).
 *
 * Regex inputs are capped (REGEX_SCAN_CAP) before testing: WIDE_SEARCH_RE
 * carries a negative lookahead whose worst case is quadratic in input
 * length, and a leader can legitimately paste very long commands.
 */

const REGEX_SCAN_CAP = 2000;

/** Heavy commands the leader must never run in its own context. */
const HEAVY_RE =
	/\b(go\s+(test|build|vet)|npm\s+(test|run|ci|install)|pnpm\s+(test|run|install)|yarn\s+(test|run)|pytest|cargo\s+(test|build|check|clippy)|make(\s|$)|mvn|gradle|tsc(\s|$)|eslint|ruff|golangci-lint|ctest|dotnet\s+(test|build))/;

/**
 * Repo-wide searches that belong in a scout: grep -r, or rg with no
 * concrete file argument. Any word ending in .<ext> counts as a file
 * target — the old hardcoded extension list blocked `rg foo README.md`.
 */
const WIDE_SEARCH_RE = /\b(grep\s+-[a-zA-Z]*r|rg\s+(?!.*\s\S+\.\w{1,6}\b))/;

/**
 * A command is bounded only when its FINAL pipe segment is the bounding
 * filter — `... | head -100000 | gunzip -c` is not bounded by its head.
 */
const BOUNDED_RE = /\|\s*(tail|head|wc|grep\s+-c)\b[^|]*$/;

export function isHeavyCmd(cmd: string): boolean {
	return HEAVY_RE.test(cmd.slice(0, REGEX_SCAN_CAP));
}

export function isWideSearchCmd(cmd: string): boolean {
	return WIDE_SEARCH_RE.test(cmd.slice(0, REGEX_SCAN_CAP));
}

export function isBoundedCmd(cmd: string): boolean {
	return BOUNDED_RE.test(cmd.slice(0, REGEX_SCAN_CAP));
}

/**
 * Silently bound a single-line unbounded command; null = leave untouched
 * (already bounded, multi-line, heredoc, or too long to wrap safely).
 */
export function boundCommand(cmd: string): string | null {
	if (isBoundedCmd(cmd) || cmd.includes("<<") || cmd.includes("\n") || cmd.length >= 500) return null;
	return `{ ${cmd} ; } 2>&1 | tail -n 30`;
}
