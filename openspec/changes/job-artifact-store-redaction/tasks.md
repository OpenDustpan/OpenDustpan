## 1. Spec words (depends on the archive of add-job-service-routes-start-1)

- [ ] 1.1 Edit `openspec/changes/job-artifact-store-redaction/specs/job-actions/spec.md` as written: `job:cancel`, `job:approve`, the start action for a confirmation, `unit:redact`, `job.not-found`. Spec text only.
  - Check: `npx @fission-ai/openspec@1.15.0 validate job-artifact-store-redaction`
  -md`

## 2. The SQL redaction log (can land first as its own pull request)

- [ ] 2.1 Add the tables `redactions` and `redaction_targets` to the baseline of both dialects, in name order, and the queries in `internal/store/sqlite/queries.sql` and `internal/store/postgres/queries.sql`, then run `make generate`. NEW tables, so this task names the registry files `pkg/backup/backup.go` and `pkg/backup/portable_tables.go` and puts both tables on the portable list.
  - Check: `go test -race ./pkg/backup/... ./pkg/store/...` and `sqlc diff`
  - Confirmed by: CI
  - OpenDustpan: `internal/store/sqlite/migrations/`, `internal/store/postgres/migrations/`, `internal/store/sqlite/queries.sql`, `internal/store/postgres/queries.sql`, `pkg/backup/backup.go`, `pkg/backup/portable_tables.go`
- [ ] 2.2 SECURITY. Implement `history.RedactionLog` (`pkg/history/redaction.go:195`) in both store adapters (`internal/store/sqlite/`, `internal/store/postgres/`) and run `historytest.RunRedactions` (`internal/history/historytest/redactions.go:25`) against SQLite and PostgreSQL.
  - Check: `go test -race ./pkg/store/... -run Redactions -timeout 60m`
  - Confirmed by: CI
  - OpenDustpan: `internal/store/jobstore/`, `internal/store/sqlite/`, `internal/store/postgres/`

## 3. The job record and the store port

- [ ] 3.1 Add the NEW field `StepRecord.Earlier` and the NEW field `LogRef.Replaces` in `pkg/job/step.go` (next to `Log *LogRef`, `pkg/job/step.go:108`), and the NEW function `LogVersionOf` in `pkg/job/step.go`. Make `logOfRow` (`internal/store/jobstore/record.go:747`) and `pkg/job/service_redact.go` call `LogVersionOf`.
  - Check: table test `TestLogVersionOf` (fails on main: no such function), and the unit test that a retry finds the older version
  - Confirmed by: CI
  - OpenDustpan: `pkg/job/step.go`, `pkg/job/service_redact.go`, `internal/store/jobstore/record.go`
- [ ] 3.2 SECURITY. Add the NEW port `RedactionStore` with the NEW methods `PutLogVersion` and `RedactItem` in the NEW file `pkg/job/redactionstore.go`, with the conformance cases in `pkg/job/jobtest` first, then both SQL adapters.
  - Check: `go test -race ./pkg/job/... ./pkg/store/... -timeout 60m`
  - Confirmed by: CI
  - OpenDustpan: `pkg/job/redactionstore.go`, `pkg/job/jobtest/`, `internal/store/jobstore/tx.go`

## 4. The adapter

- [ ] 4.1 SECURITY. Write the NEW constructor `job.NewArtifactStore` and `ReplaceLog` in the NEW file `pkg/job/artifactstore.go`, in the recorder order. Run `historytest.RunRedactor` with `JobArtifactSituations` and `JobLogSituations` against the real SQL adapters. `ReplaceLog` must also finish a retry: when the older version already has a next version, the retry skips the `From` check that answers `ErrConflict` and only redacts the older version. Test: the `Redact` step fails once after the new version exists; the retry redacts the older version, and the older version no longer holds the secret.
  - Check: `go test -race ./pkg/job/... ./internal/history/... -timeout 60m`
  - Confirmed by: CI
  - OpenDustpan: `pkg/job/artifactstore.go`, `internal/history/historytest/`
- [ ] 4.2 SECURITY. Add the hostile table for each layer of the design, in the NEW file `pkg/job/artifactstore_test.go`, each case with a test that fails when its guard is removed. A check that looks redundant keeps a comment that says it is a defense-in-depth layer and names the other layers it does not rely on.
  - Check: `go test -race ./pkg/job/...`
  - Confirmed by: CI
  - OpenDustpan: `pkg/job/artifactstore_test.go`
- [ ] 4.3 SECURITY. Add the hostile tests of the boundary layer and the service layer, each sent to that layer alone: in NEW file `internal/api/jobs/redact_hostile_test.go` an empty match, a match over the size limit and a version ID that is not an ID, each answers 400 with no state change; in NEW file `pkg/job/redact_hostile_test.go` a version of another job, a version of another namespace, a running step and the current version, each refused before any blob read. Each case has a test that fails when its guard is removed.
  - Check: `go test -race ./internal/api/jobs/... ./pkg/job/... -run Hostile`
  - Confirmed by: CI
  - OpenDustpan: `internal/api/jobs/redact_hostile_test.go`, `pkg/job/redact_hostile_test.go`
- [ ] 4.4 SECURITY. Add the hostile tests of the store layer in NEW file `internal/store/jobstore/redact_hostile_test.go`: a failed audit insert in the middle of `RedactItem` leaves no row changed, a fence of an old holder answers `namespace.ErrFenced` (`pkg/namespace/namespace.go:39`) with nothing written, and a test reads every audit field, the record JSON and the error text for the match text and finds none. Each case has a test that fails when its guard is removed.
  - Check: `go test -race ./internal/store/jobstore/... -run Hostile -timeout 60m`
  - Confirmed by: CI
  - OpenDustpan: `internal/store/jobstore/redact_hostile_test.go`

## 5. The action family

- [ ] 5.1 SECURITY. Add `identity.ActionUnitRedact: FamilyData` to `families`. This task edits `pkg/identity/identity.go` only if the action list needs it, and it names `pkg/access/access.go` as the registry file. Change `pkg/access/access_test.go`: take `ActionUnitRedact` out of the "no family" list, and add role tests (`admin` holds it, `viewer`, `writer`, `maintainer`, `planner` do not).
  - Check: `go test -race ./pkg/access/...`
  - Confirmed by: CI
  - OpenDustpan: `pkg/access/access.go`, `pkg/access/access_test.go`, `pkg/identity/identity.go`
- [ ] 5.2 SECURITY. Add the hostile test of the authorization layer in `pkg/access/access_test.go`: a `writer`, a `viewer` and a principal with `job:read-logs` only are denied `unit:redact` at the route and again in the service; the test fails when the `ActionUnitRedact` line of `families` is removed.
  - Check: `go test -race ./pkg/access/... ./pkg/job/... -run Redact`
  - Confirmed by: CI
  - OpenDustpan: `pkg/access/access_test.go`

## 6. Wiring, e2e and docs

- [ ] 6.1 SECURITY. Replace `Artifacts: nil` in `pkg/dustpan/jobservice.go` with the adapter, built as `newJobArtifactReader` builds its reader. With a missing port the route stays `job.unsupported`.
  - Check: wiring test that the route answers 200 with the store and 501 without a blob store
  - Confirmed by: CI
  - OpenDustpan: `pkg/dustpan/jobservice.go`, `pkg/dustpan/jobs.go`
- [ ] 6.2 Add the new file e2e/job_log_redaction_test.go with the e2e step. It calls the routes `POST /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}/log-redactions` (`internal/api/jobs/redact.go:27`) and `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}/logs/{phase}` (`internal/api/jobs/routes.go:211`) with a user access token in the console scheme (`Authorization: token`, `authSchemeToken` in `e2e/harness.go:1889`): an ended job with a log, the redaction, the marker in the log route, a repeat gives 409 `job.wrong-state`, an unknown version gives 404 `job.not-found`, and the audit entry holds no text.
  - Check: `make e2e`
  - Confirmed by: CI
  - OpenDustpan: `e2e/job_log_redaction_test.go`
- [ ] 6.3 Update the redact section of `docs/guides/jobs.md`: read the version ID, redact, read the log, read the audit entry, and what stays (key shredded, blobs deleted later). A test runs each command. `make docs-lint` passes on each edited guide.
  - Check: `make docs-lint` passes on each edited guide, and the docs command test
  - Confirmed by: CI
  - OpenDustpan: `docs/guides/jobs.md`
- [ ] 6.4 Usability check of `docs/guides/jobs.md`: a person completes the path from the page alone and each error names its command. No file changes: record the result in the pull request.
  - Check: a person's walk through, recorded in the pull request body
  - Confirmed by: the reviewer
  - OpenDustpan: `docs/guides/jobs.md` (read only)
- [ ] 6.5 Run `make check`, then move `openspec/changes/job-artifact-store-redaction/` to the archive with `npx @fission-ai/openspec@1.15.0 archive job-artifact-store-redaction --yes` in the pull request that completes the last task.
  - Check: `make check`
  - Confirmed by: CI
  - OpenDustpan: none
