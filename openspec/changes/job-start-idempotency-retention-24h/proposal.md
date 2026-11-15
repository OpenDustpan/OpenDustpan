---
version: 4
---

## Why

**Persona: agent.** An agent uses the REST API with an access token to start a job. It sends an Idempotency-Key header with the start. The server restarts or fails over, and the agent retries the same request. The agent needs the stored answer and not a second job. The pack does not prove how long the durable store keeps a record. This change sets the retention of a job-start record to 24 hours.

The user path this change must make work:

1. **API.** The agent sends a job start with an Idempotency-Key header and an access token, and gets the answer.
2. **Server start.** The server process restarts, or the installation fails over to a replica, while the agent waits.
3. **API.** The agent sends the same start with the same key within 24 hours and gets the stored answer. No second job starts.
4. **Docs page.** The agent developer reads the retention time of an Idempotency-Key for a job start on the API page.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The agent sends the same key with a different request body. | The server refuses the request with the existing key-reuse error and starts no job. | Send the request again with a new Idempotency-Key header. |
| The agent retries after more than 24 hours. | The server treats the key as new and starts a new job. | GET /api/dustpan/v1/namespaces/{namespace}/jobs |

Evidence for the claims above:

- The package pkg/idempotency exists. Proof: The pack lists the path pkg/idempotency with exists true.
- A test for the idempotency of a job start exists on main. Proof: TestStartIdempotency at pkg/job/service_test.go:984.
- The server takes an idempotency store through an option. Proof: WithIdempotencyStore at pkg/dustpan/dustpan.go:381.
- The PostgreSQL adapter has a function that completes an idempotency record. Proof: completeIdempotencyRecord at pkg/store/postgres/idempotency.go:186.
- A restore empties the idempotency records. Proof: testRestoreEmptiesIdempotencyRecords at pkg/store/storetest/durable_restore.go:191.

## What Changes

- A job-start idempotency record has a retention of 24 hours in the durable store.
- NEW: a Retention type in pkg/idempotency holds the retention and is made only by its constructor.
- The store codec (pkg/store/internal/codec) keeps the expiry time with each record.
- A retry after a restart or a failover within the retention gets the stored answer.

Out of scope:

- Idempotency of other routes.
- Other items of milestone 5, for example write-back and run triggers.
- A setting that changes the retention.

Depends on:

- job-start-follow-ups-1321
- `job-start-follow-ups-1321` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- The agent cannot see the retention time from the API response, so the docs page must state it.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The server takes an idempotency store through an option (func WithIdempotencyStore). (`pkg/dustpan/dustpan.go:381`)
- The PostgreSQL adapter completes idempotency records (func completeIdempotencyRecord). (`pkg/store/postgres/idempotency.go:186`)
- A test covers the idempotency of a job start (TestStartIdempotency). (`pkg/job/service_test.go:984`)
- A restore test checks that idempotency records are emptied (testRestoreEmptiesIdempotencyRecords). (`pkg/store/storetest/durable_restore.go:191`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Does the 24-hour retention apply to the job start only, or to every route that takes an Idempotency-Key?
   - Options: Use 24 hours for the job start only; Use 24 hours for every route that takes an Idempotency-Key.
   - Provisional decision (owner review 14 Nov): Use 24 hours for the job start only
   - Reason: The item names the job start only. No ADR in the pack decides the retention, and the value is not a security rule. Benchmark: AWS: the idempotency token of an API call keeps its answer for a fixed time.
   - Persona impact: The agent gets a clear and stable retry window and no persona loses a feature. Escape hatch: The agent sends a new key to start a new job.

Questions considered and answered:

- Does the change touch an ADR decision? The pack lists no ADR, so no decision text can be quoted. The adr_check list is empty.
- Is a migration needed? The project rules say the baseline migration is edited in place until the first tag, so no numbered migration is added.
- Is the retention value of 24 hours a security semantic? No. It is a replay window for a retry. It grants no access and changes no authorization. The PM decides it.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
