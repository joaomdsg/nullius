// Run: node --test extensions/advisor.test.ts
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderPack, validateConsult, type ConsultInput } from "./advisor.ts";

const good: ConsultInput = {
	stage: "plan",
	goal: "port the widget cache to the new store",
	state: "N1 open, N2 open, N3 refuted",
	evidence: ["src/cache.go:41 — `c.mu.Lock()` absent in Evict()", "src/store.go:12 — `func (s *Store) Put(k, v string) error`"],
	question: "which checklist items are load-bearing for the port, and in what order?",
};

test("validateConsult: a well-formed pack passes", () => {
	assert.equal(validateConsult(good), null);
});

test("validateConsult: evidence without a path:line anchor is rejected with the offending item", () => {
	const bad = { ...good, evidence: ["the cache looks thread-unsafe to me"] };
	const err = validateConsult(bad);
	assert.ok(err && /path:line/.test(err), `expected anchor complaint, got: ${err}`);
	assert.ok(err!.includes("thread-unsafe"), "error must name the offending item");
});

test("validateConsult: NONE is a legal evidence claim, but only alone", () => {
	assert.equal(validateConsult({ ...good, evidence: ["NONE"] }), null);
	assert.ok(validateConsult({ ...good, evidence: [] }));
	assert.ok(validateConsult({ ...good, evidence: ["NONE", "src/a.go:1 — `x`"] }));
});

test("validateConsult: empty goal/question/state are rejected", () => {
	assert.ok(validateConsult({ ...good, goal: "  " }));
	assert.ok(validateConsult({ ...good, question: "" }));
	assert.ok(validateConsult({ ...good, state: "" }));
});

test("renderPack carries every field and the consult number", () => {
	const pack = renderPack(3, good);
	assert.ok(pack.includes("CONSULT 3"));
	assert.ok(pack.includes("stage: plan"));
	assert.ok(pack.includes(good.goal));
	assert.ok(pack.includes(good.state));
	assert.ok(pack.includes("src/cache.go:41"));
	assert.ok(pack.includes(good.question));
});

test("renderPack truncates oversized evidence items but keeps their anchors", () => {
	const long = `src/big.go:9 — ${"x".repeat(2000)}`;
	const pack = renderPack(1, { ...good, evidence: [long] });
	assert.ok(pack.includes("src/big.go:9"));
	assert.ok(pack.length < 2000);
});

test("renderPack elides beyond 30 evidence items and says how many were withheld", () => {
	const evidence = Array.from({ length: 33 }, (_, i) => `src/f.go:${i + 1} — \`x${i}\``);
	const pack = renderPack(1, { ...good, evidence });
	assert.ok(pack.includes("src/f.go:30"));
	assert.ok(!pack.includes("src/f.go:31 "));
	assert.ok(pack.includes("3 further items withheld"));
});

test("renderPack numbers consults so the advisor's session reads as a sequence", () => {
	assert.ok(renderPack(1, good).startsWith("## CONSULT 1"));
	assert.ok(renderPack(7, good).startsWith("## CONSULT 7"));
});
