## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add the failing e2e step for kill during backup of at least 500 MB in e2e/ha_kill_test.go (NEW func TestHaKillBackupTakeover). Check: TestHaKillBackupTakeover fails on main; calls ANY /api/server/backups/{} with an access token.
- [ ] 1.2 Add failing tests for survivor stop, database-gap renewal and resume of a partial copy in pkg/backup/service_durable_guard_test.go (NEW func TestHaKillBackupSurvivorCrash). Check: TestHaKillBackupSurvivorCrash fails on main; reclaim read through ANY /api/server/backups/{} with an access token.

## 2. Wiring and docs

- [ ] 2.1 Add a throttled blob seed helper for the 500 MB backup in e2e/ha_kill_test.go (NEW func seedThrottledBlobs). The helper seeds blobs through the proxy and starts the backup on ANY /api/server/backups/{} with an access token. Check: The fixture seeds 500 MB and the copy still runs at kill time.
- [ ] 2.2 Expose the NEW field expiryCount on the durable operation with the same name on backup status and backup check output, in internal/cli/client/backup.go and internal/cli/client/backup_check.go. Check: Operation shape and ANY /api/server/backups/{} and ANY /api/server/backups/{}/check return the same NEW field expiryCount with an access token.
- [ ] 2.3 Document the measured takeover time with its date and no service level wording. Check: docs/guides/high-availability.md lists the figure with date.

## 3. Definition of done

- [ ] 3.1 Run HA step 3: kill, succeeded within the lease period plus 60 seconds, backup check complete, one expiry. Check: `make e2e-ha` passes the step on PostgreSQL with an S3 target, using ANY /api/server/backups/{} and its check route with an access token.
- [ ] 3.2 Run the usability check from the docs page alone. Check: The Operator completes the path from the docs page and each error names one command.
- [ ] 3.3 Run the full gate. Check: `make check` reports zero findings.
