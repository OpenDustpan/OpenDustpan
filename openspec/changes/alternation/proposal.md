---
version: 1
---

Roadmap: `docs/roadmap.md:448` (Alternation).

## Why

**Persona: operator.** An Operator runs two or more serve instances behind one load balancer so the team survives a lost instance and deploys instance by instance while the Pulumi CLI and OpenTofu keep working.

The user path this change must make work:

1. **Server start.** Start three opendustpan serve instances on one PostgreSQL database and one S3-compatible bucket
2. **E2e.** Start round-robin TLS proxy with health checks on /health/ready in front of the three instances
3. **CLI.** Run full Pulumi scenario through the proxy
4. **CLI.** Run full OpenTofu scenario through the proxy
5. **API.** Read proxy record to confirm each instance served each tool

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Instance not ready leaves the pool until /health/ready answers 200 | Proxy health check fails; the instance leaves the pool. Run `curl -k https://proxy/health/ready` to see which instance is unhealthy, then fix that instance and run the scenarios again. | `curl -k https://proxy/health/ready` |
| Pulumi CLI fails with TLS certificate error against the proxy | CLI refuses the proxy certificate. Install the proxy CA in the CLI trust store, then run `pulumi preview` again through the proxy. | `pulumi preview` |
| OpenTofu fails with TLS certificate error against the proxy | OpenTofu refuses the proxy certificate. Install the proxy CA in the system trust store that OpenTofu reads, then run `tofu plan` again through the proxy. | `tofu plan` |

Evidence for the claims above:

- HA e2e files exist on main Proof: e2e/ha_kill_test.go, e2e/ha_outage_test.go, e2e/ha_restore_test.go, e2e/ha_noworker_test.go
- High-availability guides exist on main Proof: docs/guides/high-availability.md, docs/guides/architecture-and-sizing.md
- Instance package exists on main Proof: internal/instance
- Keyword evidence lists e2e, guides, and code for Instance Proof: e2e/e2e_masked_test.go, e2e/e2e_test.go, docs/guides/high-availability.md, internal/instance

## What Changes

- Start three instances of one installation behind a round-robin TLS proxy in e2e
- Run full Pulumi and OpenTofu scenarios through the proxy
- Assert proxy forwarding record shows each instance served requests of each tool with no sticky session

Out of scope:

- Kill during work, kill during backup, rolling deploy, schedules, database outage including 503 with Retry-After, no-worker steps
- Replication between installations, promotion and forwarding
- SQLite with more than one instance

Depends on:

- ci-service-container-s3-suite
- signed-file-urls-registry-need
- `ci-service-container-s3-suite` (order of the milestone review)
- `signed-file-urls-registry-need` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- Operator copies proxy and three-instance commands from the guide; a missing proxy log entry costs time to map a request to an instance

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- e2e helper drives the OpenTofu CLI (`e2e/tool.go:110 (func OpenTofu)`)
- A test in the e2e suite lists scenario runs and leaves out runs of other tests (`e2e/contract_unit_test.go:201 (func TestScenarioRunsLeavesOutTheRunsOfOtherTests)`)
- A test checks that a policy change of another instance applies at once (`pkg/accesstoken/accesstoken_test.go:1511 (func TestPolicyChangeOfAnotherInstanceAppliesAtOnce)`)
- A test checks that a deleted blob key is not served by the cache (`pkg/blob/cache/cache_test.go:186 (func TestDeletedKeyIsNotServed)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 280: Milestone 7: high availability on one database** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 5 (extended).
- Changed by: ADR 281 extends decision 5; ADR 286 changes; ADR 298 extends decision 4; ADR 302 extends. Chain to the end: ADR 281, ADR 286, ADR 298, ADR 302, ADR 340, ADR 337.
- Current rule: this ADR, with ADR 281, 286, 298, 302.
- Quote: "Scope: one installation, active-active (owner). Milestone 7 makes one installation run active-active: two or more `opendustpan serve` instances behind one load balancer, on one PostgreSQL database and one S3-compatible object store."
- Effect: Follows active-active scope and minimum topology: two or more instances on one PostgreSQL database and one S3-compatible store; out of scope items stay out.
- Conflict: none.

**ADR 72: Scale out on one database with durable operations** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 73 changes decision 7; ADR 75 extends decision 10; ADR 103 changes decision 6; ADR 126 changes decision 6; ADR 151 extends decision 5; ADR 166 extends decision 1; ADR 240 extends decision 6; ADR 249 extends decision 11; ADR 280 extends; ADR 298 extends. Chain to the end: ADR 73, ADR 75, ADR 103, ADR 126, ADR 151, ADR 166, ADR 240, ADR 249, ADR 280, ADR 298, ADR 322, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 146, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 129, ADR 154, ADR 210, ADR 217, ADR 220, ADR 286, ADR 297, ADR 354, ADR 281, ADR 302, ADR 337, ADR 325, ADR 326, ADR 362.
- Current rule: this ADR, with ADR 73, 75, 103, 126, 151, 166, 240, 249, 280, 298.
- Quote: "Another instance can claim an operation only after its expiry."
- Effect: Relies on existing lease and fence behavior for shared state; this change adds no new durable operation and no store change.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does alternation need to pass on SQLite with more than one instance? No. ADR 280 decision 1 keeps SQLite single-instance out of scope and requires PostgreSQL and one S3-compatible store for more than one instance.
- What proves that each instance serves each tool? The roadmap step requires the full milestone 1 Pulumi scenario and the full milestone 2 OpenTofu scenario through the proxy, with the proxy record showing each instance served requests of each tool.
- Does this change add a response header or modify internal/instance? No. Attribution uses only the proxy forwarding record. No server module change is part of this change.
- Where is 503 with Retry-After during alternation specified? database-outage covers it. ADR 280 decision 4 requires it; this change names that behavior as out of scope.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
