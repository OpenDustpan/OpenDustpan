---
version: 1
---

Issue: #2930.

## Problem

An agent that updates a skill must know which rule applies. CLAUDE.md states the two-week rule. A commit message says the owner exempted skills, but no ADR records it. Provisional decision: the written rule stands until the owner gives the words.

## Evidence

- ADR 7 states the two-week age rule for dependencies. Proof: docs/adr/0007-strict-linting-as-a-gate.md, decision text in the pack excerpt of ADR 7.
- The issue says the owner exception is in a commit message and in no ADR. Proof: Issue 2930 text in the pack, quoting the second commit message of 65e6990c.

## Fix boundary

- Edit the skill line in CLAUDE.md (AGENTS.md is the same file): keep the two-week rule for skills.
- Add one sentence to that line: the claim of an owner exception for skills (commit 65e6990c) is unverified, and the two-week rule applies until the owner gives the words.
- The gh-stack skill is known to be newer than two weeks. The owner decides about it; this change does not pin it.

Out of scope:

- Writing an ADR that changes ADR 7 for skills
- Changing the two-week rule for dependencies
- Pinning the gh-stack skill to the 2026-10-21 commit

## Check

- Write the check: the skill line in CLAUDE.md holds both 'two weeks' and 'unverified'. Check: rg -n 'two weeks.*unverified|unverified.*two weeks' CLAUDE.md finds nothing on main.
- Edit the skill line in CLAUDE.md to keep the two-week rule and add the unverified-claim sentence. Check: The rg command above finds one line in CLAUDE.md.
- Check that no other doc states a skills exception: run rg -n 'two weeks' CLAUDE.md docs and read each hit. Check: Each hit agrees with the written rule.
- A person reads the skill line alone and states the rule. Check: The person states: two weeks, claim unverified. A person's step.
- Run the full gate. Check: make check reports zero findings.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The make target adr-index rebuilds the ADR index. (`Makefile:183 (make target adr-index)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P22)**, Item 20: #2930 two-week rule for agent skills: "the written rule stands (two weeks for skills too) until the owner gives the words; the claim in commit 65e6990c is marked unverified. Reason: the PM cannot decide what the owner said; the written rule is the safe one."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 7: Strict linting is the first gate** (not owner-tagged).

- Decisions that apply: the ADR as a whole.
- Changed by: ADR 275 changes. Chain to the end: ADR 275, ADR 323, ADR 325.
- Current rule: this ADR, with ADR 275.
- Quote: "Dependencies are pinned exactly, and new versions are adopted only once they
  are at least two weeks old."
- Effect: The change keeps this rule for skills and adds no exception.
- Conflict: none.

**ADR 275: The TypeScript client is one published package** (owner-tagged: decision 4).

- Decisions that apply: 4 (current).
- Changed by: ADR 323 changes decision 8. Chain to the end: ADR 323, ADR 325.
- Current rule: this ADR, with ADR 323.
- Quote: "**Packages of the `@opendustpan` scope are exempt from the two-week age rule of ADR 7.** A consumer still pins such a package to an exact version. A package of any other scope keeps the age rule."
- Effect: This owner decision exempts only the @opendustpan scope. It does not cover skills. The change does not alter its words.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: task "Run the full gate." is not confirmed by the agent alone: end it with one fixed phrase: "Confirmed by: CI (...)", "Confirmed by: the driver's gates." or "A person's step."; a code check fails: `CLAUDE.md` is named by the design or a task but is not in the touches of the stack plan.

Questions considered and answered:

- May an ADR record the exception for skills now? No. Owner decision, 13 Nov 2026 (queue P22): the written rule stands until the owner gives the words.
- Does ADR 275 exempt skills? No. Decision 4 exempts only packages of the @opendustpan scope.
- Must the change pin gh-stack now? No. Owner decision, 13 Nov 2026 (queue P22) says only that the written rule stands. The pin is for the owner.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
