## Context

Job A holds an accepted claim on a workspace and applies. Job C registers a claim on the same workspace. The claim record, the register route, and the fence come from the dependency item. This change adds the overlap refusal, the blocked status, and the release of the block. No code lands before the owner accepts the concurrent-applies RFC as an ADR.

## Decisions

1. **Refusal is a state of the claim record.** A refused claim stores the refused state with the blocking claim ID and the overlapping instance address. The overlap check and the insert run in one fenced transaction under a lock on the workspace claims, so two parallel inserts cannot both be accepted. Registration is idempotent by job ID and claim ID from the dependency item. A blocked job stays blocked after the blocker ends. The claim and the job use stable IDs: the workspace ID and the claim ID.

2. **A blocked job is a terminal state of the job.** The job state machine in `pkg/job/state.go` gets the state blocked and the event blocked (running to blocked). The blocker claim ID and the overlapping address stay in the refused claim record (columns `blocker_id` and `overlap_address`) and in `Job.Reason` as text. The job gets no new field, so no struct-field preflight is needed. A job token of a blocked job is not live (`jobtoken.JobStateLive`).
3. **The refusal commits, then answers.** Today `Register` rolls back on an overlap, so no refused claim exists. The registration stores the refused claim in the fenced transaction, commits, and then returns the overlap error to the route, which answers 409. A retry with the same job ID and claim ID finds the stored refused claim and answers the same 409. A refused claim has no lease, takes no part in the overlap set, and is not ended by the job release or the lease sweep.
4. **The schema changes in place.** One baseline file for each dialect holds the new columns and the new claim state (no migration code, because no tag shipped the table). The backup column list of the claims table matches the live schema. The generated code is made again with `make generate`.

## Files that the change touches

The claim domain (`pkg/workspace/claim_register.go`, `claim_store.go`, `workspacetest/claims.go`), the job domain (`pkg/job/state.go`, `claimer.go`, `jobtest`), the token check (`pkg/jobtoken/jobtoken.go`), the revision state map (`internal/selfservice/state.go`), the route glue (`internal/api/runnerapi/claimer.go`, `claims.go`, `errors.go`), the runner (`pkg/runner`), the wiring (`pkg/dustpan/runners.go`), the SQL adapters (both baselines, both `queries.sql`, `workspace_claims.go`, `internal/store/codec/claims.go`, the backup column lists and `storetest/backup.go`), the docs and the e2e step. Each file sits in a task of `tasks.md`. The coordinator of the Kubernetes runner (`pkg/runner/coordinator.go`) has no server job state and is not a touch.

## Structure

An unexported type claimSet has one constructor used by the registration function. The constructor checks the overlap inside the fenced transaction. No caller stores an accepted claim that overlaps. The same constructor builds a refused claim from the blocker, so the refused state and the blocker ID cannot be set apart.

## Risks

- The refusal omits the blocking claim ID, so the developer cannot tell which team blocks the apply.
- A blocked job starts the apply and calls the provider.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| Store | The overlap check and the insert run in one fenced transaction under the workspace claims lock. | Two parallel overlapping registrations run at once; at most one claim is accepted. |
| Service | The apply start checks for an accepted claim of the job and denies the apply without one, including a stale runner whose lease was lost. | A runner with a refused claim, or with a lost lease, starts the apply phase; the server denies it with 409. |
| HTTP handler | The refusal read checks the job read action on the job. | A caller without the action reads a refused claim; the server denies the read with no claim content. |
