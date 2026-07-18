/**
 * Lens catalog + language packs for the deterministic hunt engine.
 *
 * Language knowledge is DATA: a pack maps a language to tree-sitter node
 * queries; universal name lists are shared across languages. A language
 * with no pack degrades to open LLM hunts — pack coverage buys precision
 * and cost, never soundness.
 */

export interface LangPack {
	wasm: string; // grammar file under tree-sitter-wasms/out/
	/** query capturing @name and @body of every function/method definition */
	fnQuery: string;
	/** node types that make an lvalue "shared-looking" (field/index access) */
	sharedLhs: RegExp;
	/** defer/finally-style constructs (node types), for release checking */
	deferNodes: string[];
	/** swallowed-error patterns on body text */
	swallowRe?: RegExp;
}

export const EXT_TO_LANG: Record<string, string> = {
	".go": "go",
	".ts": "typescript",
	".tsx": "typescript",
	".js": "javascript",
	".jsx": "javascript",
	".py": "python",
	".rs": "rust",
	".java": "java",
};

export const LANG_PACKS: Record<string, LangPack> = {
	go: {
		wasm: "tree-sitter-go.wasm",
		fnQuery: `[
			(function_declaration name: (identifier) @name body: (block) @body)
			(method_declaration name: (field_identifier) @name body: (block) @body)
		]`,
		sharedLhs: /^[\w)]+(\.|\[)/,
		deferNodes: ["defer_statement"],
		swallowRe: /(^|\s)_\s*=\s*\w|(\w+),\s*_\s*:?=/,
	},
	typescript: {
		wasm: "tree-sitter-typescript.wasm",
		fnQuery: `[
			(function_declaration name: (identifier) @name body: (statement_block) @body)
			(method_definition name: (property_identifier) @name body: (statement_block) @body)
		]`,
		sharedLhs: /^this\.|^[\w)]+(\.|\[)/,
		deferNodes: ["finally_clause"],
		swallowRe: /catch\s*(\([^)]*\))?\s*\{\s*\}/,
	},
	javascript: {
		wasm: "tree-sitter-javascript.wasm",
		fnQuery: `[
			(function_declaration name: (identifier) @name body: (statement_block) @body)
			(method_definition name: (property_identifier) @name body: (statement_block) @body)
		]`,
		sharedLhs: /^this\.|^[\w)]+(\.|\[)/,
		deferNodes: ["finally_clause"],
		swallowRe: /catch\s*(\([^)]*\))?\s*\{\s*\}/,
	},
	python: {
		wasm: "tree-sitter-python.wasm",
		fnQuery: `(function_definition name: (identifier) @name body: (block) @body)`,
		sharedLhs: /^self\.|^[\w)]+(\.|\[)/,
		deferNodes: ["finally_clause"],
		swallowRe: /except[^:]*:\s*pass/,
	},
	rust: {
		wasm: "tree-sitter-rust.wasm",
		fnQuery: `(function_item name: (identifier) @name body: (block) @body)`,
		sharedLhs: /^self\.|^[\w)]+(\.|\[)/,
		deferNodes: [],
		swallowRe: /\.ok\(\)\s*;|let\s+_\s*=/,
	},
	java: {
		wasm: "tree-sitter-java.wasm",
		fnQuery: `(method_declaration name: (identifier) @name body: (block) @body)`,
		sharedLhs: /^this\.|^[\w)]+(\.|\[)/,
		deferNodes: ["finally_clause"],
		swallowRe: /catch\s*\([^)]*\)\s*\{\s*\}/,
	},
};

/** Universal name lists — lowercase substring matching on callee names. */
export const GUARD_CALLS = ["lock", "rlock", "trylock", "acquire", "compareandswap", "compare_and_swap", "cas", "synchronized", "withlock", "with_lock", "mutex", "serialize"];
export const RELEASE_CALLS = ["unlock", "release", "close", "stop", "dispose", "shutdown", "put", "free"];
export const ACQUIRE_CALLS = ["lock", "rlock", "acquire", "open", "begin", "subscribe", "register", "addlistener"];
export const FANOUT_CALLS = ["broadcast", "publish", "notify", "notifyall", "emit", "wake", "wakeall", "sendall", "send_all", "fanout", "dispatchall"];
export const CLEAR_CALLS = ["clear", "reset", "truncate", "drain", "flushqueue", "empty"];
export const SEND_CALLS = ["write", "send", "flush", "push", "publish", "emit", "deliver"];
export const NIL_ARGS = ["nil", "null", "none", "undefined"];
export const SCOPE_HINTS = ["sess", "session", "scope", "tenant", "user", "conn", "client", "key", "filter", "id"];

export interface Lens {
	id: string;
	title: string;
	/** what the leader should do about a confirmed finding */
	hint: string;
	/** dispatch text for LLM hunters adjudicating AMBIGUOUS targets */
	huntQuestion: string;
}

export const LENSES: Lens[] = [
	{
		id: "serialization",
		title: "unserialized shared mutation",
		hint: "add the lock/CAS INSIDE the mutating entrypoint's own body (a caller's or sibling's lock is not a mechanism)",
		huntQuestion:
			"Does anything serialize this function's mutation of shared state — a lock taken in ITS OWN body, single-goroutine/channel ownership, or a CAS loop spanning the read-modify-write? Quote the mechanism with its guards, or answer ABSENT.",
	},
	{
		id: "scope-confinement",
		title: "fan-out without scope filter",
		hint: "pass the owning session/tenant/scope at the fan-out call site so only in-scope receivers wake",
		huntQuestion:
			"At this fan-out/broadcast call: quote the argument or filter that confines delivery to the owning session/scope/tenant. A nil/absent filter that wakes everyone is ABSENT.",
	},
	{
		id: "wake-predicates",
		title: "wake/skip predicate cannot discriminate",
		hint: "make the predicate actually depend on the receiver's state (and read it under the same lock as its writes)",
		huntQuestion:
			"Can this predicate actually be false (or true) for some receiver? A tautology (e.g. len(x) >= 0, || true) or contradiction is ABSENT. Quote the predicate and say which inputs flip it.",
	},
	{
		id: "fault-survival",
		title: "effect cleared before its write is confirmed",
		hint: "clear/ack only AFTER the write succeeds; on failure the data must survive for retry (at-least-once)",
		huntQuestion:
			"This body clears/drains state and also writes/sends. Quote the ordering: is the clear confirmed-after-write (survives a failed write), or does a fault between clear and send lose the data (ABSENT)?",
	},
	{
		id: "resource-release",
		title: "acquire without release on all paths",
		hint: "release via defer/finally so every path — including error paths — releases",
		huntQuestion:
			"This body acquires (lock/handle/subscription) with no visible release on all paths. Quote the release mechanism covering EVERY path including errors, or ABSENT.",
	},
	{
		id: "swallowed-errors",
		title: "error path drops the error",
		hint: "propagate, retry, or record the error — deliberate discards need a mechanism (log + recovery), not silence",
		huntQuestion:
			"This error is discarded. Quote what makes the discard safe (the error genuinely cannot matter, or is handled elsewhere on this path), or ABSENT.",
	},
	{
		id: "lifecycle-races",
		title: "cleanup races liveness",
		hint: "cleanup/sweep must check liveness under the same synchronization the liveness writers use",
		huntQuestion:
			"For this sweep/TTL/cleanup path: quote the check that prevents reclaiming a still-live resource (and the synchronization making that check race-free), or ABSENT.",
	},
];

export const lensById = (id: string) => LENSES.find((l) => l.id === id);
