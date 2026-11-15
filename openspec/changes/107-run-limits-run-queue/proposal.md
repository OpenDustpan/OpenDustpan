---
version: 2
---

## Why

**Persona: operator.** As an Operator I run many stacks and workspaces on few runners. I need limits that queue excess runs, so one team cannot use all capacity.

The user path this change must make work:

1. **API.** Operator sets an installation cap and a per-namespace run limit through the settings resource of the installation and of the namespace.
2. **CLI.** Developer starts a run when limits are full. The run waits in the queue.
3. **Docs page.** Operator reads how the queue and the limits work.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Run stays queued past the limit | The server keeps the run queued and shows its queue position. The exact error code waits on the run model. | `make e2e` |

Evidence for the claims above:

- Main has a held-unit queue for job starts. It is related but is not a run limit. Proof: pkg/job/service_test.go:928 TestStartQueuesWhenHeld
- Main has a durable scheduler with a first-claim delay. It is related but is not a run queue. Proof: pkg/durable/scheduler_test.go:175 TestSchedulerWaitsTheFirstDelayBeforeItsFirstClaim

## What Changes

- Stored run limits and a visible run queue wait on the run model of milestone 5.
- Limits use the settings resource, and a raise of a limit needs a fresh authentication.
- Queue claims use leases and fencing tokens for slot counting.

Out of scope:

- Run options and re-runs
- Job kinds for state operations
- Step results and scanners
- Drift detection

Depends on:

- none

Builds on (not a change):

- runs (milestone 5)

## Usability

Where the path still costs the person time or surprises them:

- Queue position must show in the console and the API.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- A job start queues when the unit is held. This is a hold queue, not a run limit. (`pkg/job/service_test.go:928 (TestStartQueuesWhenHeld)`)
- The durable scheduler waits a first delay before its first claim. This is timing, not queue order. (`pkg/durable/scheduler_test.go:175 (TestSchedulerWaitsTheFirstDelayBeforeItsFirstClaim)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (owner-tagged: decision 7).

- Decisions that apply: 7 (current).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365.
- Quote: "One settings API (owner decision 2026-10-18)."
- Effect: Run limits are settings of the installation and of each namespace on the one settings resource. A raise of a limit needs a fresh authentication.
- Conflict: none.

**ADR 248: Federation model: installations own namespaces, request forwarding, asynchronous replication and manual failover** (not owner-tagged).

- Decisions that apply: 6 (current), 14 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "The fence stays. Each write in a namespace carries a `namespace.Fence`, and the store checks it in the same transaction, as ADR 4 decides."
- Quote: "Background work runs on the primary installation."
- Effect: Queue writes carry the fence. Queued work and limit counting run on the primary installation only.
- Conflict: none.

**ADR 72: Scale out on one database with durable operations** (not owner-tagged).

- Decisions that apply: 2 (current), 4 (current).
- Changed by: ADR 73 changes decision 7; ADR 75 extends decision 10; ADR 103 changes decision 6; ADR 126 changes decision 6; ADR 151 extends decision 5; ADR 166 extends decision 1; ADR 240 extends decision 6; ADR 249 extends decision 11; ADR 280 extends; ADR 298 extends. Chain to the end: ADR 73, ADR 75, ADR 103, ADR 126, ADR 151, ADR 166, ADR 240, ADR 249, ADR 280, ADR 298, ADR 322, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 146, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 129, ADR 154, ADR 210, ADR 217, ADR 220, ADR 286, ADR 297, ADR 354, ADR 281, ADR 302, ADR 337, ADR 325, ADR 326, ADR 362.
- Current rule: this ADR, with ADR 73, 75, 103, 126, 151, 166, 240, 249, 280, 298.
- Quote: "A lease per operation (L1). Each claimed operation has an expiry on the database clock."
- Quote: "A write-path installation check."
- Effect: Queue claims and slot counting use durable operations with a lease per operation. Every queue write carries the fence and the maintenance check in the same transaction.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What is the scope of run limits?
   - Options: Per namespace only; Per namespace and per unit; Installation and namespace, then per unit and per runner pool.
   - Provisional decision (owner review 14 Nov): Installation cap plus per-namespace limit first, then per-unit limits.
   - Reason: The pack proves no run model, no route and no limit setting. Benchmark: AWS: per-queue concurrency limits with FIFO order and visible queue position.
   - Persona impact: Operator sets one limit per namespace and one cap for the installation. Escape hatch: Operator sets limits high to disable queueing.
   - Owner-tagged ADRs involved: ADR 212 (their words do not change).
2. What order does the run queue use, and what starves on a shared pool?
   - Options: Strict FIFO per namespace; FIFO with lanes for plans and applies.
   - Provisional decision (owner review 14 Nov): Strict FIFO per namespace first.
   - Reason: ADR 72 sets leases but no queue order. Benchmark: Google: queued work starts in order received, with priority as a later explicit step.
   - Persona impact: FIFO is plain to explain. Escape hatch: A namespace administrator starts one queued run by hand.
3. How does a limit change, and what guards a raise?
   - Options: Settings resource, fresh authentication to raise; Separate route with its own action.
   - Provisional decision (owner review 14 Nov): Settings resource with fresh authentication to raise, and an audit entry for each change.
   - Reason: ADR 212 decision 7 sets one settings resource. Benchmark: SOC 2: each security-weakening change needs a fresh authentication and an audit record.
   - Persona impact: Operator uses the settings API they know. Escape hatch: A lower limit needs no fresh step and applies at once.
   - Owner-tagged ADRs involved: ADR 212 (their words do not change).
4. How do restart, takeover, lease expiry and a lowered limit treat running and queued runs, and who wins the last slot?
   - Options: Slot frees at lease expiry, with a fencing token check; Slot frees at once on runner disconnect.
   - Provisional decision (owner review 14 Nov): Count a slot only while a live lease holds it. The last slot is claimed in one transaction with the fencing token. A lowered limit stops new starts and leaves running runs alone.
   - Reason: ADR 72 decision 2 sets leases but no slot rule. Benchmark: Google: a lease holder that dies loses its slot after expiry and one worker takes it.
   - Persona impact: Two starters cannot take the last slot. Escape hatch: Operator cancels a stuck queued run by its ID.
5. What happens to a queued run when its unit is held, frozen, deleted, suspended or restoring?
   - Options: Wait through all of them; Fail closed on delete, suspend and restore, wait through short holds.
   - Provisional decision (owner review 14 Nov): Fail closed on delete, suspend and restore with a named error. Wait through holds.
   - Reason: No ADR in the pack sets this. Benchmark: AWS: a queued request that its target cannot run fails closed with a named error.
   - Persona impact: Developer sees one named error. Escape hatch: A namespace administrator requeues the run.

Questions considered and answered:

- Where does queued run work execute and what guards each queue write? ADR 72 decisions 2 and 4 and ADR 248 decisions 6 and 14: leases with fencing tokens on the primary installation, with the fence and the maintenance check on each write.
- Through what route do limits change? ADR 212 decision 7: the settings resource of the scope. No separate limits route.
- What are the limit values and the queue order? No ADR sets them. They stay as open questions for the RFC.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
