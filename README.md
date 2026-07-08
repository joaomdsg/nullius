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

The builder doesn't write code freehand. It runs the **Red → Yellow → Green → Blue** TDD cycle, one full cycle per logical unit:

- 🔴 **Red** — write the failing test; confirm it fails for the right reason.
- 🟡 **Yellow** — an explorer critiques the test (trivial-pass? missing edge cases? wrong failure?). The orchestrator judges the findings and redirects.
- 🟢 **Green** — minimal implementation to pass; nothing more.
- 🔵 **Blue** — an explorer audits coverage and correctness. The orchestrator classifies each finding as pure cleanup (redirect the builder) or a new cycle (queue it in the telegraph log). Never interleave.

Yellow and Blue are read-only *findings*; the verdict is always the orchestrator's. That is byproxy's core rule applied to TDD: the cheapest model reports, the expensive model decides. The orchestrator drives the loop and manages the queue of surfaced work — full spec in [`references/rygb-cycle.md`](.claude/skills/byproxy/references/rygb-cycle.md).

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

## Status: unbenchmarked

No transcript with a token bill exists yet. The claim that this beats "just let the expensive model read the files" is a design bet stated honestly, not a measured result. The next artifact this repo needs is a real orchestration log with costs, next to a single-agent baseline on the same task. Until then, treat the economics as a hypothesis you validate on your own workload.

## The philosophy in one sentence

Reason from above, delegate the reading down, let one hand do the writing, and keep the only record you trust in the telegraph log — because the expensive model should spend its tokens thinking, not fetching.
