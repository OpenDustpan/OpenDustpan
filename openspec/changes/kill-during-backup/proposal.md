---
version: 2
---

Roadmap: `docs/roadmap.md:455` (Kill during a backup).

## Why

**Persona: operator.** An Operator runs several serve processes on one PostgreSQL database and one S3-compatible bucket so a lost process never loses a backup. The Operator starts a large backup, loses the process that holds it, and still gets a complete backup with no manual action.

The user path this change must make work:

1. **API.** Seed at least 500 MB of blobs and start a backup through the proxy.
2. **API.** Read the backup status on ANY /api/server/backups/{} with an access token to find the holder process.
3. **E2e.** Send SIGKILL to the holder while the copy runs.
4. **API.** Watch the backup end succeeded within the lease period plus 60 seconds.
5. **CLI.** Run backup check and confirm complete and one expiry.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Backup operation never reaches succeeded after the holder was killed. | Backup stays running past the lease period plus 60 seconds. Read the backup status again. | `GET /api/server/backups/{id}` with an access token |
| Backup check fails after takeover. | Backup check reports incomplete or corrupt. Run backup check again after the backup ends. | `opendustpan backup check <backup-id>` |
| Backup ends failed after the survivor also stops. | Backup ends failed after takeover. Read the backup status reason and start a new backup. | `opendustpan backup check <backup-id>` |

Evidence for the claims above:

- HA kill tests exist for this milestone. Proof: e2e/ha_kill_test.go
- The backup and restore guide documents backup operation and check. Proof: docs/guides/backup-restore-upgrade.md
- The backup domain package holds the backup logic. Proof: pkg/backup
- The exit test kills the holder and requires succeeded within lease period + 60 seconds with one expiry. Proof: docs/roadmap.md:455

## What Changes

- HA e2e step proves backup takeover after SIGKILL of the holder within the lease period plus 60 seconds.
- Backup check proves the taken-over backup complete.
- Durable operation shows the NEW field expiryCount with the same name on backup status and backup check output.

Out of scope:

- Alternation of Pulumi and OpenTofu traffic across instances.
- Rolling deploy with mixed binary versions.
- Schedule single-runner ticks.
- Database outage 503 behavior.
- No-worker 503 behavior.
- Helm chart replicas and store refusal.
- Attempt-scoped target fencing, owned by backup-fence-target-writes-attempt.
- Blob delete waiting while a backup runs, owned by W5 under RFC 105 option 3.

Depends on:

- backup-fence-target-writes-attempt
- `alternation` (order of the milestone review)
- `backup-fence-target-writes-attempt` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- Operator finds the holder in backup status with no log search.
- Operator sees one expiry and a complete check with no extra search.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has a backup status response type in the CLI client. (`internal/cli/client/backup.go:115 (func backupFlags.register)`)
- Main has a backup check CLI path. (`internal/cli/client/backup_check.go:67 (func BackupCheck)`)
- Main has a worker watch lease period helper. (`pkg/dustpan/workerwatch.go:189 (func workerWatch.leasePeriod)`)
- Main has a test that fails an operation that ended without a worker. (`pkg/backup/service_durable_guard_test.go:93 (func TestRunFailsAnOperationThatEndedWithoutAWorker)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 280: Milestone 7: high availability on one database** (owner-tagged (all decisions)).

- Decisions that apply: 7 (current), 8 (current).
- Changed by: ADR 281 extends decision 5; ADR 286 changes; ADR 298 extends decision 4; ADR 302 extends. Chain to the end: ADR 281, ADR 286, ADR 298, ADR 302, ADR 340, ADR 337, ADR 368, ADR 369, ADR 370.
- Current rule: this ADR, with ADR 281, 286, 298, 302.
- Quote: "Blob deletes wait while a backup runs: RFC 105 option 3 (owner). The project accepts the proposal of RFC 105: option 3, in its form with no clock, for each delete that the RFC lists as in scope."
- Quote: "The lease stays at 30 seconds (owner). The lease period of a durable operation stays fixed at 30 seconds. No flag and no setting changes it."
- Effect: The step keeps the fixed 30 second lease with no flag. Takeover waits the lease period plus 60 seconds.
- Conflict: none.

**ADR 72: Scale out on one database with durable operations** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 73 changes decision 7; ADR 75 extends decision 10; ADR 103 changes decision 6; ADR 126 changes decision 6; ADR 151 extends decision 5; ADR 166 extends decision 1; ADR 240 extends decision 6; ADR 249 extends decision 11; ADR 280 extends; ADR 298 extends. Chain to the end: ADR 73, ADR 75, ADR 103, ADR 126, ADR 151, ADR 166, ADR 240, ADR 249, ADR 280, ADR 298, ADR 322, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 146, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 129, ADR 154, ADR 210, ADR 217, ADR 220, ADR 286, ADR 297, ADR 354, ADR 281, ADR 302, ADR 337, ADR 325, ADR 326, ADR 362.
- Current rule: this ADR, with ADR 73, 75, 103, 126, 151, 166, 240, 249, 280, 298.
- Quote: "A lease per operation (L1). Each claimed operation has an expiry on the database clock. The worker that runs it extends the expiry. Each claim has a fencing token that increases on each takeover."
- Effect: The backup claim expires on the database clock, another worker claims it after expiry with a higher fencing token, and each step write checks the token.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Where does the one-expiry count live and which API exposes it?
   - Options: Add NEW field expiryCount to the durable operation shape and show the same name on backup status and backup check output; Keep the count only in the operation row with no API field.
   - Provisional decision (owner review 14 Nov): Add NEW field expiryCount to the durable operation shape and show the same name on backup status and backup check output.
   - Reason: A visible count lets the suite assert one expiry without a direct database read, and the docs record the measured takeover time with its date. Benchmark: AWS documents backup completion with a retry count and a completion timestamp measured by the service.
   - Persona impact: Operator sees one expiry in backup status with no extra search. Escape hatch: Read the backup status reason and start a new backup when the count is unclear.
   - Owner-tagged ADRs involved: ADR 280 (their words do not change).

Questions considered and answered:

- Does another worker take over the backup after SIGKILL? Yes. ADR 72 decision 2: another worker claims after expiry, with a new fencing token checked in each step transaction.
- What is the lease period for the takeover wait? ADR 280 decision 8: fixed 30 seconds, no flag, no setting. The bound is the lease period plus 60 seconds.
- Where does the test read the holder and the result? From backup status on ANY /api/server/backups/{} and from ANY /api/server/backups/{}/check. Both routes are on main.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
