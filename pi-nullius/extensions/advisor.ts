/**
 * Advisor consult packs — pure, testable (advisor.test.ts).
 *
 * The advisor is a reasoning-only model (no tools, no reads/writes) that
 * supplies the judgment a fast leader cannot: it rules on structured
 * consult packs and returns multistep guidance with verify predicates.
 * The pack template is LOAD-BEARING: the fast leader fills slots
 * mechanically from its ledger; validateConsult rejects unanchored
 * evidence so summaries and opinions cannot masquerade as facts. The
 * advisor's memory is a persistent pi session (created with --session-id
 * on the first consult, resumed with --session after), so consult N sees
 * its own reasoning from consults 1..N-1.
 */

export type ConsultStage = "terrain" | "plan" | "surprise" | "close";

export interface ConsultInput {
	stage: ConsultStage;
	goal: string;
	state: string;
	evidence: string[];
	question: string;
}

const ANCHOR_RE = /\S+\.\w{1,6}:\d+/; // path.ext:line somewhere in the item
const MAX_EVIDENCE_ITEMS = 30;
const MAX_EVIDENCE_CHARS = 500;

/** Returns an error message for the leader, or null when the pack is sound. */
export function validateConsult(input: ConsultInput): string | null {
	if (!input.goal?.trim()) return "consult rejected: goal is empty — paste the mandate verbatim.";
	if (!input.state?.trim())
		return "consult rejected: state is empty — list every checklist/plan item with its status, or 'none yet'.";
	if (!input.question?.trim()) return "consult rejected: question is empty — name the one ruling you need.";
	if (!input.evidence?.length)
		return "consult rejected: evidence is empty — quote the new facts (path:line — `quote`), or pass exactly ['NONE'].";
	const isNone = input.evidence.length === 1 && input.evidence[0].trim() === "NONE";
	if (!isNone) {
		for (const item of input.evidence) {
			if (item.trim() === "NONE")
				return "consult rejected: NONE must be the ONLY evidence item — it claims there is nothing new.";
			if (!ANCHOR_RE.test(item))
				return `consult rejected: evidence item lacks a path:line anchor — quote the mechanism, don't summarize it: "${item.slice(0, 120)}"`;
		}
	}
	return null;
}

export function renderPack(n: number, input: ConsultInput): string {
	const evidence = input.evidence
		.slice(0, MAX_EVIDENCE_ITEMS)
		.map((e) => `- ${e.length > MAX_EVIDENCE_CHARS ? `${e.slice(0, MAX_EVIDENCE_CHARS)}…[truncated]` : e}`)
		.join("\n");
	const elided =
		input.evidence.length > MAX_EVIDENCE_ITEMS
			? `\n- …[${input.evidence.length - MAX_EVIDENCE_ITEMS} further items withheld — leader: re-consult with the load-bearing ones]`
			: "";
	return `## CONSULT ${n} — stage: ${input.stage}
GOAL: ${input.goal.trim()}
STATE:
${input.state.trim()}
NEW EVIDENCE:
${evidence}${elided}
QUESTION: ${input.question.trim()}`;
}
