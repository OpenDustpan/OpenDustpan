---
rfc: 26
title: Unit status in the inventory
status: discussing
created: 2026-10-10
---

# RFC 26. Unit status in the inventory

Status: discussing. The owner decided the words and the badge rule on 18
October 2026. [ADR 265](../adr/0265-a-unit-shows-all-its-states-with-one-fixed-set-of-words.md)
records them. This text holds the open questions, with options and a
recommendation for each. No code writes a state yet.

## Problem

[ADR 13](../adr/0013-separate-tool-models-shared-read-model-and-access.md)'s
inventory has a status for each unit, with no defined values. `Entry.Status`
is a free string, and no tool sets it. Competitors show a small vocabulary.
Terramate has healthy, failed and drifted. Harness has "apply needed". HCP
Terraform and env zero lead with "needs attention". The console's unit list
and its badges depend on this vocabulary.

## Decided

- Words (owner, 26.1): in sync, running, queued, locked, waiting for
  approval, apply needed, drifted, failed, unknown.
- Lists (owner, 26.2): all states of a unit show as badges with a tooltip.
  There is no winning status, so no precedence rule picks one.
- The wire value of "waiting for approval" is `awaiting-approval`. The human
  word is "approval wait".
- [ADR 213](../adr/0213-job-tokens-no-change-plans-bulk-calls-forge-connections-and-locks.md)
  decision 9: a lock is a flag, not a status.
- [ADR 263](../adr/0263-drift-detection-is-a-plan-only-refresh-with-an-opt-in-schedule.md):
  the drift statuses are `drifted`, `in-sync` and `unknown`. The drift
  result of a unit sets them.

## Options and proposal

### 1. How does `locked` meet ADR 213 decision 9?

The word `locked` is in decision 26.1, and the issue wants a locked and
drifted unit to return both states. ADR 213 decision 9 says that the
vocabulary holds no `locked` value.

- A. `states` holds `locked`, built at read time from the lock and the manual
  lock. The flags stay the source. Nothing stores a lock state.
- B. `states` holds no `locked`. The entry keeps the flags, and the console
  draws the lock badge from them. A filter by `locked` reads the flags.
- C. Store `locked` as a state. This contradicts ADR 213 decision 9.

Recommendation: A. The badge set is one list for the console and the filter
(decision 26.2). The flag stays the only source, so ADR 213 decision 9 holds
for storage. The owner needs to confirm that it holds for the read model too.

### 2. What are the wire values?

Recommendation: kebab case, like ADR 263 and `awaiting-approval`:
`in-sync`, `running`, `queued`, `locked`, `awaiting-approval`,
`apply-needed`, `drifted`, `failed`, `unknown`. The values are stable
strings. A table test holds them. The console and the docs show each tool's
own words (ADR 12, ADR 251).

### 3. Who sets each state?

| State | Set by | Source today |
| --- | --- | --- |
| `locked` | the inventory | the lock and the manual lock |
| `drifted`, `in-sync`, `unknown` | the drift package | the drift result (ADR 263) |
| `running`, `queued`, `awaiting-approval` | the job model | the jobs of the unit, when the job store writes them |
| `failed` | the job model | the latest job of the unit |
| `apply-needed` | no source | see question 5 |

Options: A: the inventory stores the set of states of a unit, and each owner
writes its own states in the transaction of its own write (ADR 13). B: the
inventory builds the set at read time from the tables of each owner.

Recommendation: A for the states that change on a write, B only for `locked`,
which the inventory already holds. A stored set makes the filter one indexed
read. B makes every list read join the job and drift tables. Each state has
one owner, and no owner writes the state of another.

### 4. When do `in-sync` and `unknown` show?

If every unit without a drift setting showed `unknown`, every unit would carry a
badge.

- A. A unit with a drift check setting shows `unknown` until its first
  result. A unit without the setting shows no drift state.
- B. Every unit always shows one of the three drift states.

Recommendation: A. `in-sync` shows when the latest result has no difference.
An empty set of states means that nothing needs attention.

### 5. Where does `apply-needed` come from?

A unit with no runner has no job to apply. RFC 12 names "the main branch is
ahead of what the runner applied" as the case.

Recommendation: keep the word in the vocabulary, with no source. The job
model sets it when a person approves a plan with changes and nobody
applies it, or when
the code location is ahead of the applied version. A separate slice builds
the source. Until then the state never appears.

### 6. What does `failed` mean, and when does it clear?

ADR 263 decision 11 says that a failed drift check shows as a failed job and
does not change the drift result.

Recommendation: `failed` means that the latest change job of the unit ended
failed. A failed drift check does not set it. It clears when a later change
job succeeds or when a person acknowledges it.

### 7. In what order are the badges?

Recommendation: the response lists the states in a fixed order, by the
severity that a person needs first: `failed`, `awaiting-approval`,
`apply-needed`, `drifted`, `running`, `queued`, `locked`, `unknown`,
`in-sync`. The order is only the order of the badges. It picks no winner.

### 8. What shape does a state have on the wire?

Recommendation: `states` is an array of objects. Each has `state`, `since`
and a `detail` object with fixed keys for that state. For `locked`, the keys
are the holder, the time and the operation. The API gives no text for the
tooltip, because the console writes it in the language of the person.

Open: the existing `lock` and `manualLock` fields stay or go. Recommendation:
they stay, because they carry the flags that ADR 213 decision 9 keeps, and
`states` repeats them for the badge.

### 9. How does a filter work?

Recommendation: the query parameter `state` can repeat. A unit matches when it
has any of the named states. An unknown value answers 400 and names the valid
values. This is the style of the existing label filter.

### 10. Which slice comes first?

Recommendation: `locked`, as soon as question 1 has an answer. The server
knows it today, so the slice needs no new writer. `drifted` waits for the
drift store. The job states wait for the job store.

## Open questions

Questions 1 to 10 above need the answer of the owner. Question 1 is the one
that blocks the first slice.

## Outcome

Accepted in part as [ADR 213](../adr/0213-job-tokens-no-change-plans-bulk-calls-forge-connections-and-locks.md)
decision 9 on 2026-10-18: a lock is a flag, not a status. Accepted in part as
[ADR 265](../adr/0265-a-unit-shows-all-its-states-with-one-fixed-set-of-words.md)
on 2026-10-20: the words, the badge rule and the wire value `awaiting-approval`.
The rest stays open.
