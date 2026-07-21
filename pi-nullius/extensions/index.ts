/**
 * pi-nullius — the nullius harness as a pi extension.
 *
 * Turns any pi session into a nullius leader:
 *   - `scout` tool: throwaway read-only sub-sessions on a cheap model
 *     (pi -p --mode json subprocess), report hard-capped in code.
 *   - Context diet governor: blocks whole-file reads of big files,
 *     duplicate reads, and heavy build/test/search commands in the
 *     leader's own context — with a steering reason each time.
 *   - Write-side gates (gates.ts): steer oversized source edits and
 *     runs of source edits with no test touched; an identical resend of
 *     a blocked edit is the leader's override.
 *   - Nullius compactor: replaces pi's generic compaction summary with
 *     a STATUS/FACTS/RISKS/UNKNOWN/RULED-OUT evidence ledger written
 *     by the scout model.
 *   - Leader rules + lens library appended to the system prompt.
 *   - Live status: resident context, growth per turn, leader vs scout $.
 *
 * Config (env):
 *   NULLIUS_SCOUT_MODEL    provider/model-id   (default local-fast/qwen3.6)
 *   NULLIUS_SCOUT_TIMEOUT  seconds             (default 300)
 *   NULLIUS_DIET=0         disable the governor
 *   NULLIUS_MAX_READ       whole-read line ceiling (default 250)
 *   NULLIUS_MAX_EDIT       focused-fix edit-line ceiling (default 40)
 *   NULLIUS_TESTS_FIRST    source edits allowed per test touch (default 3)
 *   NULLIUS_SCOUT_PARALLEL hunt-pipeline scout concurrency (default 5)
 *   NULLIUS_AUTOHUNT=0     disable the auto-hunt on session start
 *   NULLIUS_ADVISOR_MODEL  provider/model-id for the reasoning advisor
 *                          (unset = consult tool not registered)
 *   NULLIUS_DEBUG=1        debug logging to stderr
 *   NULLIUS_OFF=1          disable the extension entirely
 */

import { spawn } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { isToolCallEventType } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";
import { ADVISOR_PROMPT, COMPACTOR_PROMPT, LEADER_RULES, SCOUT_PROMPT } from "./prompts";
import { renderPack, validateConsult } from "./advisor";
import { LENSES, lensById } from "./lenses";
import {
	evaluateEditGate,
	editText,
	initialGateState,
	DEFAULT_MAX_EDIT_LINES,
	DEFAULT_TESTS_FIRST_THRESHOLD,
	type EditGateState,
} from "./gates";
import { scanRepo, type Finding } from "./scanner";
import { isHeavyCmd, isWideSearchCmd, isBoundedCmd, boundCommand } from "./diet";
import { singleFlight } from "./singleflight";
import { dirname, join } from "node:path";

const SCOUT_MODEL = process.env.NULLIUS_SCOUT_MODEL ?? "local-fast/qwen3.6";
const SCOUT_TIMEOUT_S = Number(process.env.NULLIUS_SCOUT_TIMEOUT ?? 300);
// Reasoning-only judgment tier; unset = the consult tool is not registered.
const ADVISOR_MODEL = process.env.NULLIUS_ADVISOR_MODEL;
const ADVISOR_TIMEOUT_S = Number(process.env.NULLIUS_ADVISOR_TIMEOUT ?? 600);
const DIET_ON = process.env.NULLIUS_DIET !== "0";
const MAX_WHOLE_READ = Number(process.env.NULLIUS_MAX_READ ?? 250);
const MAX_EDIT_LINES = Number(process.env.NULLIUS_MAX_EDIT ?? DEFAULT_MAX_EDIT_LINES);
const TESTS_FIRST_THRESHOLD = Number(
	process.env.NULLIUS_TESTS_FIRST ?? DEFAULT_TESTS_FIRST_THRESHOLD,
);
const DEFAULT_CAP_LINES = 40;

interface ScoutRun {
	text: string;
	cost: number;
	tokens: number;
	turns: number;
}

function splitModel(spec: string): { provider: string; model: string } {
	const i = spec.indexOf("/");
	if (i < 0) return { provider: "anthropic", model: spec };
	return { provider: spec.slice(0, i), model: spec.slice(i + 1) };
}

