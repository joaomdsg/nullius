// Run: node --test extensions/singleflight.test.ts
import { test } from "node:test";
import assert from "node:assert/strict";
import { singleFlight } from "./singleflight.ts";

test("concurrent callers share one in-flight run", async () => {
	let runs = 0;
	let release!: (v: string) => void;
	const fn = singleFlight(() => {
		runs++;
		return new Promise<string>((r) => (release = r));
	});
	const a = fn();
	const b = fn();
	release("done");
	assert.equal(await a, "done");
	assert.equal(await b, "done");
	assert.equal(runs, 1);
});

test("a settled run does not pin later calls (success and failure both clear)", async () => {
	let runs = 0;
	const fn = singleFlight(async () => {
		runs++;
		if (runs === 1) throw new Error("boom");
		return runs;
	});
	await assert.rejects(fn(), /boom/);
	assert.equal(await fn(), 2); // failure cleared the flight — retry runs
	assert.equal(await fn(), 3); // success cleared it too
});

test("joiners get the first caller's run, not their own args", async () => {
	const seen: number[] = [];
	let release!: () => void;
	const fn = singleFlight((n: number) => {
		seen.push(n);
		return new Promise<void>((r) => (release = r));
	});
	const a = fn(1);
	const b = fn(2); // joins flight 1; its own args never run
	release();
	await Promise.all([a, b]);
	assert.deepEqual(seen, [1]);
});
