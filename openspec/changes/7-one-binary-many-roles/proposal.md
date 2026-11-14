---
version: 2
---

## Why

**Persona: operator.** As an Operator I run one server process on a laptop and replicas of one server process in production, with no extra processes to learn.

The user path this change must make work:

1. **Server start.** Run one server process with all built-in work from the install guide

## What Changes

- No code change now. This coarse change records that one binary with shared storage per installation stays until a runner need forces a new role.

Out of scope:

- Optional modules and the capabilities document
- Runner hosting and runner protocol work
- Kubernetes operator packaging

Depends on:

- none

Builds on (not a change):

- runners-and-remote-runs (RFC 6) - a second role that needs its own process

## Usability

Where the path still costs the person time or surprises them:

- The operator runs one process by default and learns no new flag in this change.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main refuses a restore when the binary is below the schema minimum. (`pkg/backup/restore_cancel_test.go:145 (TestRestoreCheckRefusesABinaryBelowTheMinimum)`)
- Main schedules claimed durable work with a first delay before its first claim. (`pkg/durable/scheduler_test.go:175 (TestSchedulerWaitsTheFirstDelayBeforeItsFirstClaim)`)
- Main watches worker processes and moves no result on a skewed process clock. (`pkg/dustpan/workerwatch_internal_test.go:345 (TestSkewedProcessClockMovesNoResult)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 72: Scale out on one database with durable operations** (not owner-tagged).

- Decisions that apply: 2 (current), 4 (current).
- Changed by: ADR 73 changes decision 7; ADR 75 extends decision 10; ADR 103 changes decision 6; ADR 126 changes decision 6; ADR 151 extends decision 5; ADR 166 extends decision 1; ADR 240 extends decision 6; ADR 249 extends decision 11; ADR 280 extends; ADR 298 extends. Chain to the end: ADR 73, ADR 75, ADR 103, ADR 126, ADR 151, ADR 166, ADR 240, ADR 249, ADR 280, ADR 298, ADR 322, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 146, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 129, ADR 154, ADR 210, ADR 217, ADR 220, ADR 286, ADR 297, ADR 354, ADR 281, ADR 302, ADR 337, ADR 325, ADR 326, ADR 362.
- Current rule: this ADR, with ADR 73, 75, 103, 126, 151, 166, 240, 249, 280, 298.
- Quote: "A lease per operation (L1). Each claimed operation has an expiry on the database clock."
- Quote: "A write-path installation check. Every write transaction reads the installation state in the same statement that reads its namespace fence."
- Effect: A future role split keeps leased durable operations and the write-path installation check. This coarse change adds no role and changes neither rule.
- Conflict: none.

**ADR 248: Federation model: installations own namespaces, request forwarding, asynchronous replication and manual failover** (not owner-tagged).

- Decisions that apply: 14 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "Background work runs on the primary installation."
- Effect: Background work of a namespace stays on its primary installation. This coarse change moves no work to another process.
- Conflict: none.

**ADR 305: Our own Kubernetes operator, modeled on the Pulumi Kubernetes Operator** (owner-tagged (all decisions)).

- Decisions that apply: 9 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The operator ships with the coordinator (owner, 18 October 2026)."
- Effect: A Kubernetes operator stays out of the core server binary. This coarse change adds no operator code to the binary.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does main already run separate server roles or processes? No. The pack lists tests for leased durable work, restore schema checks, and worker watches. The pack holds no ADR text that names server roles or a start flag, so no role split is specified here.
- What does this coarse change build? No code. It records the hold position only. A later RFC names the module registry, the start flag, and the role set.
- Which ADR decisions bound a future role split? ADR 72 decisions 2 and 4 bound leases and writes. ADR 248 decision 14 bounds where background work runs. ADR 305 decision 9 keeps the operator out of the core binary.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
