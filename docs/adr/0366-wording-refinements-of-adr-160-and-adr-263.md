# 366. Wording refinements of ADR 160 and ADR 263

Status: accepted · 2026-11-13. Decided by the product manager under ADR 365. Neither edited decision is an owner decision

Changes:

- Changes ADR 160 decision 14: a wording refinement, the audit entry of an API write of a context entry.
- Changes ADR 263 decision 12: a wording refinement, the server reads no state for a drift check.

## Context

ADR 365 lets a wording refinement edit an accepted ADR in place. A second
check of the decisions that apply now compared the rules of two decisions
at a time. It found two sentences that, read alone, contradict other
decisions. In both cases the rest of the ADR gives the scope, and the
sentence does not.

## Decision

1. **ADR 263 decision 12 names the drift check.** It said "The server reads
   no state." Read alone, this contradicts ADR 41 decision 1 ("The server
   may read state"), ADR 49 decision 8 ("The server reads the outputs from
   the state"), ADR 127 decision 4 and ADR 160 decision 7 (the masked state
   read of outputs) and ADR 342 decision 3 (the gates read the state body).
   Decision 12 is about the drift check: its title is "The runner reduces
   the plan to addresses and kinds". It now says "For a drift check, the
   server reads no state."
2. **ADR 160 decision 14 names the context entry.** It said "An API write
   appends its audit entry in the same transaction". Read alone, this
   contradicts ADR 193 decision 5 ("A write of a draft writes no audit
   entry"). ADR 160 is about context entries. It now says "An API write of
   a context entry appends its audit entry in the same transaction".

## Consequences

- No code changes. Each edited ADR has a changelog item and a "Changed by"
  item.
- The check judged 1,994 pairs of rules. These two sentences are its only
  findings that a person must act on.
