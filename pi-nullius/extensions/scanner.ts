/**
 * Deterministic lens scanner: tree-sitter over the repo, no LLM.
 *
 * Produces findings in three buckets:
 *   absent    — mechanically decided (tautologies, nil scope args)
 *   ambiguous — a relied-on property with no locally visible mechanism;
 *               an LLM hunter must adjudicate
 * PRESENT targets (mechanism visible in the same body) are counted, not
 * surfaced. Exhaustive by construction over packed languages; files in
 * unpacked languages are returned for open fallback hunts.
 */

import { readdirSync, readFileSync, statSync } from "node:fs";
import { extname, join } from "node:path";
import {
	ACQUIRE_CALLS, CLEAR_CALLS, EXT_TO_LANG, FANOUT_CALLS, GUARD_CALLS,
	LANG_PACKS, NIL_ARGS, RELEASE_CALLS, SCOPE_HINTS, SEND_CALLS,
} from "./lenses";

export interface Finding {
	lens: string;
	file: string;
	fn: string;
	line: number;
	status: "absent" | "ambiguous";
	note: string;
	/** function body text, truncated — lets hunters adjudicate without re-reading */
	snippet?: string;
}

export interface ScanResult {
	findings: Finding[];
	presentCount: number;
	filesScanned: number;
	fnScanned: number;
	unpackedFiles: string[]; // files in languages without a pack
	errors: string[];
}

const SKIP_DIRS = new Set([".git", "node_modules", "vendor", "dist", "build", "target", ".pi", "__pycache__", "testdata"]);
const MAX_FILE_BYTES = 400_000;

function* walk(dir: string): Generator<string> {
	for (const e of readdirSync(dir, { withFileTypes: true })) {
		if (e.isDirectory()) {
			if (!SKIP_DIRS.has(e.name) && !e.name.startsWith(".")) yield* walk(join(dir, e.name));
		} else if (e.isFile()) {
			yield join(dir, e.name);
		}
	}
}

const lc = (s: string) => s.toLowerCase();
const nameMatches = (callee: string, list: string[]) => {
	const n = lc(callee);
	return list.some((g) => n.includes(g));
};

/** last identifier segment of a call's function expression */
function calleeName(callNode: any): string {
	const fn = callNode.childForFieldName?.("function") ?? callNode.namedChild(0);
	if (!fn) return "";
	const t = fn.text as string;
	const seg = t.split(/[.:]/).pop() ?? t;
	return seg.replace(/[^\w]/g, "");
}

function collect(node: any, types: Set<string>, out: any[]) {
	if (types.has(node.type)) out.push(node);
	for (let i = 0; i < node.namedChildCount; i++) collect(node.namedChild(i), types, out);
}

const CALL_TYPES = new Set(["call_expression", "call", "method_invocation", "macro_invocation"]);
const ASSIGN_TYPES = new Set([
	"assignment_statement", "assignment_expression", "assignment", "augmented_assignment",
	"inc_dec_statement", "update_expression", "compound_assignment_expr", "short_var_declaration",
]);
const BINARY_TYPES = new Set(["binary_expression", "binary_operator", "boolean_operator", "comparison_operator"]);

let tsInit: Promise<{ Parser: any; Language: any }> | null = null;
async function initTS(pkgDir: string) {
	if (!tsInit) {
		tsInit = (async () => {
			const TS = await import("web-tree-sitter");
			const Parser = (TS as any).Parser ?? (TS as any).default ?? TS;
			await Parser.init();
			const Language = (TS as any).Language ?? Parser.Language;
			return { Parser, Language };
		})();
	}
	return tsInit;
}

