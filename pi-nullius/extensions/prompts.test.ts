// Run: node --test extensions/prompts.test.ts
// Pins prompt prose to the implemented mechanics — prompts drift silently otherwise
// (measured: LEADER_RULES shipped naming 8 lenses while lenses.ts implemented 7).
import { test } from "node:test";
import assert from "node:assert/strict";
import { ADVISOR_PROMPT, LEADER_RULES } from "./prompts.ts";
import { LENSES } from "./lenses.ts";

// Each implemented lens must be recognizable in the leader's lens-library prose.
const PROSE_PHRASE: Record<string, string> = {
	serialization: "serialization",
	"scope-confinement": "scope confinement",
	"wake-predicates": "predicates",
	"fault-survival": "fault survival",
	"lost-updates": "lost updates",
	"resource-release": "resource release",
	"swallowed-errors": "swallowed errors",
	"lifecycle-races": "lifecycle",
	boundaries: "boundaries",
};

test("every implemented lens appears in the leader's lens library prose", () => {
	const prose = LEADER_RULES.toLowerCase();
	for (const lens of LENSES) {
		const phrase = PROSE_PHRASE[lens.id];
		assert.ok(phrase, `lens ${lens.id} has no prose phrase registered in this test — add one`);
		assert.ok(prose.includes(phrase), `lens ${lens.id} ("${phrase}") is missing from LEADER_RULES`);
	}
});

test("leader rules route judgment to the consult tool at the three gates", () => {
	for (const marker of ["consult", "stage=plan", "stage=surprise", "stage=close"]) {
		assert.ok(LEADER_RULES.includes(marker), `LEADER_RULES lost the consult discipline marker "${marker}"`);
	}
});

test("advisor prompt mandates the structured ruling format the leader parses on", () => {
	for (const field of ["RULING:", "PLAN:", "CONSULT-AGAIN-WHEN:", "DEMAND:", "ASSUMPTIONS:", "VERIFY"]) {
		assert.ok(ADVISOR_PROMPT.includes(field), `ADVISOR_PROMPT lost mandatory field ${field}`);
	}
	assert.ok(/no tools/i.test(ADVISOR_PROMPT), "advisor must be told it is tool-less");
});
