---
version: 2
---

## Why

**Persona: agent.** An Agent wants to start runs, read status and logs, and cancel runs from code, without use of the console by hand.

The user path this change must make work:

1. **API.** Call the runs API with an access token to start a run, read status and logs, and cancel when needed.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| 401 when the token is not valid. | Check the token and server health with `opendustpan status`. | `opendustpan status` |
| 403 when the caller lacks the run action on the unit. | Ask a namespace administrator for a grant on the unit, then check with `opendustpan check`. | `opendustpan check` |

Evidence for the claims above:

- Runner pools, claims and the runner protocol exist on main. Proof: ANY /runner/v1 in the route table and pkg/durable/runner_claimed_test.go:16 holds TestRunnerRunsAnOperationStartedWithItsClaim
- Runner error codes exist on main. Proof: internal/errcode/catalog_runner.go:8 lists runner.immutable, runner.invalid, runner.limit, runner.not-found, runner.pool-exists, runner.pool-in-use, runner.retired, runner.token-used

## What Changes

- Hold the place for RFC 72 programmatic runs through the API and an SDK.
- Refine scope, API operations, credentials and SDK languages after runners and the run model land.

Out of scope:

- Run limits and queue (RFC 107)
- Run options, re-runs and variables (RFC 108)
- Job kinds for state operations (RFC 109)
- Step results and scanners (RFC 110)
- C# and Java clients

Depends on:

- runners (RFC 6)
- 107-run-limits-run-queue
- 108-run-options-re-runs

Builds on (not a change):

- run model (milestone 5)

## Usability

Where the path still costs the person time or surprises them:

