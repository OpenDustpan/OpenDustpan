---
version: 2
---

Issue: #2027.

Roadmap: `docs/roadmap.md:466` (Test rolling deploy with mixed version pool.).

## Why

**Persona: operator.** An operator tries to deploy a new release instance by instance while teams keep running tofu apply and pulumi up. Without this test there is no proof that mixed versions stay safe and that old writes stop with a clear error.

The user path this change must make work:

1. **Server start.** Start three opendustpan serve binaries of one installation on PostgreSQL with S3 blobs behind round-robin TLS proxy
2. **E2e.** Run tofu apply and pulumi up loops through proxy while replacing each binary one at a time
3. **API.** Read proxy record and instance list to confirm each instance served each tool with zero failed commands

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Old binary write refused during mixed version pool | Server answers error code schema.binary-below-minimum with a clear version message. No write commits. Loop retries on another instance. Check with `GET /api/server/instances`. | `GET /api/server/instances` |
| Request hits draining instance during replace | Proxy health check marks the draining instance unhealthy. Requests go to the next healthy instance. Check with `curl -k https://<proxy>/health/ready`. | `curl -k https://<proxy>/health/ready` |

Evidence for the claims above:

- A mixed version test target exists on main. Proof: (read only) `Makefile`, line 309
- Main defines the schema below-minimum error code. Proof: (read only) `internal/errcode/catalog_schema.go`, line 8
- Main checks the drain period in health. Proof: (read only) `pkg/dustpan/health.go`, line 510 (func checkDrainPeriod)
- Main lists server instances. Proof: route table or API description for ANY /api/server/instances

## What Changes

- The rolling deploy step with three binaries behind a round-robin TLS proxy, and the loops of `tofu apply` and `pulumi up` with no failed command, are on main. This change extends `e2e/ha_rolling_test.go` with two checks: the error code `schema.binary-below-minimum` on a refused write, and a read of `GET /api/server/instances` that lists three instances.
- Add no new server logic.

Out of scope:

- Version rows and minimum version check on write path
- Graceful drain with 503 and claim release
- Helm chart replicas and real contract migrations
- Docs page with measured recovery figures

Depends on:

- rolling-deploy-version-minimum-check
- rolling-deploy-graceful-drain-release
- `rolling-deploy-graceful-drain-release` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- Operator must find which instance served each request from proxy record; record format must stay stable and machine readable.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- health checks the drain period ((read only) `pkg/dustpan/health.go`, line 510 (func checkDrainPeriod))
- store test refuses version zero in the journal suite ((read only) `pkg/store/storetest/journal.go`, line 210 (func testJournalRefusesVersionZero))
- store test refuses a bad version in the portable stage suite ((read only) `pkg/store/storetest/portable.go`, line 279 (func testPortableStageRefusesVersion))
- the rolling deploy step runs a loop of `tofu apply` and `pulumi up` while each instance is replaced (`e2e/ha_rolling_test.go:195 (func haRollingDeploy)`)
- the step replaces one instance with a binary of a newer build (`e2e/ha_rolling_test.go:148 (func haRoll)`)
- the step checks that each old instance answers 503 on a write and leaves the pool (`e2e/ha_rolling_test.go:344 (func haCheckOldInstances)`)
- the mixed-version matrix runs the current binary with the binary of each of the last 2 tags (`e2e/ha_rolling_test.go:417 (func haMixedVersionMatrix)`)
- the high availability suite runs the step as "5 rolling deploy" ((read only) `e2e/ha_test.go`, line 118 (func runHighAvailability))
- job service defines tofu apply steps ((read only) `pkg/dustpan/jobservice.go`, line 858 (func tofuApplySteps))

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does this change add server logic for version checks or drain? No. This part adds no server logic. Version refusal and drain ship in rolling-deploy-version-minimum-check and rolling-deploy-graceful-drain-release.
- What pool and load does the rolling deploy step use? Three serve binaries behind a round-robin TLS proxy, with tofu apply and pulumi up loops. Contract migration is faked by a test-only minimum-version row write inside the test setup, with no server code.
- How do refused old-binary writes fit zero failed commands? No. Refused writes are retried inside the loop. Zero failed commands means every CLI command exits 0 after retry, not zero refused writes.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
