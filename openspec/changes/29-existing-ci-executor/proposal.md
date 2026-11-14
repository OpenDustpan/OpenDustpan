---
version: 1
---

## Why

**Persona: developer.** A developer runs plans and applies in the CI the team already uses and wants that history in OpenDustpan without moving to hosted runners.

The user path this change must make work:

1. **CLI.** Run the Pulumi CLI or OpenTofu from existing CI against the server
2. **API.** Read unit history through the tool-neutral API

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| No new error in this change. | The server answers the tool APIs as today. No new error code is added. | `pulumi stack ls` |

Evidence for the claims above:

- RFC 29 waits on runs in milestone 5 and is not shipped. Proof: docs/roadmap.md:818 | [RFC 29](rfc/0029-existing-ci.md) Existing CI as the executor | runs (milestone 5)

## What Changes

- No behavior change. The change records RFC 29 as waiting on the run model and keeps one refinement task.

Out of scope:

- The run and job model from milestone 5
- Runner pools and claims
- Schedules, triggers and approvals for runs
- C# and Java clients

Depends on:

- runs in milestone 5

## Usability

Where the path still costs the person time or surprises them:

- The Developer path stays as today until the RFC is accepted.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Runner executor phases are covered by an executor test (`pkg/runner/executor_test.go:127 (TestExecutorPhases)`)
- Durable scheduler waits the first delay before its first claim (`pkg/durable/scheduler_test.go:175 (TestSchedulerWaitsTheFirstDelayBeforeItsFirstClaim)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 305: Our own Kubernetes operator, modeled on the Pulumi Kubernetes Operator** (owner-tagged (all decisions)).

- Decisions that apply: 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The work runs through OpenDustpan jobs and gates (owner). The operator does not run a tool."
- Effect: An external CI executor follows the same pattern: it requests work through the REST API and the server does not run the tool.
- Conflict: none.

**ADR 248: Federation model: installations own namespaces, request forwarding, asynchronous replication and manual failover** (not owner-tagged).

- Decisions that apply: 6 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "The fence stays. Each write in a namespace carries a `namespace.Fence`, and the store checks it in the same transaction, as ADR 4 decides."
- Effect: A write by existing CI carries the namespace fence and fails closed when the failover version changes.
- Conflict: none.

**ADR 72: Scale out on one database with durable operations** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 73 changes decision 7; ADR 75 extends decision 10; ADR 103 changes decision 6; ADR 126 changes decision 6; ADR 151 extends decision 5; ADR 166 extends decision 1; ADR 240 extends decision 6; ADR 249 extends decision 11; ADR 280 extends; ADR 298 extends. Chain to the end: ADR 73, ADR 75, ADR 103, ADR 126, ADR 151, ADR 166, ADR 240, ADR 249, ADR 280, ADR 298, ADR 322, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 146, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 129, ADR 154, ADR 210, ADR 217, ADR 220, ADR 286, ADR 297, ADR 354, ADR 281, ADR 302, ADR 337, ADR 325, ADR 326, ADR 362.
- Current rule: this ADR, with ADR 73, 75, 103, 126, 151, 166, 240, 249, 280, 298.
- Quote: "A lease per operation (L1). Each claimed operation has an expiry on the database clock."
- Effect: A job claim by existing CI is a lease with expiry. A dead CI run loses the claim after expiry.
- Conflict: none.

## Open questions

1. What is the contract between existing CI and OpenDustpan: does CI claim a job, only write state through tool APIs, or post to a report endpoint? Decides: owner.
   - Options: Existing CI claims a job through the jobs API and reports status; Server only reads state that existing CI writes through the tool APIs; Existing CI posts status through a narrow report endpoint without claiming.
   - Recommended: Existing CI claims a job through the jobs API and reports status Reason: All work stays in jobs and gates, and code and cloud credentials stay outside the server.

Not spec-ready: question 1: the benchmark names none of Google, AWS, PCI DSS, SOC 2, ISO 27001.

Questions considered and answered:

- Does any accepted ADR already decide existing CI as an executor? No. RFC 29 stays open and waits on runs in milestone 5. ADR 305 decision 4 covers only the operator pattern.
- Which fence and lease rules apply to an external executor? ADR 248 decision 6 keeps the fence on every namespace write. ADR 72 decision 2 makes a claim a lease that expires.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
