---
version: 2
---

## Why

**Persona: developer.** Developer with existing cloud resources tries to find unmanaged resources and bring them under management, but discovery and codification wait on runs and have no accepted design yet.

Evidence for the claims above:

- RFC 47 waits on runs in milestone 5. Proof: docs/roadmap.md:826 row: [RFC 47](docs/rfc/0047-discovery-and-codification.md) Discovery and codification | runs (milestone 5)

## What Changes

- Record RFC 47 discovery and codification as waiting on runs
- Refine scope, sources and code output only after RFC acceptance

Out of scope:

- Run model, runners and triggers
- Code generation for unmanaged resources in detail (RFC 124)

Depends on:

- none

Builds on (not a change):

- runs (milestone 5)

## Usability

Where the path still costs the person time or surprises them:

- Developer sees plainly that discovery and codification are undecided and wait on runs.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has a test named TestMaintenanceKeepsDiscovery; the pack does not show that it implements discovery. (`pkg/dustpan/maintenance_test.go:257 (TestMaintenanceKeepsDiscovery)`)
- Main has a durable scheduler delay test; the pack does not show that it implements discovery. (`pkg/durable/scheduler_test.go:175 (TestSchedulerWaitsTheFirstDelayBeforeItsFirstClaim)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 13: Separate tool models; shared access control and inventory** (not owner-tagged).

- Decisions that apply: the ADR as a whole.
- Changed by: ADR 19 extends; ADR 41 changes; ADR 150 changes; ADR 247 extends; ADR 265 extends; ADR 337 extends. Chain to the end: ADR 19, ADR 41, ADR 150, ADR 247, ADR 265, ADR 337, ADR 47, ADR 248, ADR 60, ADR 103, ADR 146, ADR 151, ADR 238, ADR 240, ADR 352, ADR 154, ADR 199, ADR 210, ADR 242, ADR 153, ADR 346, ADR 286, ADR 63, ADR 64, ADR 65, ADR 68, ADR 72, ADR 126, ADR 334, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299.
- Current rule: this ADR, with ADR 19, 41, 150, 247, 265, 337.
- Quote: "Each tool keeps its own domain model, state machine and storage schema"
- Effect: A future discovery design keeps tool models separate and uses tool-neutral unit references for any cross-tool list.
- Conflict: none.

**ADR 248: Federation model: installations own namespaces, request forwarding, asynchronous replication and manual failover** (not owner-tagged).

- Decisions that apply: 14 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "Background work runs on the primary installation."
- Effect: A future discovery design runs background work only on the primary installation.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which sources may discovery read, with whose credentials, and which authorization action, sealing and audit record guard a scan?
   - Options: Cloud API scan with read-only credentials; State import only; No scan until runs exist.
   - Provisional decision (owner review 14 Nov): Cloud API scan with read-only credentials, decided in the RFC.
   - Reason: RFC 47 is undecided and waits on runs; no ADR selects sources, credentials, actions or audit. Benchmark: AWS IAM Access Analyzer uses read-only analysis with read-only credentials.
   - Persona impact: Developer sees no new path until the RFC is accepted, so no workflow breaks. Escape hatch: No discovery runs until the RFC is accepted, so no credential is requested or stored.
2. How does discovery behave on a non-primary installation, after a failover-version change and during a restore?
   - Options: Primary installation only; Any member with forwarding; No schedule until RFC acceptance.
   - Provisional decision (owner review 14 Nov): Primary installation only with fence checks, decided in the RFC.
   - Reason: ADR 248 puts background work on the primary installation, but RFC 47 names no schedule or restore handling. Benchmark: Google Cloud Asset Inventory runs discovery centrally while enforcement stays regional.
   - Persona impact: Developer sees no partial schedule on replicas, so behavior stays predictable. Escape hatch: No discovery runs until the RFC is accepted, so no failover or restore conflict can occur.

Questions considered and answered:

- Can one codified output cover both tools? ADR 13 answers it: tool models stay separate, so codification cannot merge Pulumi and OpenTofu models.
- Where does discovery run in a federation? ADR 248 decision 14 answers it: background work of a namespace runs only on its primary installation.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
