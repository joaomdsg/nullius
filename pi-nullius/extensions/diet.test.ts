// Run: node --test extensions/diet.test.ts   (node >= 23 strips the TS types natively)
import { test } from "node:test";
import assert from "node:assert/strict";
import { isHeavyCmd, isWideSearchCmd, isBoundedCmd, boundCommand } from "./diet.ts";

test("heavy commands are flagged", () => {
	assert.ok(isHeavyCmd("go test ./..."));
	assert.ok(isHeavyCmd("npm run build"));
	assert.ok(isHeavyCmd("cargo clippy"));
	assert.ok(!isHeavyCmd("git status"));
	assert.ok(!isHeavyCmd("ls -la"));
});

test("wide search: grep -r and bare rg are wide", () => {
	assert.ok(isWideSearchCmd("grep -rn foo ."));
	assert.ok(isWideSearchCmd("grep -Irn foo ."));
	assert.ok(isWideSearchCmd("rg foo"));
	assert.ok(isWideSearchCmd("rg -n 'pattern' src/"));
});

test("wide search: rg targeting a concrete file is NOT wide (any extension)", () => {
	assert.ok(!isWideSearchCmd("rg foo src/main.go"));
	// regression: extension list used to be hardcoded — README.md was blocked
	assert.ok(!isWideSearchCmd("rg foo README.md"));
	assert.ok(!isWideSearchCmd("rg -n scout extensions/index.test.ts"));
	assert.ok(!isWideSearchCmd("rg foo config.yaml"));
});

test("wide search: pathological long input does not hang and stays classified", () => {
	const long = `rg foo ${"a".repeat(50_000)}`;
	const t0 = Date.now();
	isWideSearchCmd(long);
	assert.ok(Date.now() - t0 < 1000, "regex must not backtrack catastrophically");
});

test("bounded: final pipe segment must be the bounding filter", () => {
	assert.ok(isBoundedCmd("go test ./... 2>&1 | tail -n 20"));
	assert.ok(isBoundedCmd("rg foo | head -5"));
	assert.ok(isBoundedCmd("ls | wc -l"));
	assert.ok(isBoundedCmd("rg -c foo | grep -c x"));
	assert.ok(isBoundedCmd("sort file.txt | head"));
	// regression: a mid-pipeline head used to count as bounded
	assert.ok(!isBoundedCmd("cat big.log | head -100000 | gunzip -c"));
	assert.ok(!isBoundedCmd("rg foo | tail -n 5 | xargs cat"));
	assert.ok(!isBoundedCmd("cat big.log"));
});

test("boundCommand wraps single-line unbounded commands only", () => {
	assert.equal(boundCommand("git log --oneline"), "{ git log --oneline ; } 2>&1 | tail -n 30");
	assert.equal(boundCommand("ls | head"), null); // already bounded
	assert.equal(boundCommand("cat <<EOF\nx\nEOF"), null); // heredoc
	assert.equal(boundCommand("a\nb"), null); // multi-line
	assert.equal(boundCommand(`git log ${"x".repeat(600)}`), null); // too long
});
