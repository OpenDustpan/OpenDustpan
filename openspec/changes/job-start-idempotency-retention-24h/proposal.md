---
version: 5
---

Issue: #1326.

## Why

**Persona: agent.** An agent uses the REST API with an access token to start a job. It sends an Idempotency-Key header with the start. The server restarts or fails over, and the agent retries the same request. The agent needs the stored answer and not a second job. Issue 1326 says that idempotency keys must be kept for 24 hours, as decision 6 of ADR 210 requires. On main, the key of a job start names its job for as long as the job row exists: no time check applies. This change makes the key of a job start name its job for 24 hours from the creation of that job. After that time, the same key starts a new job.

The user path this change must make work:

1. **API.** The agent sends a job start with an Idempotency-Key header and an access token, and gets the answer.
2. **Server start.** The server process restarts, or the installation fails over to a replica, while the agent waits.
3. **API.** The agent sends the same start with the same key within 24 hours and gets the stored answer. No second job starts.
4. **API.** The agent sends the same key more than 24 hours after the first start. The server starts a new job.
5. **Docs page.** The agent developer reads the retention time of an Idempotency-Key for a job start on the API page.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The agent sends the same key with a different request body within 24 hours. | The server refuses the request with the existing key-reuse error (422, `request.idempotency-key-reused`) and starts no job. | Send the request again with a new Idempotency-Key header. |
| The agent retries after more than 24 hours. | The server treats the key as new and starts a new job. | GET /api/dustpan/v1/namespaces/{namespace}/jobs |

Evidence for the claims above (each fact is on main at commit 08f4a4372):

- The key of a job start lives in the job row, not in the generic idempotency store. Proof: func sameKey at `internal/store/jobstore/store.go:549` calls `tx.JobByKey` at line 554 and returns the stored job with no time check. The `jobs` table has the column `idempotency_key` (`internal/store/postgres/migrations/0168_baseline.sql:742`, `internal/store/sqlite/migrations/0168_baseline.sql:1026`).
- The start route does not use the generic idempotency store. Proof: `internal/api/jobs/start.go` uses the package `pkg/idempotency` only for `idempotency.ParseKey` (line 245), `idempotency.MaxKeyLength` (line 247) and `idempotency.ErrInvalidKey` (lines 255, 262). The key goes into `job.StartRequest.IdempotencyKey` at line 333. `rg -n "idempotency\." pkg/job/service.go` on main gives no line.
- The 24-hour constant of the generic store does not reach the job start. Proof: `RecordTTL = 24 * time.Hour` at `pkg/idempotency/idempotency.go:29`. Its only callers outside tests are `internal/store/postgres/idempotency.go:166,277` and `internal/store/sqlite/idempotency.go:155,275`, the generic store.
- A unique index stops a second job row with the same key in a namespace. Proof: `CREATE UNIQUE INDEX jobs_key ON jobs USING btree (namespace_id, idempotency_key) WHERE (idempotency_key <> ''::text)` at `internal/store/postgres/migrations/0168_baseline.sql:2398`, and the same index at `internal/store/sqlite/migrations/0168_baseline.sql:1918`. `InsertJob` returns `ErrKeyIndex` for it (`internal/store/jobstore/tx.go:40`).
- The update of a job row does not write the key. Proof: the `UpdateJob` query sets no `idempotency_key` (`internal/store/postgres/queries.sql:4756`, `internal/store/sqlite/queries.sql:4262`).
- Only the `UpdateJob` query may update the `jobs` table. Proof: func TestJobRowIsWrittenOnlyByUpdateJobRow at `internal/store/postgres/job_update_chokepoint_internal_test.go:22`, and the same test in `internal/store/sqlite/job_update_chokepoint_internal_test.go`.
- The jobs API page says that a key lives as long as the job. Proof: "The server keeps a key as long as it keeps the job." at `docs/reference/jobs-api.md:116-117`.

## What Changes

- The key of a job start names its job for the Retention (24 hours) from the creation time of that job. The expiry is not stored: the job store derives it on each read as the creation time of the job plus the Retention. The creation time of the job row is the one source of truth.
- A start whose key names a job older than the Retention starts a new job. In the same transaction, the store clears the key of the old job row, and the new job row takes the key. The unique index `jobs_key` stays as it is.
- The `UpdateJob` query of each dialect can clear the key of a job row. It can never set a key on an update.
- NEW: a Retention type in `pkg/idempotency` holds the retention. Only its constructor makes a value that works, and the constructor refuses less than 24 hours. The zero value fails closed.
- The jobs API page states the retention and links a NEW reference page.

