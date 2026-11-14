---
version: 1
---

Issue: #1185, #1197.

## Problem

As a Developer I run stacks and workspaces and need to know when a unit drifts and when its drift clears, without polling each unit.

## Evidence

- Drift store Record returns a Transition Proof: pkg/store/internal/driftstore/driftstore.go:158:func Record(ctx context.Context, tx Tx, r *drift.Result) (drift.Transition, error)
- Transition carries At Unit JobID From To Proof: pkg/store/internal/driftstore/driftstore.go:188:	return drift.Transition{At: r.CheckedAt, Unit: r.Unit, JobID: r.JobID, From: from, To: r.Status()}, nil
- Drift conformance suite runs over sqlite and postgres stores Proof: pkg/store/storetest/drift.go:33:// RunDrift runs the conformance suite of package drifttest over the stores of

## Fix boundary

- Two notify catalog types unit.drifted warning and unit.drift-cleared info with reference rows
- Drift store Record appends the notice in the result transaction with unit and job dedupe key and summary of unit reference and count only
- Repeat same-state result sends nothing; refused, failed or canceled record sends nothing
- Drift package writes drifted, in-sync and unknown to inventory states in its own fenced write through NEW MarkUnknown
- Docs line on the notification page

Out of scope:

- Outbox, deliveries and dispatcher core of #1172
- Drift schedule and auto-dedrift
- Sink rotation and chat inbound

## Check

- Add notice cases to NEW file pkg/drift/drifttest/notice.go for first drifted, first clean, repeat quiet, failed, canceled, refused and stale fence Check: TestRecordAppendsDriftedOnce fails on main
- Add states and NEW MarkUnknown conformance in pkg/store/storetest/drift.go Check: TestStatesWriteDriftedInSyncUnknown on sqlite and postgres fails on main
- Add NEW types unit.drifted warning and unit.drift-cleared info to the single notify.Types catalog in pkg/notify Check: rg unit.drifted shows the two types in pkg/notify
- Append the notice with row lock in Record in pkg/store/internal/driftstore Check: Record appends one notice on both adapters
- Add NEW MarkUnknown in pkg/drift and call it from the state version write path with its fence Check: state write sets unknown in the same fenced transaction
- Add the drift notice rows to the notification guide at NEW docs/guides/notifications-drift.md Check: docs page renders the drift notice rows
- Run e2e states step on the drift read route with an access token Check: GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/drift with an access token shows drifted then in-sync states
- Run e2e delivery step on the subscription deliveries route with an access token Check: deliveries list shows one drifted delivery; BLOCKED by #1172 outbox

Depends on:

- Drift slice 1 Transition from drift.Store.Record
- BLOCKED by #1172 outbox, deliveries and dispatcher for delivery
- BLOCKED by #1196 states slice for the states writer base

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Record is table-tested on main (`pkg/drift/drift_test.go:301 (func TestTransition)`)
- Drift conformance harness exists on main (`pkg/store/storetest/drift.go:47 (func driftHarness)`)
- Fence type exists on main (`pkg/namespace/namespace.go:178 (func Namespace.Fence)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 145: One notification model with sinks for email, chat and signed webhooks** (not owner-tagged).

- Decisions that apply: 1 (extended), 4 (current), 5 (extended), 7 (current), 8 (current).
- Changed by: ADR 146 extends decision 5; ADR 149 changes decisions 15 and 24; ADR 149 extends decisions 3, 13 and 27; ADR 197 extends decision 5; ADR 203 extends decision 5; ADR 204 extends decision 5; ADR 210 changes; ADR 234 extends decisions 1 and 24; ADR 241 extends decision 5; ADR 248 changes decision 9; ADR 262 extends; ADR 315 extends; ADR 334 changes decisions 13 and 20. Chain to the end: ADR 146, ADR 149, ADR 197, ADR 203, ADR 204, ADR 210, ADR 234, ADR 241, ADR 248, ADR 262, ADR 315, ADR 334, ADR 227, ADR 297, ADR 222, ADR 229, ADR 230, ADR 225, ADR 286, ADR 300, ADR 353, ADR 247, ADR 313, ADR 343, ADR 301, ADR 320, ADR 339, ADR 354, ADR 333, ADR 335, ADR 351, ADR 268, ADR 355, ADR 299, ADR 332, ADR 328, ADR 359, ADR 368.
- Current rule: this ADR, with ADR 146, 149, 197, 203, 204, 210, 234, 241, 248, 262, 315, 334.
- Quote: "One shape. `notify.Notification` has an ID with the prefix `ntf-`, a type from one catalog, a scope (a namespace or `@server`), the object and its stable ID, the subject and the agent that caused it, a severity (`info`,"
- Quote: "No secret in a notification. A notification never holds a secret value, a credential, state, a plan or a log. It holds names, IDs, a summary and links. A reader follows a link to the console, where the actions of the rea"
- Quote: "A notification commits with its change. The store of each domain appends the notifications of a change in the transaction of the change, as it appends the audit entries. A notification never exists for a change that roll"
- Effect: Notice holds names, IDs, count and links with no secret, commits with the result, uses catalog types and one row per dedupe key.
- Conflict: none.

**ADR 263: Drift detection is a plan-only refresh job with a drifted status and an opt-in schedule** (not owner-tagged).

- Decisions that apply: 2 (current), 6 (current), 10 (current), 11 (current).
- Changed by: ADR 354 extends; ADR 358 extends decision 7; ADR 366 changes decision 12. Chain to the end: ADR 354, ADR 358, ADR 366.
- Current rule: this ADR, with ADR 354, 358, 366.
- Quote: "A change to `drifted` sends a notification. It uses the model of ADR 145. The slice that sends it adds the type to the catalog of notification types."
- Quote: "A failed check keeps the last result. A check that fails or is canceled does not change the result or the time of the unit. The failure shows as a failed job. When another job writes a state version of the unit, the status becomes `unknown`. This sta"
- Effect: Change into drifted sends a notice; failed or canceled check keeps the last result and sends nothing.
- Conflict: none.

**ADR 265: A unit shows all its states with one fixed set of words** (not owner-tagged).

- Decisions that apply: 7 (current), 8 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Each state has named writers. The drift package writes `drifted`, `in-sync` and `unknown`. The job model writes `running`, `queued` and `awaiting-approval`. The job model and the `stack` package write `failed` (decision 10). The job model writes `apply-needed` (decision 9). A writer writes a state only in the transaction of its own write, with the `namesp"
- Quote: "`unknown` shows only for a unit with a drift check setting. The unit shows `unknown` until its first drift result. A unit with no drift check setting shows no drift state. `in-sync` shows when the latest drift result has no difference. An empty set of states means that nothing needs attention."
- Effect: Drift package is the sole writer of drifted, in-sync and unknown in its own fenced write.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request. A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation. A feature that a person or a client can see has a route, a setting, a command, a flag, a console page or an error that a user reads. A pull request that changes only internal code needs no page."
- Effect: Notification docs line ships in the same pull request.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Where does the notice write happen ADR 263 decision 6 plus ADR 145 decision 7: in the fenced result transaction of Record.
- What sends on a failed, canceled or refused record ADR 263 decision 11: a failed or canceled check keeps the last result and sends nothing; a refused Record writes nothing.
- How does repeat drifted stay quiet when the key holds the job ID The Transition gate decides; the dedupe key of unit ID and job ID only skips a retried Record of the same job. A repeat drifted check with a new job ID sends nothing because From equals To.
- Who writes the inventory drift states ADR 265 decisions 7 and 8: only the drift package writes drifted, in-sync and unknown, each in the transaction of its own write with the namespace fence.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
