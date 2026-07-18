/**
 * Behavior contract for the write-side gates. Run:
 *   node --test extensions/gates.test.ts
 * (Node >= 23 strips the TS types natively; no build step.)
 */
import { test } from "node:test";
import assert from "node:assert/strict";
import {
	isTestPath,
	isSourcePath,
	editLineCount,
	editText,
	evaluateEditGate,
	initialGateState,
	type GateConfig,
	type EditGateState,
} from "./gates.ts";

const CFG: GateConfig = { maxEditLines: 40, testsFirstThreshold: 3 };
const lines = (n: number) => Array.from({ length: n }, (_, i) => `l${i}`).join("\n");

test("isTestPath recognizes test/spec/__tests__ conventions", () => {
	// Fails if the test-file regex drops any convention.
	for (const p of [
		"src/a.test.ts",
		"src/a.spec.tsx",
		"pkg/foo_test.ts".replace("_test", ".test"),
		"src/__tests__/a.ts",
		"src/tests/a.ts",
		"test/a.ts",
	]) {
		assert.equal(isTestPath(p), true, p);
	}
	assert.equal(isTestPath("src/attest.ts"), false); // 'test' must be a segment/suffix
	assert.equal(isTestPath("src/a.ts"), false);
});

test("isSourcePath: code yes, docs/config/tests no", () => {
	// Fails if docs/config leak into the gated set or code drops out.
	assert.equal(isSourcePath("src/a.ts"), true);
	assert.equal(isSourcePath("src/a.go"), true);
	assert.equal(isSourcePath("README.md"), false);
	assert.equal(isSourcePath("package.json"), false);
	assert.equal(isSourcePath("src/a.test.ts"), false);
});

test("editLineCount counts newlines; empty write is zero", () => {
	assert.equal(editLineCount(""), 0);
	assert.equal(editLineCount("one"), 1);
	assert.equal(editLineCount("a\nb\nc"), 3);
});

test("editText: write uses content, edit sums edits[].newText", () => {
	// Fails if the edit-tool array shape (pi's real schema) is not summed.
	assert.equal(editText({ path: "a.ts", content: "a\nb" }), "a\nb");
	assert.equal(
		editText({ path: "a.ts", edits: [{ oldText: "x", newText: "a\nb" }, { oldText: "y", newText: "c" }] }),
		"a\nb\nc",
	);
	assert.equal(editText({ path: "a.ts" }), ""); // unknown shape → fail-open
	// End-to-end: an edit whose summed newText exceeds the ceiling is blocked.
	const inp = { path: "src/a.ts", edits: [{ oldText: "", newText: lines(41) }] };
	const r = evaluateEditGate({ path: inp.path, newText: editText(inp) }, initialGateState(), CFG);
	assert.equal(r.block, true);
});

test("size cap blocks an oversized source edit, waives on identical resend", () => {
	const big = { path: "src/a.ts", newText: lines(41) };
	const r1 = evaluateEditGate(big, initialGateState(), CFG);
	// Fails if the size ceiling is not enforced (e.g. maxEditLines ignored).
	assert.equal(r1.block, true);
	assert.match(r1.reason ?? "", /focused-fix ceiling/);

	// Identical resend = leader override → allowed.
	const r2 = evaluateEditGate(big, r1.state, CFG);
	assert.equal(r2.block, false); // fails if retry-waive is removed
	assert.equal(r2.state.lastBlock, null);
});

test("a 40-line edit is at the ceiling and passes", () => {
	// Fails if the comparison is `>=` instead of `>`.
	const r = evaluateEditGate({ path: "src/a.ts", newText: lines(40) }, initialGateState(), CFG);
	assert.equal(r.block, false);
});

test("docs/config edits never trip size cap nor the ratchet", () => {
	let st: EditGateState = initialGateState();
	for (let i = 0; i < 10; i++) {
		const r = evaluateEditGate({ path: "README.md", newText: lines(500) }, st, CFG);
		assert.equal(r.block, false); // fails if non-source is gated
		st = r.state;
	}
	assert.equal(st.sourceEditsSinceTest, 0); // fails if docs increment the ratchet
});

test("tests-first ratchet: 3 source edits pass, 4th is steered", () => {
	let st: EditGateState = initialGateState();
	for (let i = 0; i < 3; i++) {
		const r = evaluateEditGate({ path: `src/f${i}.ts`, newText: "x" }, st, CFG);
		assert.equal(r.block, false, `edit ${i + 1} should pass`);
		st = r.state;
	}
	assert.equal(st.sourceEditsSinceTest, 3);
	// 4th with no test touched → blocked. Fails if the ratchet is absent.
	const r4 = evaluateEditGate({ path: "src/f3.ts", newText: "x" }, st, CFG);
	assert.equal(r4.block, true);
	assert.match(r4.reason ?? "", /since the last test touch/);
});

test("touching a test file resets the ratchet", () => {
	let st: EditGateState = { sourceEditsSinceTest: 3, lastBlock: null };
	const rt = evaluateEditGate({ path: "src/a.test.ts", newText: "x" }, st, CFG);
	assert.equal(rt.block, false);
	assert.equal(rt.state.sourceEditsSinceTest, 0); // fails if test touch doesn't reset
	// Now a source edit passes again.
	const rs = evaluateEditGate({ path: "src/a.ts", newText: "x" }, rt.state, CFG);
	assert.equal(rs.block, false);
});

test("ratchet waive on resend resets the counter (declared interface/refactor)", () => {
	let st: EditGateState = { sourceEditsSinceTest: 3, lastBlock: null };
	const blocked = evaluateEditGate({ path: "src/a.ts", newText: "x" }, st, CFG);
	assert.equal(blocked.block, true);
	// Identical resend → allowed AND counter reset (unlike a size waive).
	const waived = evaluateEditGate({ path: "src/a.ts", newText: "x" }, blocked.state, CFG);
	assert.equal(waived.block, false);
	assert.equal(waived.state.sourceEditsSinceTest, 0); // fails if ratchet waive doesn't reset
});

test("size waive still counts as one source edit toward the ratchet", () => {
	// Distinguishes size-waive (increment) from ratchet-waive (reset).
	const big = { path: "src/a.ts", newText: lines(41) };
	const blocked = evaluateEditGate(big, initialGateState(), CFG);
	const waived = evaluateEditGate(big, blocked.state, CFG);
	assert.equal(waived.block, false);
	assert.equal(waived.state.sourceEditsSinceTest, 1); // fails if size waive resets like ratchet
});

test("a non-identical edit after a block is NOT auto-waived", () => {
	// The waive is keyed to the exact blocked edit, not 'the next edit'.
	const big = { path: "src/a.ts", newText: lines(41) };
	const blocked = evaluateEditGate(big, initialGateState(), CFG);
	const different = evaluateEditGate({ path: "src/a.ts", newText: lines(50) }, blocked.state, CFG);
	assert.equal(different.block, true); // fails if any next edit waives regardless of key
});
