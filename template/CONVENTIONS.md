# Conventions

This file grows as the team learns. Thin on day one is honest. A long list of rules with no reasoning is a liability — nobody knows which ones still matter, or why they were written.

The format here is questions. Answer them. The answers become your conventions.

---

## Error Handling

- How do you handle errors at system boundaries (user input, external APIs)?
- How do you handle internal errors — do you let them propagate, log, recover?
- What's your strategy for communicating errors to callers vs. users?
- Where have errors bitten you before, and what did you learn?

_Answer:_

---

## Concurrency and State

- What's your concurrency model?
- Where is shared state, and how is it protected?
- What concurrency bug or race condition has caused you pain in this codebase?

_Answer:_

---

## Testing

- What does a good test look like here?
- What does the test name communicate? What do the assertions communicate?
- When does a test need a comment? (Default: when you need to explain _why_, not what.)
- What makes a test brittle in this project, and how do you avoid it?
- What's your policy on mocks and fakes?

_Answer:_

**Test run strategy:** Run the full suite at 🔴 Red (to verify the new test fails) and at unit completion. During 🟢 Green, run scoped tests — the new test plus existing tests for the modules modified. Scoped tests passing is sufficient to proceed within a cycle.

_Fill in your test filter command (e.g., `jest src/auth/`, `pytest tests/auth/`, `go test ./auth/...`):_

---

## Naming

- What naming conventions exist, and why do they exist?
- Are there naming patterns you've regretted and moved away from?
- How do you name things that cross domain boundaries?

_Answer:_

---

## Structure and Boundaries

- How is the codebase organized? What's the principle behind the structure?
- Where are the hard boundaries, and what enforces them?
- What's allowed to depend on what?

_Answer:_

---

## Dependencies

- What's your policy on adding external dependencies?
- What criteria does a new dependency have to meet?
- Are there categories of dependency you avoid and why?

_Answer:_

---

## Glossary

_Pin domain terms used across specs and code. When a term has more than one plausible meaning in this domain, define the one this project uses._

_Terms defined here are the authoritative definitions. When a dispatch's `CONTEXT:` needs a domain term, it draws from here — one place, one meaning._

| Term | Definition |
|---|---|
| | |

---

## Anti-patterns

_Concrete examples of what convention violations look like in code. Add entries as you find them. The agent can search for these patterns mechanically._

| Convention | Violation looks like | Example to search for |
|---|---|---|
| | | |

---

## Commit conventions

```
feat(<area>): <summary>
fix(<area>): <summary>
refactor(<area>): <summary>
test(<area>): <summary>
```

_State the shape your commits take and why. One logical unit per commit keeps history reviewable._

---

## Things We've Learned the Hard Way

_This section grows with incidents, postmortems, and painful lessons. Short entries are fine. Include a date or reference if useful._

-

---

## Notes for byproxy

_Project-specific facts byproxy should feed into explorer dispatch `CONTEXT:` fields and its own recon. Fill these in as you discover constraints an explorer would otherwise have to re-derive: build quirks, non-obvious module boundaries, flaky tests, environment setup._

-
