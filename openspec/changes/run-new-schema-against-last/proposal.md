---
version: 1
---

Issue: #743.

## Why

**Persona: operator.** Operator upgrades one instance at a time. Operator needs proof that the new schema still serves the last 2 release binaries on one database, so a rolling deploy never breaks a running CLI.

The user path this change must make work:

1. **E2e.** CI builds the new binary and the last 2 ancestor v* tags, starts them on one database, and drives real CLIs against each old binary
2. **CLI.** Operator runs the same matrix locally before merge
3. **Docs page.** Operator reads the matrix guide with the tag list, the zero-old-binary note, and the refusal error

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Old binary refuses its writes on the new schema. | The old binary read a higher minimum row and refused its own writes with error code schema.binary-below-minimum. Rebuild the binaries and run the NEW `make mixed-version-test` again. | `make mixed-version-test` |
| No ancestor v* tag exists before the first tag. | The test reports zero old binaries and runs the new-binary steps. Run `git fetch --tags` with full history before the run. | `git fetch --tags` |
| Tag list is empty because the clone is shallow or tags were not fetched. | The harness fails loudly and names the missing tags. Fetch all tags with full history, then run the NEW `make mixed-version-test` again. | `make mixed-version-test` |

Evidence for the claims above:

- The e2e upgrade helper exists. Proof: paths lists e2e/upgrade.go with exists true
- A mixed-version e2e test exists on main. Proof: TestMixedVersion in e2e/upgrade_mixed_test.go:74
- The refusal error code exists on main. Proof: schema.binary-below-minimum in internal/errcode/catalog_schema.go:8
- The refusal path has a unit test on main. Proof: TestRestoreCheckRefusesABinaryBelowTheMinimum in pkg/backup/restore_cancel_test.go:145

## What Changes

- Mixed-version e2e harness over e2e/upgrade.go for the last 2 ancestor v* tags on SQLite and PostgreSQL
- NEW make target and NEW CI job next to the upgrade test
- Self-check that a binary below the minimum is refused with schema.binary-below-minimum
- Explicit zero-old-binary report before the first tag, never a silent skip

Out of scope:

- Version rows and minimum check logic itself
- Rolling deploy exit-test step with live proxy alternation
- Database outage and schedule behavior
- Helm chart and docs for several replicas

Depends on:

- rolling-deploy-version-minimum-check

## Usability

Where the path still costs the person time or surprises them:

