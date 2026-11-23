---
version: 5
---

## Why

**Persona: operator.** Operator upgrades an installation that holds real stacks and workspaces and must prove that data, secrets and locks survive the new binary with no manual step.

The user path this change must make work:

1. **Docs page.** Operator reads the upgrade test docs page and finds the pinned old commit and the backup step
2. **E2e.** Operator runs the upgrade fixture against SQLite and PostgreSQL with the real Pulumi CLI and OpenTofu
3. **Server start.** Operator starts the new server binary on the same database and key file
4. **CLI.** Developer runs one stack write and one workspace write against the upgraded server

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Fixture object does not survive the upgrade | Upgrade fixture fails: object missing or digest differs after upgrade. The log names the stable ID. | `make upgrade-test` |
| Old binary restarted on upgraded database exits | Old binary refused to start: schema is newer than the binary. Restore the pre-upgrade backup into an empty database, then start the older binary. | `make upgrade-test` |

Evidence for the claims above:

- Main has a schema upgrade test from released tags. Proof: internal/store/postgres/upgrade_schema_test.go:23 (func TestUpgradeSchemaFromReleasedTags)
- Main has a rolling upgrade test. Proof: pkg/dustpan/workerwatch_internal_test.go:240 (func TestRollingUpgradeGivesNoRefusal)
- Main serves the upgrade-check route. Proof: ANY/GET /api/dustpan/v1/upgrade-check in the route table of the pack
- The upgrade test order covers real CLI data, secrets, held lock and the empty-database case. Proof: ADR 33 decision 7, ADR 72 decision 10 and ADR 75 decision 4 in the pack

## What Changes

- Build the old binary from the NEW file `e2e/upgrade/pinned-commit.txt` set after agent-shapes-before-first-tag and key-encryption-key-rotation merge with the release-build target, then from the latest three minor tags, and seed real stacks, workspaces, secrets, principals of each kind, grants, agent-block audit entries, one finished key rotation and one held lock
- Stop the old binary, start the new binary on the same database and key file, and verify survival with the NEW tests `TestUpgradeRealDataSqlite` and `TestUpgradeRealDataPostgres`
- Check old-binary refusal as a process exit with a log line, keep the suite outside `make check` with a CI job, and write the operator upgrade docs page whose commands run in the test

Out of scope:

- Creating a tag or publishing a release
- Downgrade support beyond refusal and backup restore
- New migration framework or baseline edits

Depends on:

- agent-shapes-before-first-tag
- key-encryption-key-rotation
- release-artifacts-binary-archives

## Usability

Where the path still costs the person time or surprises them:

- Operator finds the pinned commit and the backup step on one docs page.
- Fixture log names the stable ID of each missing object.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Store suite checks schema upgrade from released tags. (`internal/store/postgres/upgrade_schema_test.go:23 (func TestUpgradeSchemaFromReleasedTags)`)
- Worker watch suite checks that a rolling upgrade gives no refusal. (`pkg/dustpan/workerwatch_internal_test.go:240 (func TestRollingUpgradeGivesNoRefusal)`)
- Postgres store deletes a held manual lock. (`internal/store/postgres/manual_locks.go:429 (func Store.deleteHeldLock)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 33: Upgrade checks** (not owner-tagged).

- Decisions that apply: 2 (current), 6 (current), 7 (extended).
- Changed by: ADR 39 supersedes decision 3; ADR 72 changes decision 5; ADR 72 extends decision 7; ADR 72 supersedes decision 1. Chain to the end: ADR 39, ADR 72, ADR 237, ADR 238, ADR 245, ADR 73, ADR 75, ADR 103, ADR 126, ADR 151, ADR 166, ADR 240, ADR 249, ADR 280, ADR 298, ADR 252, ADR 285, ADR 324, ADR 315, ADR 356, ADR 295, ADR 322, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 146, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 129, ADR 154, ADR 210, ADR 217, ADR 220.
- Current rule: this ADR, with ADR 39, 72.
- Quote: "Upgrade test. `make upgrade-test` and a CI job build the newest `v*` tag that is an ancestor of HEAD."
- Quote: "The test checks that the data, the secrets and the held lock survive."
- Quote: "When no tag exists, the test runs the empty-database case of ADR 21."
- Quote: "The binary refuses to start on a database schema that is newer than it knows."
- Effect: The change builds the real-data path of decision 7, keeps the refusal of decision 2 and the restore path of decision 6.
- Conflict: none.

**ADR 72: Scale out on one database with durable operations** (not owner-tagged).

- Decisions that apply: 10 (extended).
- Changed by: ADR 73 changes decision 7; ADR 75 extends decision 10; ADR 103 changes decision 6; ADR 126 changes decision 6; ADR 151 extends decision 5; ADR 166 extends decision 1; ADR 240 extends decision 6; ADR 249 extends decision 11; ADR 280 extends; ADR 298 extends. Chain to the end: ADR 73, ADR 75, ADR 103, ADR 126, ADR 151, ADR 166, ADR 240, ADR 249, ADR 280, ADR 298, ADR 322, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 146, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 129, ADR 154, ADR 210, ADR 217, ADR 220, ADR 286, ADR 297, ADR 354, ADR 281, ADR 302, ADR 337, ADR 325, ADR 326, ADR 362.
- Current rule: this ADR, with ADR 73, 75, 103, 126, 151, 166, 240, 249, 280, 298.
- Quote: "Upgrades can skip releases. The migration runner applies every embedded migration that the database does not record, in order."
- Effect: The change tests a direct start from an old release with no release in between.
- Conflict: none.

**ADR 75: Release cadence and security support** (not owner-tagged).

- Decisions that apply: 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Supported releases. The latest three minor versions are the supported releases of ADR 72 decision 10."
- Effect: After tags exist, the fixture runs from each of the latest three minor versions.
- Conflict: none.

**ADR 73: Until 1.0, a change can break everything except the tool-compatible APIs** (not owner-tagged).

- Decisions that apply: 5 (current).
- Changed by: ADR 322 changes decision 6; ADR 322 extends decision 4. Chain to the end: ADR 322, ADR 325, ADR 326, ADR 362, ADR 363.
- Current rule: this ADR, with ADR 322.
- Quote: "Released shapes keep their upgrade path."
- Effect: Shapes of a tag keep their upgrade path; the pre-tag pin has no guarantee.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Must the test use the real CLIs and check secrets and the held lock? Yes. ADR 33 decision 7 and ADR 72 decision 10 order it.
- From which old releases must the test start? One pinned commit until the first tag, then the latest three minor versions (ADR 75 decision 4).
- Must the test prove downgrade? No. ADR 33 decision 6 gives restore of the backup, and decision 2 gives the refusal.
- Does the change add a route? No. The existing upgrade-check route stays as it is.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
