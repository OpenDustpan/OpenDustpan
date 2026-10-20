# 265. A unit shows all its states with one fixed set of words

Status: accepted · 2026-10-20. Decisions 1 to 4 are owner decisions. Decisions 5 to 14 are decided by the product manager under the owner's delegation of 20 October 2026. Extends ADR 13. Supersedes in part ADR 213 decision 9: the vocabulary now holds `locked`, derived from the lock flag (decision 5). Accepts RFC 26

## Context

[RFC 26](../rfc/0026-unit-status.md) asks for a fixed, tool-neutral
vocabulary of unit states in the inventory. [ADR 263](0263-drift-detection-is-a-plan-only-refresh-with-an-opt-in-schedule.md)
decision 10 adds the drift statuses `drifted`, `in-sync` and `unknown` and
leaves the wire spelling to RFC 26. [ADR 213](0213-job-tokens-no-change-plans-bulk-calls-forge-connections-and-locks.md)
decision 9 says that a lock is a flag, not a status.

Issue #933 holds the owner decisions 26.1 and 26.2 of 18 October 2026. ADR 222
decision 8 holds the wire value of the approval wait.

## Decision

1. **The state words are one fixed set.** Issue #933 says (26.1, owner,
   2026-10-18): "the status words are: in sync, running, queued, locked,
   waiting for approval, apply needed, drifted, failed, unknown." No tool adds
   a word. Each writer maps its own facts to these words.
2. **A list shows all states of a unit.** Issue #933 says (26.2): "lists show
   all state badges with a tooltip, not one winning status." So the inventory
   gives the set of states of a unit, not one value.
3. **The wire value of "waiting for approval" is `awaiting-approval`.** ADR
   222 decision 8 says: "its wire value stays `awaiting-approval` (owner,
   2026-10-18)". The human word is "approval wait".
4. **The wire values are stable strings.** The acceptance criteria of issue
   #933 say: "The wire values are stable strings, table-tested." A test holds
   them.
5. **`locked` is derived at read time.** The lock stays a flag and the only
   source of `locked`. No stored state holds `locked`. A locked unit keeps its
   other states. The server derives `locked` from the Lock and the ManualLock
   of the same inventory entry that the response carries. The `state=locked`
   filter uses the same function.
6. **The wire values are kebab case.** They are `in-sync`, `running`,
   `queued`, `locked`, `awaiting-approval`, `apply-needed`, `drifted`,
   `failed` and `unknown`. This is the spelling of ADR 263 and of decision 3.
7. **Each state has named writers.** The drift package writes `drifted`,
   `in-sync` and `unknown`. The job model writes `running`, `queued` and
   `awaiting-approval`. The job model and the `stack` package write `failed`
   (decision 10). The job model writes `apply-needed` (decision 9). A writer
   writes a state only in the transaction of its own write, with the
   `namespace.Fence`, and the store checks the fence in the same transaction.
   Only `locked` is derived.
8. **`unknown` shows only for a unit with a drift check setting.** The unit
   shows `unknown` until its first drift result. A unit with no drift check
   setting shows no drift state. `in-sync` shows when the latest drift result
   has no difference. An empty set of states means that nothing needs
   attention.
9. **`apply-needed` has one first source.** The source is a stored plan with
   changes that nobody applied. The case "code ahead of applied" stays with
   RFC 12.
10. **`failed` means that the latest change to end failed.** A change is an
    apply or destroy job, or a Pulumi CLI update of kind update, destroy or
    import. A preview, a refresh, a plan-only job and a drift check are not
    changes, so they do not set `failed`. `failed` is one stored fact for each
    unit with two writers: the job model and the `stack` package. Each writes
    it in the transaction of its own write, with the fence. The latest change
    to end wins: a failed change sets `failed`, and a successful change from
    either source clears it. A Pulumi job that runs `pulumi up` against our API
    gives a job end and an update end for the same change. Both write the same
    fact, so the result is the same whichever comes last. An OpenTofu CLI apply
    outside a job gives no `failed`, because the server sees only the state
    upload. There is no acknowledge action.
11. **The badges have a fixed order.** The order is `failed`,
    `awaiting-approval`, `apply-needed`, `drifted`, `running`, `queued`,
    `locked`, `unknown`, `in-sync`. The order is the order of the badges only.
    It picks no winner. The code holds the order in exactly one place.
12. **A state is `{state, since}` on the wire.** The `states` field is an
    array of objects. Each object has `state` and `since`. There is no
    `detail` field. For `locked`, `since` is the earlier of the time of the
    lock and the time of the manual lock. Later data for one state comes only
    as typed, optional fields that hold IDs only and have a size limit.
13. **The `state` filter is a repeatable query parameter.** A unit matches
    when it has any of the named states, like the reference filter. The filter
    adds to the other filters with AND. The match is exact: the server does
    not normalize a value. The server removes duplicate values. A request has
    at most 9 values, and the limit counts the raw values before the server
    removes duplicates. An unknown value, or more than 9 values, gets status 400,
    and the error names the valid values. `inventory.ParseState` returns the
    sentinel `ErrUnknownState` for an unknown value.
14. **The first slice is `locked`.** It adds the `states` field with
    `locked`, the `state` filter and the badge order. A separate change removes
    the `Entry.Status` field and the `status` field of the API. They are a
    free string that no code sets, and no released console version reads them.

## Consequences

- The console reads the set of states for its unit list and its badges. It
  shows each tool's own words (ADR 12, ADR 251).
- A change to the word set, to a wire value or to the order of the badges
  needs a new ADR.
- This ADR adds the domain type and its test in `pkg/inventory`. No code
  writes a state yet. The follow-up issues build the slices in this order:
  `locked`, the drift states, the job states, and the source of
  `apply-needed`.
