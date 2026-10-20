# 265. A unit shows all its states with one fixed set of words

Status: accepted · 2026-10-20. Records decisions 26.1 and 26.2 of the owner of 18 October 2026 and the wire value of the approval wait in the policy model. Extends ADR 13. Meets ADR 213 decision 9 for the word `locked`

## Context

[RFC 26](../rfc/0026-unit-status.md) asks for a fixed, tool-neutral
vocabulary of unit states in the inventory. [ADR 263](0263-drift-detection-is-a-plan-only-refresh-with-an-opt-in-schedule.md)
decision 10 adds the drift statuses `drifted`, `in-sync` and `unknown` and
leaves the wire spelling to RFC 26. [ADR 213](0213-job-tokens-no-change-plans-bulk-calls-forge-connections-and-locks.md)
decision 9 says that a lock is a flag, not a status.

The owner decided on 18 October 2026, and the policy model fixed one wire
value.

## Decision

1. **The state words are one fixed set (owner decision 26.1).** A unit state
   is one of: in sync, running, queued, locked, waiting for approval, apply needed,
   drifted, failed, unknown. No tool adds a word. A tool maps its own states
   to these words.
2. **A list shows all states of a unit (owner decision 26.2).** A list of
   units shows every state that applies, each as a badge with a tooltip. It
   does not show one winning status. So the inventory gives the set of states
   of a unit, not one value.
3. **The wire value of "waiting for approval" is `awaiting-approval`.** The
   human word is "approval wait".
4. **The wire values are stable strings.** A test holds them.

## Not decided here

RFC 26 holds the rest: how `locked` meets ADR 213 decision 9, the other wire
values, who sets each state, when `in-sync` and `unknown` show, the order of
the badges, the data of each state, and the filters. The spelling of the
drift statuses in `pkg/drift` is kebab case, like decision 3. The other
values wait for the owner.

## Consequences

- The console reads the set of states for its unit list and its badges. It
  shows each tool's own words (ADR 12, ADR 251).
- No code writes a state yet. The domain type and its test are in
  `pkg/inventory`.