- Operator sees one CI job with a clear report: which tags ran, or an explicit zero-old-binary note before the first tag.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- A mixed-version e2e test exists that covers the new-plus-one-old shape without tag discovery, 1-tag and 0-tag reports, or dual-dialect matrix. (`e2e/upgrade_mixed_test.go:74 (func TestMixedVersion)`)
- The binary-below-minimum error code exists. (`internal/errcode/catalog_schema.go:8 (error code schema.binary-below-minimum)`)
- A unit test proves refusal of a binary below the minimum at restore check. (`pkg/backup/restore_cancel_test.go:145 (func TestRestoreCheckRefusesABinaryBelowTheMinimum)`)
- A rolling-upgrade unit test exists. (`pkg/dustpan/workerwatch_internal_test.go:240 (func TestRollingUpgradeGivesNoRefusal)`)
- PostgreSQL test URL env var exists. (`internal/servertest/servertest.go:15 (env var DUSTPAN_TEST_POSTGRES_URL)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 72: Scale out on one database with durable operations** (not owner-tagged).

- Decisions that apply: 10 (extended), 11 (extended).
- Changed by: ADR 73 changes decision 7; ADR 75 extends decision 10; ADR 103 changes decision 6; ADR 126 changes decision 6; ADR 151 extends decision 5; ADR 166 extends decision 1; ADR 240 extends decision 6; ADR 249 extends decision 11; ADR 280 extends; ADR 298 extends. Chain to the end: ADR 73, ADR 75, ADR 103, ADR 126, ADR 151, ADR 166, ADR 240, ADR 249, ADR 280, ADR 298, ADR 322, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 146, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 129, ADR 154, ADR 210, ADR 217, ADR 220, ADR 286, ADR 297, ADR 354, ADR 281, ADR 302, ADR 337, ADR 325, ADR 326, ADR 362.
- Current rule: this ADR, with ADR 73, 75, 103, 126, 151, 166, 240, 249, 280, 298.
- Quote: "Rolling deploys: expand and contract with K = 2. Binaries of release R-2 to R can run together on one schema."
- Quote: "Upgrades can skip releases. The migration runner applies every embedded migration that the database does not record, in order."
- Effect: The matrix proves decision 11 on the current baseline and decision 10 across tags. It changes no expand or contract rule.
- Conflict: none.

**ADR 75: Release cadence and security support** (not owner-tagged).

- Decisions that apply: 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Supported releases. The latest three minor versions are the supported releases of ADR 72 decision 10. The full upgrade test starts from each of them."
- Effect: The matrix uses the last 2 ancestor tags as the K=2 rolling subset. The full upgrade test from each of the 3 supported releases stays separate.
- Conflict: none.

**ADR 322: No migrations or compatibility code before the first tag** (owner-tagged: decision 1, 2).

- Decisions that apply: 1 (changed), 2 (current).
- Changed by: ADR 325 changes decision 1; ADR 325 changes decision 5; ADR 325 changes decision 6; ADR 326 extends decision 1; ADR 362 changes decision 5. Chain to the end: ADR 325, ADR 326, ADR 362, ADR 363.
- Current rule: this ADR, with ADR 325, 326, 362.
- Quote: "One baseline for each dialect until the first tag (owner). Each dialect keeps one numbered migration file, the baseline `pkg/store/<dialect>/migrations/<NNNN>_baseline.sql`."
- Quote: "No migration or compatibility code for untagged shapes (owner). Until the first tag, no pull request writes migration, upgrade, compatibility, alias or data-conversion code for a schema or an API shape that no tag shipped."
- Effect: Before the first tag the test runs the edited baseline only, with no numbered migration and no compatibility code.
- Conflict: none.

**ADR 249: Components of the server process, and a start flag that selects them** (not owner-tagged).

- Decisions that apply: 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Migrations. Each process migrates at start, under the advisory lock, whatever its components. The start order of the processes does not matter."
- Effect: Each binary in the matrix migrates at start under the advisory lock. Either start order works.
- Conflict: none.

**ADR 280: Milestone 7: high availability on one database** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current).
- Changed by: ADR 281 extends decision 5; ADR 286 changes; ADR 298 extends decision 4; ADR 302 extends. Chain to the end: ADR 281, ADR 286, ADR 298, ADR 302, ADR 340, ADR 337, ADR 368, ADR 369, ADR 370.
- Current rule: this ADR, with ADR 281, 286, 298, 302.
- Quote: "Scope: one installation, active-active (owner). Milestone 7 makes one installation run active-active: two or more `opendustpan serve` instances behind one load balancer, on one PostgreSQL database and one S3-compatible object store."
- Effect: SQLite with more than one concurrent instance stays out of scope. The SQLite matrix runs binaries one at a time on one file.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Which old binaries must the matrix run? ADR 72 decision 11 sets K = 2, so binaries R-2 to R run together. The job builds the last 2 ancestor v* tags plus the new binary. ADR 75 decision 4 keeps the wider 3-release upgrade test separate.
- In which order do the binaries start? ADR 249 decision 4 says each process migrates at start under the advisory lock and start order does not matter. The harness tests new-first and one reverse-order start on PostgreSQL.
- What happens before the first tag when no v* tag exists? The issue states the answer: before the first tag the test runs with no old binary and reports that fact. ADR 322 decision 1 covers why there is no old schema to test. A shallow clone with no tags fails loudly instead of using the zero-tag report.
- Which Pulumi CLI and OpenTofu versions drive the old binaries? The current e2e tool versions drive all binaries, because the issue names real Pulumi CLI and OpenTofu steps and no per-tag pin exists. The log records the versions used.
- Who owns the minimum-row write and the refusal? The rolling-deploy-version-minimum-check change owns the version row write and the below-minimum refusal. This change only runs that existing refusal with error code schema.binary-below-minimum in a self-check and adds no server write path. This change stays blocked until rolling-deploy-version-minimum-check lands because the harness reads the minimum row that it writes.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