- No path exists yet. The refined change must keep one scripted path from the docs.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- A claim-based runner run carries a fencing token checked on each write. (`pkg/durable/runner_claimed_test.go:16 (TestRunnerRunsAnOperationStartedWithItsClaim)`)
- Job tokens allow only writes the job model allows. (`pkg/gate/statewrite_test.go:804 (TestJobTokenWritesOnlyWhatTheJobModelAllows)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 41: The server reads state and masks secrets** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 60 changes decisions 5 and 13; ADR 103 changes decision 13; ADR 146 extends decision 13; ADR 151 changes decision 10; ADR 238 changes decision 12; ADR 240 changes decisions 9 and 13; ADR 240 extends decision 5; ADR 352 extends decision 10. Chain to the end: ADR 60, ADR 103, ADR 146, ADR 151, ADR 238, ADR 240, ADR 352, ADR 63, ADR 64, ADR 65, ADR 68, ADR 72, ADR 126, ADR 210, ADR 334, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 149, ADR 262, ADR 297, ADR 154, ADR 217, ADR 220, ADR 315, ADR 356, ADR 354, ADR 124, ADR 216.
- Current rule: this ADR, with ADR 60, 103, 146, 151, 238, 240, 352.
- Quote: "A read masks secret values. A state read answers the structure and the values of a state. It replaces each secret value with a marker."
- Effect: Run logs and state reads mask secret values. Plain values need a separate reveal with its own action and audit entry.
- Conflict: none.

**ADR 72: Scale out on one database with durable operations** (not owner-tagged).

- Decisions that apply: 2 (current), 4 (current).
- Changed by: ADR 73 changes decision 7; ADR 75 extends decision 10; ADR 103 changes decision 6; ADR 126 changes decision 6; ADR 151 extends decision 5; ADR 166 extends decision 1; ADR 240 extends decision 6; ADR 249 extends decision 11; ADR 280 extends; ADR 298 extends. Chain to the end: ADR 73, ADR 75, ADR 103, ADR 126, ADR 151, ADR 166, ADR 240, ADR 249, ADR 280, ADR 298, ADR 322, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 146, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 129, ADR 154, ADR 210, ADR 217, ADR 220, ADR 286, ADR 297, ADR 354, ADR 281, ADR 302, ADR 337, ADR 325, ADR 326, ADR 362.
- Current rule: this ADR, with ADR 73, 75, 103, 126, 151, 166, 240, 249, 280, 298.
- Quote: "A lease per operation (L1). Each claimed operation has an expiry on the database clock. The worker that runs it extends the expiry. Each claim has a fencing token that increases on each takeover."
- Quote: "A write-path installation check. Every write transaction reads the installation state in the same statement that reads its namespace fence."
- Effect: Programmatic runs start durable work and keep the lease and installation check rules.
- Conflict: none.

**ADR 227: One fresh-authentication window** (not owner-tagged).

- Decisions that apply: 6 (extended).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends. Chain to the end: ADR 301, ADR 320, ADR 339, ADR 354, ADR 332.
- Current rule: this ADR, with ADR 301, 320, 339, 354.
- Quote: "Errors. A step with no fresh authentication answers `403` with the error code `mfa.fresh-step-required` and changes nothing (ADR 212 decision 3). The error names the path of a new sign-in. An access token never gives a fresh authentication."
- Effect: A gated run that needs fresh authentication is refused when the caller has no fresh step. An access token alone does not pass.
- Conflict: none.

**ADR 248: Federation model: installations own namespaces, request forwarding, asynchronous replication and manual failover** (not owner-tagged).

- Decisions that apply: 14 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "Background work runs on the primary installation. This follows from ADR 72 and decision 3: durable operations, schedules and retention of a namespace run only on its primary installation, with the lease of ADR 72 decision 2. That lease is local to one database."
- Effect: Programmatic runs start work only on the primary installation of the namespace.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which run operations does the API expose, and which SDKs ship first?
   - Options: Start, status, logs and cancel plus a Go client generated from the API description; API only with no SDK.
   - Provisional decision (owner review 14 Nov): Start, status, logs and cancel plus a Go client generated from the API description
   - Reason: Runners and the run model are not decided, so the operation set, actions and audit entries stay open. Benchmark: AWS: every API operation ships with a generated SDK client.
   - Persona impact: An Agent gets scripted start, status and logs. Escape hatch: Use the console and the CLI until the SDK ships.
2. Which credential starts a run, how do retries and cancel races behave, and how are log secrets masked?
   - Options: Caller access token, Idempotency-Key on start, masked paged logs; New run-scoped credential.
   - Provisional decision (owner review 14 Nov): Caller access token, Idempotency-Key on start, masked paged logs
   - Reason: The pack does not bind credentials, retry, 421, 503 or log paging to runs. Benchmark: SOC 2: least-privilege credentials and logged access.
   - Persona impact: An Agent retries with no duplicate run. Escape hatch: Read status before a new start.
3. How do start retry, runner loss and cancel race behave?
   - Options: Idempotency-Key on start, lease takeover on runner loss, cancel loses to completion; No retry rule.
   - Provisional decision (owner review 14 Nov): Idempotency-Key on start, lease takeover on runner loss, cancel loses to completion
   - Reason: Duplicate runs, lease takeover and cancel race need the run model. Benchmark: AWS: retry with an idempotency token never makes a duplicate.
   - Persona impact: An Agent retries a timed-out start with no duplicate run. Escape hatch: Read status before a retry and cancel only a live run.
4. How do log reads, reveal and fresh authentication behave for gated runs?
   - Options: Masked logs by default with a separate reveal, fresh step refused with mfa.fresh-step-required; Plain logs.
   - Provisional decision (owner review 14 Nov): Masked logs by default with a separate reveal, fresh step refused with mfa.fresh-step-required
   - Reason: Log secret masking, reveal action and fresh refusal need the run model. Benchmark: ISO 27001: secret data stays masked and privileged steps need fresh proof.
   - Persona impact: An Agent reads logs with no secret leak and gets a clear refusal for gated runs. Escape hatch: Masked logs stay readable and reveal stays a separate call.
   - Owner-tagged ADRs involved: ADR 227 (their words do not change).

Questions considered and answered:

- Does programmatic access need a new protocol? No. Automation uses the public REST API. A script can do everything that an operator does (ADR 305 decision 4 is context only, not a decision for runs).
- How do runs keep leases, fences and placement? ADR 72 decisions 2 and 4 and ADR 248 decision 14 keep leases, fences and primary placement unchanged.
- How are log secrets and gate approvals handled? ADR 41 decisions 2 and 6 and ADR 227 decision 6 answer masking, reveal and fresh authentication. The run-specific actions and audit entries wait on the RFC.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
