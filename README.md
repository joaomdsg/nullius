# byproxy

You act by proxy. You never touch the code directly.

## The problem nobody wants to name

Every AI coding tool in 2026 is obsessed with context engineering — curating what the model sees so it produces better output. Embeddings, RAG pipelines, dynamic rule loading, memory systems. All of it answers one question: *what should the model see?*

Two questions go unanswered. **Who should do the work?** — because the expensive model burns tokens reading files it could have delegated. And **what survives between steps?** — because chat history evaporates and the model re-derives what it already knew.

byproxy answers the first outright and the second within a session. One orchestrator reasons and decides; it never reads a file or runs a command itself. Cheap explorer agents are its eyes. A single builder is its hands. Everything they exchange is a terse, schema-bound telegraph, and that append-only telegraph log is the system's working memory — a compact, cache-stable record of every fact established, instead of raw files re-paged each turn.

(Honest caveat: the log lives in the orchestrator's context, so it is durable only within a session. To survive compaction or a new session, the open work must be re-emitted as an explicit telegraph — persistence across sessions is a discipline here, not yet an automatic artifact.)

## The architecture

This is the **control plane / data plane** split from networking, applied to coding agents. The orchestrator is the control plane: it holds no payload, it decides where things go. The agents are the data plane: they move the bytes.

- **You (the orchestrator).** Expensive model. Reason over telegraphs, decide, dispatch. Never touch ground truth. Doubt a load-bearing fact → dispatch a second explorer and compare.
- **Explorers.** Haiku-class, read-only, disposable. One narrow question each, then they cease to exist. They report facts and quote machine output verbatim — they never judge. The cheapest model in the loop must never decide what information survives.
- **Builder.** One Sonnet-class agent, serial, the only writer. Executes a precise spec with a mechanical exit condition, and escalates the moment a deterministic trigger fires rather than circling.

**Why it aims to be cheaper.** Raw file contents pulled into the orchestrator are re-fed to the most expensive model every turn and bloat its prefix. Telegraphs stay small and keep that prefix cache-stable, so cost grows slowly; a cheap model does the paging while the expensive one spends its tokens deciding. These are design bets, not measured constants — the win shows up on tasks big enough that re-reading dominates. On a small change, an orchestrator that just reads the files may be cheaper. Validate on your own workload.

## TGS — the telegraph protocol

All inter-agent messages use **TGS** (Telegraphic-Grok-Schema): labeled fields, no prose, grok-dense inside fields, machine output quoted verbatim, absence reported explicitly (`UNKNOWN:` is mandatory). A dispatch to the builder looks like:

```
TASK: fix off-by-one token count lex.go:31
SCOPE: lex.go only
CONTEXT: comment lex.go:29 claims intentional skip — preserve intent or update it
DONE-WHEN: TestParseHeader + TestComment green
ESCALATE-IF: 2 consecutive fail runs | fix requires touching parse.go | diff >30 lines
```

Full spec: [`references/tgs-spec.md`](.claude/skills/byproxy/references/tgs-spec.md).

## The builder's discipline: RYGB

The builder doesn't write code freehand — but byproxy doesn't pay a dispatch per TDD phase either (benchmarked: that triples builder turns for no correctness gain). The guarded cycle, v2:

- **Design red-team** (once per task) — the orchestrator fixes each unit's test list and API surface; a read-only explorer attacks the design *before* anything builds: write-only features, mandated-but-untested code, trivially-passing tests.
- **One dispatch = one unit's full cycle** — a fresh builder writes the tests, quotes the failure verbatim to prove it fails for the *right reason*, implements minimally, prunes, and reports with its diff stat.
- **Audit, pipelined** — a read-only explorer audits each landed unit (running in parallel with the next unit's builder); the final audit re-runs the full suite as verification.

Explorers report *findings*, never verdicts; the orchestrator judges everything and queues surfaced work in the telegraph log. That is byproxy's core rule applied to TDD: the cheapest model reports, the expensive model decides. Full spec in [`references/rygb-cycle.md`](.claude/skills/byproxy/references/rygb-cycle.md).

## What's in this repo

```
.claude/
  skills/byproxy/
    SKILL.md                  # the orchestrator's operating instructions
    references/
      tgs-spec.md             # the TGS message protocol
      rygb-cycle.md           # the builder's RYGB TDD discipline (orchestrator-driven)
  agents/
    byproxy-explorer.md       # read-only subagent (Haiku, no Edit/Write tools)
    byproxy-builder.md        # single-writer subagent (Sonnet, RYGB)
template/
  CONVENTIONS.md              # questions that produce reasoning, not bare rules —
                              # the orchestrator pulls project facts from here into
                              # dispatch CONTEXT fields
```

The **agent definitions** are what make this runnable: their frontmatter sets each subagent's model tier and — critically — its tool fence. The explorer has no Edit or Write tools, so file edits are blocked by the platform. It does keep Bash (it has to run tests and builds), and Bash can technically write — so read-only-through-Bash rests on the agent's prompt discipline plus your Claude Code permission settings, not the tool fence alone. Honest boundary: platform-enforced for edits, discipline-enforced for shell.

## Getting started

byproxy is a **Claude Code skill** (Claude-specific for now — it relies on Claude Code's subagent dispatch and agent-definition model). It needs two pieces installed: the skill *and* the agent definitions.

```sh
git clone https://github.com/joaomdsg/byproxy.git
# the skill (orchestrator instructions + references)
ln -s "$(pwd)/byproxy/.claude/skills/byproxy" ~/.claude/skills/byproxy
# the subagents it dispatches
ln -s "$(pwd)/byproxy/.claude/agents/byproxy-explorer.md" ~/.claude/agents/byproxy-explorer.md
ln -s "$(pwd)/byproxy/.claude/agents/byproxy-builder.md"  ~/.claude/agents/byproxy-builder.md
```

Then in any project, invoke `/byproxy` (or just describe a multi-agent coding task) and hand it the work.

## Status: three benchmarks in, measured headlessly

Full history in [`benchmarks/`](benchmarks/); benchmark 3 is the one to trust — it runs both arms headless via the [harness](benchmarks/harness/) (fresh worktrees, measured `total_cost_usd`, independent scoring, n=3 per arm), eliminating the inline-orchestration biases of benchmarks 1–2.

**Benchmark 3 (L-task, v2 workflow, fable-low orchestrator vs solo opus):**
6/6 runs fully green. Cost: statistical tie (byproxy mean $4.88 with a $0.15 spread; solo mean $4.40 with a $2.61 spread). Wall time: solo ~1.6× faster. byproxy's real differentiators: **cost predictability** and **self-auditing output** — every byproxy run shipped with a disclosed defect list from its own audit; solo reported unqualified success.

History: bench 1 (S-task, inline) byproxy ~40% cheaper; bench 2 (L-task, inline) byproxy lost ~1.8× — the cost was builder re-entry, which the v2 redesign (context packs, one dispatch per unit, design red-team, pipelined audits) eliminated: the explore+build data plane now costs ~$2.2, and the remaining cost center is the orchestrator's own price tag. Benchmark 4: a sonnet control plane (projected ~$2.6–3.2, under the solo mean) and a task whose DoD discriminates quality.

## The philosophy in one sentence

Reason from above, delegate the reading down, let one hand do the writing, and keep the only record you trust in the telegraph log — because the expensive model should spend its tokens thinking, not fetching.