/** Run a throwaway pi subprocess in json mode; harvest final text + usage. */
function runScout(
	dispatch: string,
	systemPrompt: string,
	modelSpec: string,
	cwd: string,
	signal: AbortSignal,
	timeoutS: number = SCOUT_TIMEOUT_S,
	// session/tool args: scouts are throwaway; the advisor overrides with a
	// persistent session and --no-tools.
	extraArgs: string[] = ["--no-session"],
): Promise<ScoutRun> {
	const { provider, model } = splitModel(modelSpec);
	const args = [
		"-p",
		"--mode", "json",
		...extraArgs,
		"--no-extensions",
		"--no-skills",
		"--no-context-files",
		"--no-prompt-templates",
		"--no-themes",
		"--thinking", "off",
		"--provider", provider,
		"--model", model,
		"--system-prompt", systemPrompt,
		dispatch,
	];
	return new Promise<ScoutRun>((resolve, reject) => {
		const child = spawn("pi", args, { cwd, stdio: ["ignore", "pipe", "pipe"] });
		// Incremental line parse — NEVER buffer the raw stream (a runaway
		// scout can emit GBs of token events). Keep only what we need.
		let buf = "";
		let bytesSeen = 0;
		let err = "";
		let text = "";
		let cost = 0;
		let tokens = 0;
		let turns = 0;
		let unparsedLines = 0;
		const MAX_BYTES = 100_000_000;
		const takeLine = (line: string) => {
			if (!line.trim()) return;
			let ev: any;
			try {
				ev = JSON.parse(line);
			} catch {
				unparsedLines++; // non-protocol noise; surfaced if the scout yields no report
				return;
			}
			poke(); // only real protocol events reset the idle timer
			if (ev.type === "turn_end") turns++;
			if (ev.type === "message_end" && ev.message?.role === "assistant") {
				const u = ev.message.usage;
				if (u) {
					cost += u.cost?.total ?? 0;
					tokens += u.totalTokens ?? 0;
				}
				const parts = Array.isArray(ev.message.content) ? ev.message.content : [];
				const t = parts
					.filter((p: any) => p.type === "text")
					.map((p: any) => p.text)
					.join("\n");
				if (t.trim()) text = t.slice(0, 200_000); // keep the LAST non-empty assistant text
			}
		};
		let settled = false;
		const timer = setTimeout(() => finish(new Error(`scout timed out after ${timeoutS}s`)), timeoutS * 1000);
		// A scout that emits nothing for 120s is wedged (e.g. dead server
		// connection) — kill fast instead of waiting out the full timeout.
		const IDLE_MS = 120_000;
		let idleTimer = setTimeout(() => finish(new Error("scout idle 120s — killed")), IDLE_MS);
		const poke = () => {
			clearTimeout(idleTimer);
			idleTimer = setTimeout(() => finish(new Error("scout idle 120s — killed")), IDLE_MS);
		};
		const onAbort = () => finish(new Error("scout aborted"));
		signal.addEventListener("abort", onAbort, { once: true });

		function finish(error?: Error) {
			if (settled) return;
			settled = true;
			clearTimeout(timer);
			clearTimeout(idleTimer);
			signal.removeEventListener("abort", onAbort);
			if (error) {
				child.kill("SIGKILL");
				reject(error);
				return;
			}
			takeLine(buf); // flush the final partial line
			if (!text) {
				reject(new Error(`scout produced no report (exit ${child.exitCode}, ${unparsedLines} unparsable stdout lines)\n${err.slice(-500)}`));
				return;
			}
			resolve({ text, cost, tokens, turns });
		}

		child.stdout.on("data", (d) => {
			bytesSeen += d.length;
			if (bytesSeen > MAX_BYTES) {
				finish(new Error("scout emitted >100MB — runaway generation, killed"));
				return;
			}
			buf += d;
			let nl: number;
			while ((nl = buf.indexOf("\n")) >= 0) {
				takeLine(buf.slice(0, nl));
				buf = buf.slice(nl + 1);
			}
		});
		child.stderr.on("data", (d) => {
			err = (err + d).slice(-2000);
		});
		child.on("error", (e) => finish(e as Error));
		child.on("close", () => finish());
	});
}


/** one retry for pipeline scouts — a killed/wedged scout gets a second shot */
async function runScoutRetry(
	dispatch: string, systemPrompt: string, modelSpec: string, cwd: string,
	signal: AbortSignal, timeoutS?: number,
): Promise<ScoutRun> {
	try {
		return await runScout(dispatch, systemPrompt, modelSpec, cwd, signal, timeoutS);
	} catch (e) {
		if (signal.aborted) throw e;
		return await runScout(dispatch, systemPrompt, modelSpec, cwd, signal, timeoutS);
	}
}

function capLines(text: string, cap: number): string {
	const lines = text.split("\n");
	if (lines.length <= cap) return text;
	return `${lines.slice(0, cap).join("\n")}\n…[scout report truncated at ${cap} lines — refine the dispatch instead of raising the cap]`;
}

const ESCAPE = "#nullius:ok";
const DEBUG = process.env.NULLIUS_DEBUG === "1";
const dbg = (msg: string) => DEBUG && console.error(`[nullius] ${msg}`);

