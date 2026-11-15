---
version: 4
---

## Why

**Persona: developer.** A developer cancels a job that runs on a runner pool. The job must show as canceled. The background task that backs it must end the same way, so that no record stays open after the cancel.

The user path this change must make work:

1. **API.** The developer sends a cancel request for a job in a namespace.
2. **API.** The developer reads the job with GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job} and sees the state canceled.
3. **E2e.** A step with the real runner cancels a job and checks that the job and its durable record agree.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The job already ended. | The server answers with a conflict error that names the end state of the job. | GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job} |

Evidence for the claims above:

- The pack lists a durable recorder type in the job package. Proof: pkg/job/recorder.go:56 (type DurableRecorder)
- The pack lists the function that builds the recorder in the server wiring. Proof: pkg/dustpan/runners.go:226 (func newDurableRecorder)
- The pack lists a PostgreSQL function that starts a durable operation inside a state write. Proof: pkg/store/postgres/state_close.go:91 (func startDurableOperation)

## What Changes

- The cancel path of a job ends the job as canceled and ends its durable operation record as canceled in one transaction.
- No new route is added. The existing job cancel route keeps its path.

Out of scope:

- Job start rules and the other follow-ups of the first review round.
- Cancel of a Pulumi update.

Depends on:

- job-start-follow-ups-1321
- `job-start-follow-ups-1321` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- The cancel error names the job read call that shows the end state.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The DurableRecorder type exists in the job package. (`pkg/job/recorder.go:56 (type DurableRecorder)`)
- The server wiring builds the recorder. (`pkg/dustpan/runners.go:226 (func newDurableRecorder)`)
- The PostgreSQL store starts a durable operation in a state close. (`pkg/store/postgres/state_close.go:91 (func startDurableOperation)`)
- The PostgreSQL store exposes durable operations. (`pkg/store/postgres/durable.go:44 (func Store.DurableOperations)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does the change touch a numbered decision? The pack names no ADR for this item, so no ADR is quoted. The drafter found no conflict in the pack.
- Is the change security code? No. It changes only the end state of two records. It adds no authorization rule, so the existing authorization actions of the cancel route stay.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
