---
rfc: 26
title: Unit status in the inventory
status: accepted (ADRs 213, 265)
created: 2026-10-10
---

# RFC 26. Unit status in the inventory

Status: accepted (ADRs 213 and 265). [ADR 265](../adr/0265-a-unit-shows-all-its-states-with-one-fixed-set-of-words.md)
records the decisions: the words and the badge rule (owner, 2026-10-18), and
the answers to all ten questions below (product manager, under the owner's
delegation of 20 October 2026). No question is open. No code writes a state
yet.

## Problem

[ADR 13](../adr/0013-separate-tool-models-shared-read-model-and-access.md)'s
inventory has a status for each unit, with no defined values. `Entry.Status`
is a free string, and no tool sets it. Competitors show a small vocabulary.
Terramate has healthy, failed and drifted. Harness has "apply needed". HCP
Terraform and env0 put "needs attention" first. The console's unit list and
its badges depend on this vocabulary.

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

## Questions and answers

The product manager decided each answer under the owner's delegation of 20
October 2026. ADR 265 holds the text of each decision.

### 1. How does `locked` meet ADR 213 decision 9?

The word `locked` is in decision 26.1, and the issue wants a locked and
drifted unit to return both states. ADR 213 decision 9 says that the
vocabulary holds no `locked` value.

- A. `states` holds `locked`, built at read time from the lock and the manual
  lock. The flags stay the source. Nothing stores a lock state.
- B. `states` holds no `locked`. The entry keeps the flags, and the console
  draws the lock badge from them. A filter by `locked` reads the flags.
- C. Store `locked` as a state. This contradicts ADR 213 decision 9.

Answer: A (ADR 265 decision 5). Option A supersedes in part the vocabulary
sentence of ADR 213 decision 9 and keeps the flag as the only source. The badge set is one list for the console and
the filter (decision 26.2). The flag stays the only source, so no stored state
holds `locked`.

### 2. What are the wire values?

Answer: kebab case, like ADR 263 and `awaiting-approval`: `in-sync`,
`running`, `queued`, `locked`, `awaiting-approval`, `apply-needed`,
`drifted`, `failed`, `unknown` (ADR 265 decision 6). The values are stable
strings. A table test holds them. The console and the docs show each tool's
own words (ADR 12, ADR 251).

### 3. Who sets each state?

| State | Set by | Source today |
| --- | --- | --- |
| `locked` | the inventory | the lock and the manual lock |
| `drifted`, `in-sync`, `unknown` | the drift package | the drift result (ADR 263) |
| `running`, `queued`, `awaiting-approval` | the job model | the jobs of the unit, when the job store writes them |
| `failed` | the job model, and the `stack` package for a Pulumi CLI update | the latest change to end: an apply or destroy job, or an update of kind update, destroy or import |
| `apply-needed` | the job model | a stored plan with changes that nobody applied (question 5) |

Options: A: the inventory stores the set of states of a unit, and each writer
writes its own states in the transaction of its own write (ADR 13). B: the
inventory builds the set at read time from the tables of each writer.

Answer: A for the states that change on a write, and B only for `locked`
(ADR 265 decision 7). A stored set makes the filter one indexed read. B makes
every list read join the job and drift tables. Each write of a state carries
the `namespace.Fence`, and the store checks it in the same transaction. Each
state has named writers. A writer writes a state only in the transaction of
its own write, with the namespace fence.

### 4. When do `in-sync` and `unknown` show?

If every unit without a drift setting showed `unknown`, every unit would carry a
badge.

- A. A unit with a drift check setting shows `unknown` until its first
  result. A unit without the setting shows no drift state.
- B. Every unit always shows one of the three drift states.

Answer: A (ADR 265 decision 8). `in-sync` shows when the latest result has no
difference. An empty set of states means that nothing needs attention.

### 5. Where does `apply-needed` come from?

A unit with no runner has no job to apply. RFC 12 names "the main branch is
ahead of what the runner applied" as one case.

Answer: `apply-needed` has one first source: a stored plan with changes that
nobody applied (ADR 265 decision 9). The case "code ahead of applied" stays
with RFC 12.

### 6. What does `failed` mean, and when does it clear?

ADR 263 decision 11 says that a failed drift check shows as a failed job and
does not change the drift result.

Answer: `failed` means that the latest change to end failed (ADR 265 decision
10). A change is an apply or destroy job, or a Pulumi CLI update of kind
update, destroy or import. A preview, a refresh, a plan-only job and a drift
check are not changes. `failed` is one stored fact for each unit with two
writers: the job model and the `stack` package. Each writes it in the
transaction of its own write, with the fence. The latest change to end wins: a
failed change sets `failed`, and a successful change from either source clears
it. A Pulumi job that runs `pulumi up` against our API gives a job end and an
update end for the same change. Both write the same fact, so the result is the
same whichever comes last. An OpenTofu CLI apply outside a job gives no
`failed`, because the server sees only the state upload. There is no
acknowledge action.

### 7. In what order are the badges?

Answer: the response lists the states in a fixed order (ADR 265 decision 11):
`failed`, `awaiting-approval`, `apply-needed`, `drifted`, `running`,
`queued`, `locked`, `unknown`, `in-sync`. The order is only the order of the
badges. It picks no winner. The code holds the order in exactly one place.

### 8. What shape does a state have on the wire?

Answer: `states` is an array of objects. Each has `state` and `since`
(ADR 265 decision 12). There is no `detail` field. For `locked`, `since` is the
earlier of the time of the lock and the time of the manual lock. Later data
for one state comes only as typed, optional fields that hold IDs only and
have a size limit. The API gives no text for the tooltip, because the console
writes it in the language of the person.

The `lock` and `manualLock` fields stay. They carry the flags that ADR 213
decision 9 keeps, and they hold the holder and the operation. `states`
repeats the fact for the badge.

### 9. How does a filter work?

Answer: the query parameter `state` can repeat (ADR 265 decision 13). A unit
matches when it has any of the named states. This is the style of the
reference filter. The label filter is different: a unit must have all the
named labels. The `state` filter adds to the other filters with AND. The
match is exact, with no normalization. The server removes duplicate values. A
request has at most 9 values, and the limit counts the raw values before the
server removes duplicates. An unknown value answers 400 and names the valid
values. `inventory.ParseState` returns the sentinel `ErrUnknownState`.

### 10. Which slice comes first?

Answer: `locked` (ADR 265 decision 14). The server knows it today, so the slice
needs no new writer. `drifted` waits for the drift store. The job states wait
for the job store. A separate change removes `Entry.Status` and the API field
`status`, because no code sets them and no released console version reads
them.

## Open questions

None.

## Outcome

Accepted in part as [ADR 213](../adr/0213-job-tokens-no-change-plans-bulk-calls-forge-connections-and-locks.md)
decision 9 on 2026-10-18: a lock is a flag, not a status. Accepted as
[ADR 265](../adr/0265-a-unit-shows-all-its-states-with-one-fixed-set-of-words.md)
on 2026-10-20: the words, the badge rule, the wire values and the answers to
questions 1 to 10. ADR 265 supersedes in part the vocabulary sentence of ADR 213
decision 9. The slices that build the code stay open as issues.