export default function nullius(pi: ExtensionAPI) {
	if (process.env.NULLIUS_OFF === "1") return;

	// ── run stats ────────────────────────────────────────────────────────
	let leaderCost = 0;
	let scoutCost = 0;
	let scoutRuns = 0;
	let ctxTokens = 0;
	let prevCtxTokens = 0;
	let leaderTurns = 0;
	const readLedger = new Set<string>(); // path|offset|limit already read
	const editedSinceRead = new Set<string>();
	let gateState: EditGateState = initialGateState();

	const fmtK = (n: number) => (n >= 1000 ? `${(n / 1000).toFixed(1)}k` : `${n}`);
	const status = () =>
		`nullius ctx ${fmtK(ctxTokens)} (Δ${fmtK(Math.max(0, ctxTokens - prevCtxTokens))}/turn) · leader $${leaderCost.toFixed(2)} · ${scoutRuns} scouts $${scoutCost.toFixed(2)}` +
		(checklist.length ? ` · checklist ${checklist.length - openItems().length}/${checklist.length}` : "");

	// ── leader rules into the system prompt ──────────────────────────────
	pi.on("before_agent_start", async (event) => {
		return { systemPrompt: event.systemPrompt + LEADER_RULES };
	});

	// ── stats from every assistant message ───────────────────────────────
	pi.on("message_end", async (event, ctx) => {
		const m: any = event.message;
		if (m?.role !== "assistant" || !m.usage) return;
		leaderCost += m.usage.cost?.total ?? 0;
		prevCtxTokens = ctxTokens || m.usage.input + m.usage.cacheRead + m.usage.cacheWrite;
		ctxTokens = m.usage.input + m.usage.cacheRead + m.usage.cacheWrite + m.usage.output;
		leaderTurns++;
		if (ctx.hasUI) ctx.ui.setStatus("nullius", status());
	});

	// ── the scout tool ────────────────────────────────────────────────────
	pi.registerTool({
		name: "scout",
		label: "Scout",
		description:
			"Dispatch a throwaway read-only scout (cheap model, own context) to absorb bulk: builds, test runs, broad searches, hunting sweeps, verification reruns. Returns a capped STATUS/FACTS/VERBATIM/RISKS/UNKNOWN/RULED-OUT report. Batch independent dispatches in parallel. Modes: recon (open sweep of an area), narrow (one question), hunt (lens sweep — quote mechanisms or their absence), rerun (run named commands exactly, verbatim output = the trusted record).",
		parameters: Type.Object({
			mode: Type.Union(
				[Type.Literal("recon"), Type.Literal("narrow"), Type.Literal("hunt"), Type.Literal("rerun")],
				{ description: "Dispatch mode" },
			),
			dispatch: Type.String({
				description:
					"The full dispatch: the question/area/lenses/commands, what to REPORT, and any tool-call cap. Be surgical — the scout dies after answering.",
			}),
			cap_lines: Type.Optional(
				Type.Number({ description: `Report line ceiling (default ${DEFAULT_CAP_LINES})` }),
			),
			model: Type.Optional(
				Type.String({ description: "Override scout model as provider/model-id (rarely needed)" }),
			),
		}),
		async execute(_toolCallId, params: any, signal, _onUpdate, ctx) {
			const cap = Math.min(params.cap_lines ?? DEFAULT_CAP_LINES, 80);
			const dispatch = `MODE: ${params.mode}\nREPORT CEILING: ${cap} lines\n\n${params.dispatch}`;
			try {
				const run = await runScout(
					dispatch,
					SCOUT_PROMPT,
					params.model ?? SCOUT_MODEL,
					process.cwd(),
					signal,
				);
				scoutCost += run.cost;
				scoutRuns++;
				if (ctx.hasUI) ctx.ui.setStatus("nullius", status());
				let report = capLines(run.text, cap);
				if (params.mode !== "rerun") {
					report +=
						"\n\n[nullius: TESTIMONY, not verdict — before ruling on any suspect above (guilty OR innocent), read the quoted lines yourself with a ranged read. An unverified FACT may only close as UNKNOWN.]";
				}
				return {
					content: [{ type: "text", text: report }],
					details: { cost: run.cost, tokens: run.tokens, turns: run.turns },
				};
			} catch (e: any) {
				return {
					content: [
						{
							type: "text",
							text: `STATUS: fail\nFACTS: scout did not complete — ${e?.message ?? e}\nUNKNOWN: everything dispatched`,
						},
					],
					details: {},
					isError: true,
				};
			}
		},
	});

	// ── the advisor: reasoning-only judgment tier (consult tool) ─────────
	// The fast leader cannot be trusted with design decisions; the advisor
	// (a slow reasoning model, no tools) rules on structured consult packs.
	// Its memory is a persistent pi session: --session-id creates it on the
	// first consult, --session resumes it after. Guidance returns marked as
	// testimony — the leader quote-verifies before acting.
	let advisorSessionId: string | null = null;
	let consults = 0;
	let advisorCost = 0;
	if (ADVISOR_MODEL) {
		pi.registerTool({
			name: "consult",
			label: "Consult",
			description:
				"Consult the reasoning advisor (no tools — it knows ONLY what packs quote). MANDATORY at: stage=plan when the hunt checklist lands; stage=surprise when evidence contradicts the plan or a verdict stays ambiguous; stage=close before the close report. Fill every field mechanically from the ledger — quote verbatim, never summarize.",
			parameters: Type.Object({
				stage: Type.Union(
					[Type.Literal("terrain"), Type.Literal("plan"), Type.Literal("surprise"), Type.Literal("close")],
					{ description: "Which gate this consult serves" },
				),
				goal: Type.String({ description: "The mandate, verbatim from the user — never a paraphrase" }),
				state: Type.String({
					description: "Every checklist/plan item with its status, one per line; 'none yet' before the hunt",
				}),
				evidence: Type.Array(Type.String(), {
					description:
						"NEW quoted facts since the last consult, each item 'path:line — `verbatim quote`'. Mechanisms and machine output only. Include everything that CONTRADICTS the current plan. Exactly ['NONE'] if nothing is new.",
				}),
				question: Type.String({ description: "The single ruling you need, one sentence" }),
			}),
			async execute(_toolCallId, params: any, signal) {
				const bad = validateConsult(params);
				if (bad) return { content: [{ type: "text", text: bad }], details: {}, isError: true };
				const pack = renderPack(++consults, params);
				const fresh = !advisorSessionId;
				const sid = (advisorSessionId ??= `nullius-advisor-${Date.now().toString(36)}`);
				try {
					const run = await runScout(pack, ADVISOR_PROMPT, ADVISOR_MODEL, process.cwd(), signal, ADVISOR_TIMEOUT_S, [
						fresh ? "--session-id" : "--session", sid, "--no-tools",
					]);
					advisorCost += run.cost;
					return {
						content: [
							{
								type: "text",
								text: `${capLines(run.text, 100)}\n\n[nullius: advisor guidance is TESTIMONY — quote-verify every mechanism it names before acting on it.]`,
							},
						],
						details: { cost: run.cost, tokens: run.tokens, consult: consults },
					};
				} catch (e: any) {
					consults--;
					if (fresh) advisorSessionId = null; // session may not exist — mint anew next time
					return {
						content: [{ type: "text", text: `consult failed — ${e?.message ?? e}` }],
						details: {},
						isError: true,
					};
				}
			},
		});
	}

	// ── the hunt pipeline: deterministic scan → hunters → refuters ───────
	interface Item {
		id: string;
		lens: string;
		loc: string; // file:line (fn)
		note: string;
		status: "open" | "fixed" | "refuted" | "out_of_mandate";
		origin: "mechanical" | "hunter" | "unverified";
	}
	let checklist: Item[] = [];
	let nags = 0;
	const openItems = () => checklist.filter((i) => i.status === "open");

	const SCOUT_PAR = Number(process.env.NULLIUS_SCOUT_PARALLEL ?? 5);
	async function pMap<T, R>(items: T[], limit: number, f: (t: T) => Promise<R>): Promise<R[]> {
		const out: R[] = new Array(items.length);
		let i = 0;
		await Promise.all(
			Array.from({ length: Math.min(limit, items.length) }, async () => {
				while (i < items.length) {
					const idx = i++;
					out[idx] = await f(items[idx]);
				}
			}),
		);
		return out;
	}

	const chunk = <T,>(a: T[], n: number): T[][] => {
		const r: T[][] = [];
		for (let i = 0; i < a.length; i += n) r.push(a.slice(i, i + n));
		return r;
	};

	const HUNTER_SYS = `You adjudicate suspects in a codebase. For EACH numbered target, read the named file region yourself and answer with EXACTLY one line:
R|<target-number>|PRESENT|<the quoted mechanism, one line, with its guard>
R|<target-number>|ABSENT|<what is missing, one line>
A comment, a name, or a sibling function is NOT a mechanism — only code on the target's own path counts. When you cannot decide from code you actually read, answer ABSENT (fail closed). Output ONLY R| lines, one per target, nothing else.`;

	const REFUTER_SYS = `You try to REFUTE claimed defects. For EACH numbered claim, hunt the codebase for a mechanism that makes the code correct anyway (a caller holding the lock, a design where the behavior is intended, an invariant enforced elsewhere). Answer EXACTLY one line per claim:
V|<claim-number>|REFUTED|<the quoted mechanism or design fact, one line>
V|<claim-number>|CONFIRMED|<why no mechanism saves it, one line>
Default to CONFIRMED if uncertain. Output ONLY V| lines, nothing else.`;

	function parseLines(text: string, tag: string): Map<number, { verdict: string; detail: string }> {
		const m = new Map<number, { verdict: string; detail: string }>();
		for (const line of text.split("\n")) {
			const parts = line.trim().split("|");
			if (parts[0] !== tag || parts.length < 4) continue;
			const n = Number(parts[1]);
			if (Number.isFinite(n)) m.set(n, { verdict: parts[2].trim().toUpperCase(), detail: parts.slice(3).join("|").trim() });
		}
		return m;
	}

	// The hunt tool and the auto-hunt can both want the pipeline; two
	// concurrent runs interleave at the awaits and corrupt the shared
	// checklist, so everyone joins one flight. huntRan commits only on
	// success — a failed auto-hunt leaves the manual hunt (and the
	// agent_settled nag) as the recovery path.
	const startHunt = singleFlight(async (signal: AbortSignal, progress?: (s: string) => void) => {
		const summary = await runHuntPipeline(signal, progress);
		huntRan = true;
		return summary;
	});

	async function runHuntPipeline(signal: AbortSignal, progress?: (s: string) => void): Promise<string> {
			const pkgDir = join(dirname(__filename), "..");
			const root = process.cwd();
			progress?.("scanning (tree-sitter)…");
			const scan = await scanRepo(root, pkgDir);
			const isTestPath = (f: string) => /_test\.|(^|\/)tests?(\/|$)|conformance/.test(f);
			const kept = scan.findings.filter((f) => !isTestPath(f.file));
			const dropped = scan.findings.length - kept.length;
			const mechanicalAbsent = kept.filter((f) => f.status === "absent");
			const ambiguous = kept.filter((f) => f.status === "ambiguous");

			// Stage 2: hunters adjudicate AMBIGUOUS, grouped by lens, chunked.
			const jobs: { lens: string; targets: Finding[] }[] = [];
			for (const lens of LENSES) {
				const t = ambiguous.filter((f) => f.lens === lens.id);
				for (const c of chunk(t, 10)) jobs.push({ lens: lens.id, targets: c });
			}
			let done = 0;
			const hunterResults = await pMap(jobs, SCOUT_PAR, async (job) => {
				const lens = lensById(job.lens)!;
				const list = job.targets
					.map(
						(f, i) =>
							`${i + 1}. ${f.file}:${f.line} in ${f.fn}() — ${f.note}\n<body>\n${(f.snippet ?? "").slice(0, 2500)}\n</body>`,
					)
					.join("\n\n");
				const dispatch = `LENS: ${lens.title}\nQUESTION per target: ${lens.huntQuestion}\nEach target's function body is inlined — answer from it when it suffices; read the file/callers ONLY when the body alone cannot decide (e.g. lock possibly held by callers). Use at most 10 tool calls total.\n\nTARGETS:\n${list}`;
				try {
					const run = await runScoutRetry(dispatch, HUNTER_SYS, SCOUT_MODEL, root, signal, 900);
					scoutCost += run.cost;
					scoutRuns++;
					done++;
					progress?.(`hunters ${done}/${jobs.length}`);
					return { job, parsed: parseLines(run.text, "R") };
				} catch {
					done++;
					return { job, parsed: new Map<number, { verdict: string; detail: string }>() };
				}
			});

			const hunterAbsent: (Finding & { detail?: string; origin: "hunter" | "unverified" })[] = [];
			let clearedByHunters = 0;
			const unparsed: Finding[] = [];
			for (const { job, parsed } of hunterResults) {
				job.targets.forEach((f, i) => {
					const r = parsed.get(i + 1);
					if (!r) unparsed.push(f);
					else if (r.verdict === "ABSENT") hunterAbsent.push({ ...f, origin: "hunter", detail: r.detail });
					else clearedByHunters++;
				});
			}
			// Second pass for unparsed targets, smaller chunks parse better.
			let unverifiedCount = 0;
			if (unparsed.length) {
				progress?.(`retrying ${unparsed.length} unparsed targets`);
				const retryJobs: { lens: string; targets: Finding[] }[] = [];
				for (const lens of LENSES) {
					for (const c of chunk(unparsed.filter((f) => f.lens === lens.id), 4)) retryJobs.push({ lens: lens.id, targets: c });
				}
				const retryResults = await pMap(retryJobs, SCOUT_PAR, async (job) => {
					const lens = lensById(job.lens)!;
					const list = job.targets
						.map((f, i) => `${i + 1}. ${f.file}:${f.line} in ${f.fn}() — ${f.note}\n<body>\n${(f.snippet ?? "").slice(0, 2500)}\n</body>`)
						.join("\n\n");
					try {
						const run = await runScoutRetry(
							`LENS: ${lens.title}\nQUESTION per target: ${lens.huntQuestion}\nAnswer from the inlined bodies. Output ONLY R| lines.\n\nTARGETS:\n${list}`,
							HUNTER_SYS, SCOUT_MODEL, root, signal, 900,
						);
						scoutCost += run.cost;
						scoutRuns++;
						return { job, parsed: parseLines(run.text, "R") };
					} catch {
						return { job, parsed: new Map<number, { verdict: string; detail: string }>() };
					}
				});
				for (const { job, parsed } of retryResults) {
					job.targets.forEach((f, i) => {
						const r = parsed.get(i + 1);
						if (!r) unverifiedCount++; // demoted to a summary line, not a checklist item
						else if (r.verdict === "ABSENT") hunterAbsent.push({ ...f, origin: "hunter", detail: r.detail });
						else clearedByHunters++;
					});
				}
			}

			// Stage 3: refuters attack every ABSENT except decided tautologies.
			const decided = mechanicalAbsent.filter((f) => f.lens === "wake-predicates");
			const toRefute: (Finding & { detail?: string; origin: "mechanical" | "hunter" | "unverified" })[] = [
				...mechanicalAbsent.filter((f) => f.lens !== "wake-predicates").map((f) => ({ ...f, origin: "mechanical" as const })),
				...hunterAbsent,
			];
			const refJobs = chunk(toRefute, 5);
			const refResults = await pMap(refJobs, SCOUT_PAR, async (claims) => {
				const list = claims
					.map((f, i) => `${i + 1}. ${f.file}:${f.line} in ${f.fn}() — [${f.lens}] ${f.note}${f.detail ? ` | hunter: ${f.detail}` : ""}`)
					.join("\n");
				try {
					const run = await runScoutRetry(`CLAIMED DEFECTS:\n${list}`, REFUTER_SYS, SCOUT_MODEL, root, signal, 900);
					scoutCost += run.cost;
					scoutRuns++;
					return { claims, parsed: parseLines(run.text, "V") };
				} catch {
					return { claims, parsed: new Map<number, { verdict: string; detail: string }>() };
				}
			});

			checklist = [];
			let n = 0;
			const add = (f: Finding & { detail?: string }, origin: Item["origin"]) =>
				checklist.push({
					id: `N${++n}`,
					lens: f.lens,
					loc: `${f.file}:${f.line} (${f.fn})`,
					note: f.detail ? `${f.note} | ${f.detail}` : f.note,
					status: "open",
					origin,
				});
			for (const f of decided) add(f, "mechanical");
			let refuted = 0;
			for (const { claims, parsed } of refResults) {
				claims.forEach((f, i) => {
					const r = parsed.get(i + 1);
					if (r?.verdict === "REFUTED") {
						refuted++;
						return; // survived-refutation only reaches the leader
					}
					add(f, (f as any).origin ?? "hunter");
				});
			}

			const CAP = 40;
			let overflow = 0;
			if (checklist.length > CAP) {
				const prio = (i: Item) => (i.origin === "mechanical" ? 0 : 1);
				checklist.sort((x, y) => prio(x) - prio(y));
				overflow = checklist.length - CAP;
				checklist = checklist.slice(0, CAP);
				checklist.forEach((i, idx) => (i.id = `N${idx + 1}`));
			}
			const lines = checklist.map((i) => {
				const hint = lensById(i.lens)?.hint ?? "";
				return `${i.id} [${i.lens}${i.origin === "unverified" ? ", UNVERIFIED" : ""}] ${i.loc} — ${i.note}\n    fix shape: ${hint}`;
			});
			const summary = `HUNT COMPLETE (deterministic scan: ${scan.filesScanned} files, ${scan.fnScanned} functions; ${dropped} test-path findings set aside; hunters cleared ${clearedByHunters}, refuters cleared ${refuted}${scan.unpackedFiles.length ? `; ${scan.unpackedFiles.length} files in unpacked languages NOT scanned — hunt them via scout` : ""}).

CHECKLIST — ${checklist.length} suspects survived adversarial refutation${overflow ? ` (${overflow} lower-priority suspects withheld — ask via hunt again after ruling these)` : ""}${unverifiedCount ? `; ${unverifiedCount} targets unadjudicated after retry (scout failures) — treat their areas as UNKNOWN` : ""}. Each is IN-MANDATE. For EVERY item: read the quoted location yourself (ranged), then call rule(id, verdict, evidence) — "fixed" (name the test that proves it), "refuted" (quote the mechanism that clears it), or "out_of_mandate" (reason). You cannot close with open items.

${lines.join("\n")}`;
			return ADVISOR_MODEL
				? `${summary}\n\nMANDATORY NEXT: consult(stage=plan) with this checklist as STATE — the advisor rules fix order and what is load-bearing before you rule or fix anything.`
				: summary;
	}

	pi.registerTool({
		name: "hunt",
		label: "Hunt",
		description:
			"Run the nullius hunt pipeline over the whole tree: a deterministic tree-sitter lens scan (no LLM) enumerates every relied-on property with no locally visible mechanism; scout hunters adjudicate the ambiguous ones; independent refuters attack every ABSENT claim. Returns a checklist of surviving suspects — each is in-mandate and MUST be ruled via the rule tool (fixed / refuted / out_of_mandate) before you close. Call this ONCE, early, before designing anything (skip if the harness already delivered the checklist).",
		parameters: Type.Object({}),
		async execute(_id, _params, signal, onUpdate, ctx) {
			const summary = await startHunt(signal, (t) => onUpdate?.({ content: [{ type: "text", text: t }] }));
			if (ctx.hasUI) ctx.ui.setStatus("nullius", status());
			return { content: [{ type: "text", text: summary }], details: { items: checklist.length } };
		},
	});

	pi.registerTool({
		name: "rule",
		label: "Rule",
		description:
			"Rule on one hunt-checklist item. verdict=fixed requires the test name that proves the fix; verdict=refuted requires the QUOTED mechanism that clears the suspect; verdict=out_of_mandate requires the reason. Every checklist item must be ruled before close.",
		parameters: Type.Object({
			id: Type.String({ description: "checklist item id, e.g. N3" }),
			verdict: Type.Union([Type.Literal("fixed"), Type.Literal("refuted"), Type.Literal("out_of_mandate")]),
			evidence: Type.String({ description: "fixed: test name + change; refuted: quoted mechanism; out_of_mandate: reason" }),
		}),
		async execute(_id, params: any) {
			const item = checklist.find((i) => i.id === params.id);
			if (!item) return { content: [{ type: "text", text: `no such item ${params.id}` }], details: {}, isError: true };
			const evidence = (params.evidence ?? "").trim();
			if (evidence.length < 15)
				return {
					content: [{ type: "text", text: `evidence too thin for ${params.id} — quote the mechanism/test, don't assert it` }],
					details: {},
					isError: true,
				};
			if (params.verdict === "refuted") {
				// nullius in verba, mechanically: the quoted mechanism must
				// literally exist on disk in the item's file (or a file the
				// evidence names as path:line). Whitespace-normalized substring.
				const norm = (t: string) => t.replace(/\s+/g, " ").trim();
				const evNorm = norm(evidence);
				const fileOf = (loc: string) => loc.split(":")[0];
				const candidates = [fileOf(item.loc), ...(evidence.match(/[\w\/.-]+\.[a-z]{1,4}(?=[:\s])/g) ?? [])];
				let quoteFound = false;
				for (const f of new Set(candidates)) {
					try {
						const src = norm(readFileSync(join(process.cwd(), f), "utf8"));
						// require a ≥20-char contiguous fragment of the evidence to appear in the file
						for (let i = 0; i + 20 <= evNorm.length && !quoteFound; i += 10) {
							if (src.includes(evNorm.slice(i, i + Math.min(60, evNorm.length - i)))) quoteFound = true;
						}
					} catch {
						/* file unreadable — try next */
					}
					if (quoteFound) break;
				}
				if (!quoteFound)
					return {
						content: [
							{
								type: "text",
								text: `refutation rejected for ${params.id}: the evidence does not quote code that exists in ${fileOf(item.loc)} (or any file it names). Read the location and quote the ACTUAL mechanism verbatim, or rule it fixed/out_of_mandate.`,
							},
						],
						details: {},
						isError: true,
					};
			}
			item.status = params.verdict;
			const open = openItems().length;
			return {
				content: [{ type: "text", text: `${params.id} ruled ${params.verdict}. ${open} item(s) still open${open ? ": " + openItems().map((i) => i.id).join(",") : " — checklist clear."}` }],
				details: {},
			};
		},
	});

	let huntRan = false;
	let editsHappened = false;
	pi.on("agent_settled", async (_e, _ctx) => {
		if (!huntRan && editsHappened && nags < 2) {
			nags++;
			pi.sendUserMessage(
				"nullius: you edited code but the hunt pipeline never ran — the whole tree is unhunted. Call the hunt tool NOW, then rule every checklist item before closing.",
				{ deliverAs: "followUp" },
			);
			return;
		}
		const open = openItems();
		if (open.length && nags < 2) {
			nags++;
			pi.sendUserMessage(
				`nullius: ${open.length} hunt-checklist item(s) unruled: ${open.map((i) => `${i.id} ${i.loc}`).join("; ")}. Rule each via rule(id, verdict, evidence) — fixed (name the proving test), refuted (quote the mechanism), or out_of_mandate (reason). The run is not closed while items are open.`,
				{ deliverAs: "followUp" },
			);
		}
	});

	// ── the diet governor ─────────────────────────────────────────────────
	pi.on("tool_call", async (event) => {
		if (!DIET_ON) return;

		if (isToolCallEventType("read", event)) {
			const inp: any = event.input;
			const path = inp.path as string;
			const key = `${path}|${inp.offset ?? 0}|${inp.limit ?? 0}`;
			if (readLedger.has(key) && !editedSinceRead.has(path)) {
				return {
					block: true,
					reason: `nullius: you already read ${path} with this exact range — you hold it. Re-reads re-pay the tokens every turn. Work from what you have, or read a DIFFERENT range.`,
				};
			}
			if (!inp.limit && existsSync(path)) {
				let lineCount = 0;
				try {
					lineCount = readFileSync(path, "utf8").split("\n").length;
				} catch {
					/* binary or unreadable — let the read proceed */
				}
				if (lineCount > MAX_WHOLE_READ) {
					return {
						block: true,
						reason: `nullius: ${path} is ${lineCount} lines — a whole read of a file this size is resident context you re-pay every turn. Read the decisive region with offset/limit (≤${MAX_WHOLE_READ} lines), or dispatch scout(mode=narrow) to extract what you need.`,
					};
				}
			}
			readLedger.add(key);
			editedSinceRead.delete(path);
		}

		if (isToolCallEventType("edit", event) || isToolCallEventType("write", event)) {
			const inp: any = event.input;
			const p = inp.path as string | undefined;
			if (p) {
				const decision = evaluateEditGate(
					{ path: p, newText: editText(inp) },
					gateState,
					{ maxEditLines: MAX_EDIT_LINES, testsFirstThreshold: TESTS_FIRST_THRESHOLD },
				);
				gateState = decision.state;
				if (decision.block) return { block: true, reason: decision.reason };
				editedSinceRead.add(p);
			}
			editsHappened = true;
		}

		if (isToolCallEventType("bash", event)) {
			const inp: any = event.input;
			const cmd = (inp.command as string) ?? "";
			if (cmd.includes(ESCAPE)) return; // explicit leader override
			if (isHeavyCmd(cmd)) {
				return {
					block: true,
					reason: `nullius: builds/tests/linters never run in your context — their output is bulk you re-pay every turn. Dispatch scout(mode=rerun) with this exact command; its verbatim run is the trusted record. (Override only if genuinely tiny: prefix the command with ${ESCAPE}.)`,
				};
			}
			if (isWideSearchCmd(cmd) && !isBoundedCmd(cmd)) {
				return {
					block: true,
					reason: `nullius: repo-wide searches belong in a scout (mode=recon/hunt) — dispatch it and take the capped report. If you must run this yourself, bound it: append '2>&1 | tail -n 20'.`,
				};
			}
			// Silently bound single-line unbounded commands.
			const bounded = boundCommand(cmd);
			if (bounded) inp.command = bounded;
		}
	});

	// ── the nullius compactor ─────────────────────────────────────────────
	pi.on("session_before_compact", async (event, ctx) => {
		const prep: any = (event as any).preparation;
		if (!prep?.messagesToSummarize?.length) return;
		const transcript = prep.messagesToSummarize
			.map((m: any) => {
				const role = m.role ?? m.customType ?? "?";
				const content = Array.isArray(m.content)
					? m.content
							.map((p: any) =>
								p.type === "text"
									? p.text
									: p.type === "toolCall"
										? `[tool ${p.name}: ${JSON.stringify(p.arguments ?? {}).slice(0, 200)}]`
										: `[${p.type}]`,
							)
							.join("\n")
					: String(m.content ?? "");
				return `--- ${role} ---\n${content.slice(0, 2000)}`;
			})
			.join("\n")
			.slice(0, 200_000);
		try {
			const ac = new AbortController();
			const sig: AbortSignal = (event as any).signal ?? ac.signal;
			const run = await runScout(
				`Compact this session transcript into the ledger.\n\n<transcript>\n${transcript}\n</transcript>`,
				COMPACTOR_PROMPT,
				SCOUT_MODEL,
				process.cwd(),
				sig,
				120,
			);
			scoutCost += run.cost;
			scoutRuns++;
			if (ctx.hasUI) ctx.ui.notify(`nullius compactor: ledger written ($${run.cost.toFixed(3)})`, "info");
			return {
				compaction: {
					summary: run.text,
					firstKeptEntryId: prep.firstKeptEntryId,
					tokensBefore: prep.tokensBefore,
				},
			};
		} catch {
			return; // fall back to pi's built-in summarizer
		}
	});

	// ── /nullius command ──────────────────────────────────────────────────
	pi.registerCommand("nullius", {
		description: "Show nullius run stats and config",
		handler: async (_args, ctx) => {
			ctx.ui.notify(
				`${status()} · ${leaderTurns} leader turns · scout=${SCOUT_MODEL}${ADVISOR_MODEL ? ` · advisor=${ADVISOR_MODEL} (${consults} consults $${advisorCost.toFixed(2)})` : ""} · diet=${DIET_ON ? "on" : "off"} (NULLIUS_DIET=0 to disable)`,
				"info",
			);
		},
	});

	let autoHuntStarted = false;
	function kickAutoHunt(ctx: any) {
		// AUTO-HUNT: the pipeline is not the leader's choice. Kick it off in
		// the background; deliver the checklist as a user message the moment
		// it lands. (session_start does not fire in -p mode — hence also
		// triggered from before_agent_start.)
		if (process.env.NULLIUS_AUTOHUNT === "0" || huntRan || autoHuntStarted) return;
		autoHuntStarted = true;
		const ac = new AbortController();
		pi.on("session_shutdown", async () => ac.abort());
		void (async () => {
			try {
				const summary = await startHunt(ac.signal, (t) => {
					if (ctx.hasUI) ctx.ui.setStatus("nullius-hunt", `hunt: ${t}`);
				});
				if (!checklist.length) {
					if (ctx.hasUI) ctx.ui.setStatus("nullius-hunt", "hunt: clean — no suspects");
					return;
				}
				if (ctx.hasUI) ctx.ui.setStatus("nullius-hunt", "hunt: checklist delivered");
				pi.sendUserMessage(`nullius auto-hunt finished while you worked. ${summary}`, { deliverAs: "followUp" });
			} catch (e: any) {
				if (!ac.signal.aborted) pi.sendUserMessage(`nullius auto-hunt FAILED (${e?.message ?? e}) — call the hunt tool yourself and rule the checklist before closing.`, { deliverAs: "followUp" });
			}
		})();
	}

	pi.on("session_start", async (_e, ctx) => {
		if (!ctx.hasUI) return; // headless: before_agent_start blocks instead
		ctx.ui.setStatus("nullius", `nullius armed · scout=${SCOUT_MODEL} · diet=${DIET_ON ? "on" : "off"}`);
		kickAutoHunt(ctx);
	});
	pi.on("before_agent_start", async (_e, ctx) => {
		dbg(`before_agent_start: hasUI=${(ctx as any).hasUI} huntRan=${huntRan} started=${autoHuntStarted}`);
		// Interactive: hunt concurrently, deliver via user message.
		// Headless (-p): the process exits at settle, so BLOCK here — the
		// checklist must be in context before the first turn.
		if (ctx.hasUI) {
			kickAutoHunt(ctx);
			return;
		}
		if (process.env.NULLIUS_AUTOHUNT === "0" || huntRan || autoHuntStarted) return;
		autoHuntStarted = true;
		try {
			const ac = new AbortController();
			pi.on("session_shutdown", async () => ac.abort());
			dbg("blocking auto-hunt: pipeline starting");
			const summary = await startHunt(ac.signal);
			dbg(`blocking auto-hunt: done, ${checklist.length} items`);
			if (!checklist.length) return;
			return {
				message: {
					customType: "nullius-checklist",
					content: `nullius auto-hunt ran before your first turn. ${summary}`,
					display: true,
				},
			};
		} catch (e: any) {
			return {
				message: {
					customType: "nullius-checklist",
					content: `nullius auto-hunt FAILED (${e?.message ?? e}) — call the hunt tool yourself and rule the checklist before closing.`,
					display: true,
				},
			};
		}
	});
}