export async function scanRepo(root: string, pkgDir: string): Promise<ScanResult> {
	const res: ScanResult = { findings: [], presentCount: 0, filesScanned: 0, fnScanned: 0, unpackedFiles: [], errors: [] };
	const { Parser, Language } = await initTS(pkgDir);
	const langs: Record<string, { lang: any; parser: any; query: any }> = {};

	for (const file of walk(root)) {
		const ext = extname(file);
		const langName = EXT_TO_LANG[ext];
		if (!langName) continue;
		const rel = file.slice(root.length + 1);
		const pack = LANG_PACKS[langName];
		if (!pack) {
			res.unpackedFiles.push(rel);
			continue;
		}
		try {
			if (statSync(file).size > MAX_FILE_BYTES) continue;
			if (!langs[langName]) {
				const lang = await Language.load(join(pkgDir, "node_modules/tree-sitter-wasms/out", pack.wasm));
				const parser = new Parser();
				parser.setLanguage(lang);
				langs[langName] = { lang, parser, query: lang.query(pack.fnQuery) };
			}
			const { parser, query } = langs[langName];
			const src = readFileSync(file, "utf8");
			const tree = parser.parse(src);
			res.filesScanned++;

			// group captures into (name, body) pairs
			const caps = query.captures(tree.rootNode);
			const fns: { name: string; body: any }[] = [];
			let pendingName: string | null = null;
			for (const c of caps) {
				if (c.name === "name") pendingName = c.node.text;
				else if (c.name === "body") fns.push({ name: pendingName ?? "?", body: c.node });
			}

			for (const { name, body } of fns) {
				res.fnScanned++;
				const line = body.startPosition.row + 1;
				const snippet = body.text.split("\n").slice(0, 50).join("\n");
				const calls: any[] = [];
				const assigns: any[] = [];
				const binaries: any[] = [];
				collect(body, CALL_TYPES, calls);
				collect(body, ASSIGN_TYPES, assigns);
				collect(body, BINARY_TYPES, binaries);
				const callNames = calls.map(calleeName).filter(Boolean);
				const hasGuard = callNames.some((c) => nameMatches(c, GUARD_CALLS)) || /\batomic\b/i.test(body.text);
				const deferTexts: string[] = [];
				collect(body, new Set(pack.deferNodes), deferTexts as any);
				const deferText = (deferTexts as any[]).map((n) => n.text).join("\n");

				// serialization: shared-looking mutation with no guard in own body
				const sharedAssign = assigns.find((a: any) => {
					const lhs = (a.childForFieldName?.("left") ?? a.namedChild(0))?.text ?? a.text;
					return pack.sharedLhs.test(lhs.trim());
				});
				if (sharedAssign) {
					if (hasGuard) res.presentCount++;
					else
						res.findings.push({
							snippet,
							lens: "serialization", file: rel, fn: name, line: sharedAssign.startPosition.row + 1,
							status: "ambiguous",
							note: `mutates \`${sharedAssign.text.slice(0, 60).replace(/\n.*/s, "")}\` with no guard call in own body`,
						});
				}

				// wake predicates: tautologies — decidable
				for (const b of binaries) {
					const t = b.text.replace(/\s+/g, " ");
					if (/len\([^)]*\)\s*>=\s*0/.test(t) || /\|\|\s*true\b/.test(t) || /\btrue\s*\|\|/.test(t)) {
						res.findings.push({
							snippet,
							lens: "wake-predicates", file: rel, fn: name, line: b.startPosition.row + 1,
							status: "absent", note: `tautological predicate: \`${t.slice(0, 80)}\``,
						});
					}
				}

				// scope confinement at fan-out call sites
				for (const c of calls) {
					const cn = calleeName(c);
					if (!nameMatches(cn, FANOUT_CALLS)) continue;
					const argsNode = c.childForFieldName?.("arguments");
					const argText = argsNode?.text ?? "";
					const args = argText.slice(1, -1).split(",").map((s: string) => s.trim());
					if (args.some((a: string) => NIL_ARGS.includes(lc(a)))) {
						res.findings.push({
							snippet,
							lens: "scope-confinement", file: rel, fn: name, line: c.startPosition.row + 1,
							status: "absent", note: `nil scope argument at \`${cn}${argText.slice(0, 60)}\``,
						});
					} else if (!args.some((a: string) => nameMatches(a, SCOPE_HINTS))) {
						res.findings.push({
							snippet,
							lens: "scope-confinement", file: rel, fn: name, line: c.startPosition.row + 1,
							status: "ambiguous", note: `no scope-shaped argument at \`${cn}${argText.slice(0, 60)}\``,
						});
					} else res.presentCount++;
				}

				// fault survival: clear-ish call textually before send-ish call
				const clearCall = calls.find((c) => nameMatches(calleeName(c), CLEAR_CALLS));
				const sendCall = calls.find((c) => nameMatches(calleeName(c), SEND_CALLS));
				if (clearCall && sendCall && clearCall.startIndex < sendCall.startIndex) {
					res.findings.push({
						snippet,
						lens: "fault-survival", file: rel, fn: name, line: clearCall.startPosition.row + 1,
						status: "ambiguous",
						note: `\`${calleeName(clearCall)}\` precedes \`${calleeName(sendCall)}\` — does the cleared data survive a failed send?`,
					});
				}

				// resource release: acquire with no release in body or defer/finally
				const acquire = calls.find((c) => nameMatches(calleeName(c), ACQUIRE_CALLS));
				if (acquire) {
					const released =
						callNames.some((c) => nameMatches(c, RELEASE_CALLS)) || RELEASE_CALLS.some((r) => lc(deferText).includes(r));
					if (released) res.presentCount++;
					else
						res.findings.push({
							snippet,
							lens: "resource-release", file: rel, fn: name, line: acquire.startPosition.row + 1,
							status: "ambiguous", note: `\`${calleeName(acquire)}\` acquired, no release visible in body/defer`,
						});
				}

				// swallowed errors
				if (pack.swallowRe?.test(body.text)) {
					const m = body.text.match(pack.swallowRe);
					res.findings.push({
						snippet,
						lens: "swallowed-errors", file: rel, fn: name, line,
						status: "ambiguous", note: `error discarded: \`${(m?.[0] ?? "").trim().slice(0, 50)}\``,
					});
				}

				// lifecycle races: sweep/ttl/cleanup-named functions
				if (/sweep|reap|cleanup|evict|expire|gc\b|ttl/i.test(name)) {
					res.findings.push({
						snippet,
						lens: "lifecycle-races", file: rel, fn: name, line,
						status: "ambiguous", note: `cleanup path — verify liveness check + its synchronization`,
					});
				}
			}
		} catch (e: any) {
			res.errors.push(`${rel}: ${e?.message ?? e}`);
		}
	}
	return res;
}