No schema change: no new table, column or index.

Out of scope:

- Idempotency of other routes.
- Other items of milestone 5, for example write-back and run triggers.
- A setting that changes the retention.
- A sweep of old keys. The key stays in the job row until a new start takes it.
- The backup of the job key. The job row, with its key, is in the backup, as on main.

Depends on:

- `job-start-follow-ups-1321` (the part before it in the split, archived on main)

## Usability

Where the path still costs the person time or surprises them:

- The agent cannot see the retention time from the API response, so the docs page must state it.
- A retry after 24 hours starts a second job. The agent reads the jobs list to find the first job.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The job store returns the job that a key names, with no time check (func sameKey). (`internal/store/jobstore/store.go:549`)
- The job store runs a create again when the key index refuses the row (const createAttempts, func Create). (`internal/store/jobstore/store.go:35`)
- The job store updates a job row through one helper (func update). (`internal/store/jobstore/store.go:610`)
- The PostgreSQL and SQLite adapters update a job row in one function (func updateJobRow). (`internal/store/postgres/state_close.go:76`, `internal/store/sqlite/state_close.go:76`)
- A test covers the idempotency of a job start (func TestStartIdempotency). (`pkg/job/service_test.go:1080`)
- The conformance suite covers a repeated key (func testCreateKeyOnce). (`pkg/job/jobtest/create.go:107`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

- **ADR 210, decision 6** (`docs/adr/0210-the-tool-neutral-api-prefix-paths-actions-versions-and-style.md`). Owner-tagged: "Decision 6 is an owner decision of 2026-10-18." Quoted lines:
  - "**Retention.** The server keeps the replay record (the key, a keyed hash of the request body and the first answer) for 24 hours from the first request."
  - "**A different body.** The same key with a different body answers 422 with the error code `request.idempotency-key-reused` and changes nothing."
  - "Benchmark: Stripe idempotency keys (24 hours, a replay answers the first answer, a key with other parameters is an error)"
  - Changed by: "ADR 247 extends decision 3" and "ADR 286 changes: the words catalog and promotion become self-service and failover." Neither changes decision 6. Current rule (INDEX.md): "this ADR, with ADR 247, 286".
  - Result: no conflict. The change makes the job start follow the 24 hours of decision 6. The first request of a job start creates its job, so "24 hours from the first request" is the creation time of the job plus 24 hours.

## Open questions

none

Questions considered and answered:

- Does the 24-hour retention apply to the job start only, or to every route that takes an Idempotency-Key? The job start only. The other routes use the generic idempotency store, whose records already expire after `RecordTTL` (`pkg/idempotency/idempotency.go:29`). Decided by the PM (rule 16; benchmark: Stripe idempotency keys are pruned after 24 hours; a reused key after that is a new request).
- Where does the 24-hour check go? Where the key lives: the job row, in func sameKey. The expiry is derived on read (creation time plus Retention), so no stored expiry can disagree with the creation time. Decided by the PM (rule 16, same benchmark).
- How does the new job get the key, when the unique index `jobs_key` refuses a second row with the key? The store clears the key of the old row in the same transaction, before it inserts the new row. The `UpdateJob` query is the only query that may update a job row, so it gets the clear. No schema change is needed.
- Does the clear make a record? Yes. The old job row gets a new version and a new update time, as each update does. The same transaction also writes an audit entry with the action `job:key-expired` that names the old job, so that a user can see why the key stopped working. The store writes it on each clear, and a clear with no entry fails closed. The new start writes its own audit entry, as on main. No API answer of a job shows the key: `rg -n "IdempotencyKey" internal/api/jobs` on main gives the route flag (`internal/api/jobs/routes.go`, `internal/api/jobs/operations.go:127`) and the start request (`internal/api/jobs/start.go:333`) only.
- What happens when two retries race after the expiry? The first clears the key and inserts the new job. The clear of the second finds a newer version, and the store answers as for the key index: func Create runs again and finds the new job. One new job starts.
- Is the retention value of 24 hours a security semantic? No. It is a replay window for a retry. It grants no access and changes no authorization.
- Is a migration needed? No. The change edits one query in each dialect, not the schema.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code. Revised by the PM after review 1 of the execution driver.
