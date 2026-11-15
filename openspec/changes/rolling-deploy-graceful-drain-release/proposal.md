---
version: 4
---

Roadmap: `docs/roadmap.md:466` (Drain instances with 503 and release claims.).

## Why

**Persona: operator.** Operator replaces each instance with a newer build while teams run work. Without a clean drain the proxy still sends requests to the leaving instance and background work stalls until a lease expires. This change lets in-flight work drain before the instance leaves the pool and releases claims for takeover.

The user path this change must make work:

1. **Server start.** Roll one instance: send SIGTERM to instance A while CLI loop runs through proxy.
2. **API.** Proxy health check sees 503 on /health/ready and sends new requests to healthy instances.
3. **CLI.** Operator watches instances and backup operations until takeover completes.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Instance draining during rolling deploy | 503 with Retry-After on /health/ready and on new durable starts while draining. | `curl -i https://server/health/ready` |
| Durable start with no worker in pool | 503 with the text that names `--components worker` when no worker remains, per milestone step 8. | `opendustpan backup start` |

Evidence for the claims above:

- A readiness drain test exists on main. Proof: TestReadinessDrains at pkg/dustpan/health_internal_test.go:224
- A backup takeover test after graceful stop exists on main. Proof: TestBackupTakeoverAfterGracefulStop at internal/servertest/backup_test.go:367
- A shutdown-during-write test that leaves a backup for another worker exists on main. Proof: TestShutdownDuringWriteStepLeavesTheBackupForAnotherWorker at pkg/backup/service_durable_test.go:480
- The context pack names no ADR for this change. Proof: context pack field adrs is an empty list

## What Changes

- SIGTERM enters draining: readiness 503 with Retry-After, no new durable claims.
- Bounded wait with the constants of main: the drain of WithDrainPeriod (DefaultDrainPeriod 10 s, at most 15 s), then the shutdown bound shutdownTimeout of 30 s for in-flight requests and the claim release, inside maxStopTime 45 s (pkg/dustpan/dustpan.go:130-142). A `Retry-After` header on the draining 503 of GET /health/ready.
- Release of held durable claims at drain end so another worker takes over with no operator action.

Out of scope:

- Minimum version check that blocks old writes below stored minimum version
- Exit-test rolling deploy step with mixed version pool
- Helm chart replicas and store refusal

Depends on:

- `rolling-deploy-version-minimum-check` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- Operator watches only the readiness code during drain; the guide must give the exact wait and the one next step.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- A readiness drain test exists. (`pkg/dustpan/health_internal_test.go:224 (func TestReadinessDrains)`)
- A backup takeover test after graceful stop exists. (`internal/servertest/backup_test.go:367 (func TestBackupTakeoverAfterGracefulStop)`)
- A shutdown-during-write test that leaves a backup for another worker exists. (`pkg/backup/service_durable_test.go:480 (func TestShutdownDuringWriteStepLeavesTheBackupForAnotherWorker)`)
- An audit shipper lease-release test on shutdown exists. (`pkg/audit/ship_test.go:1292 (func TestShipperReleasesItsLeaseOnShutdown)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- What bounds the drain? The constants of main, PM decision 14 Nov 2026 22:20 (week log): no new drain timer and no internal/instance/drain.go. shutdownTimeout 30 s is the existing bound for in-flight requests and the claim release; DefaultDrainPeriod 10 s and maxStopTime 45 s stay (pkg/dustpan/dustpan.go:130-142, checkDrainPeriod at pkg/dustpan/health.go:493). SIGTERM uses WithDrainPeriod and the existing shutdown path.

- Does this change alter the minimum version check on the write path? No. The split scope keeps the minimum version check in another part; this change only drains and releases claims.
- Does this change add the exit-test rolling deploy step? No. The roadmap scope for this part is drain with readiness 503 and release of claims for takeover only.
- Does any ADR govern graceful drain or claim release? No. The context pack adrs list is empty, so no ADR decision governs graceful drain or claim release.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
