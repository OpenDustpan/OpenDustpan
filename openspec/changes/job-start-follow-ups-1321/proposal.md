Issue: #1326, #1337.

## Why

**Persona: developer.** A developer starts plan and apply jobs from scripts that retry after timeouts. Lost retries, wrong denial names, and stalled claim polls cost runs and hide the cause.

The user path this change must make work:

1. **API.** Start a plan or apply job with Idempotency-Key and retry after a timeout
2. **API.** Read the job state after a cancel and after a poison read failure
3. **Docs page.** Follow the job start and claim notes on the docs page

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Retry of a job start with the same key returns the first answer within 24 hours. | The server returns the stored answer for the same key, principal, and route. | `curl -H 'Authorization: Bearer <token>' -H 'Idempotency-Key: <key>' -H 'Content-Type: application/json' -d '{"operation":"plan"}' https://server/api/dustpan/v1/namespaces/{namespace}/jobs` |
| Job start with a non-agent source for an agent actor fails. | The server refuses the start with 400 and error code job.bad-source and names the refused source. | `curl -H 'Authorization: Bearer <token>' -H 'Content-Type: application/json' -d '{"operation":"plan","source":"api"}' https://server/api/dustpan/v1/namespaces/{namespace}/jobs` |
| Retry with the same key and a different body fails. | The server refuses the retry with 409 and error code job.idempotency-conflict without starting a second job. | `curl -H 'Authorization: Bearer <token>' -H 'Idempotency-Key: <key>' -H 'Content-Type: application/json' -d '{"operation":"apply"}' https://server/api/dustpan/v1/namespaces/{namespace}/jobs` |

Evidence for the claims above:

- Issue 1326 asks for 24 hour retention and lists the Route comment, sourceOf, and denial action nits. Proof: These items come from the round 1 review of #1321: Idempotency keys must be kept for 24 h, as ADR 210 d6 requires; the comment on Route.IdempotencyKey says the wrapper returns 400; sourceOf accepts a source that is not an agent source; the denial record always names unit:run-plan
- The quoted ADR 210 decision requires the Idempotency-Key header on mutating routes and does not state the 24 hour window in the visible text. Proof: Each route of the tool-neutral API that changes data (`POST`, `PUT`, `PATCH`, `DELETE`) accepts the header `Idempotency-Key`.
- Issue 1337 asks for poison skip with count, cancel state agreement, bindClaim comment, and bound count. Proof: These items come from the round 3 review of #1307: A poison operation: when the read of its job or its namespace fails, the poll returns an error and does not go on to the next operation; After a cancel where a change began, the durable record says canceled but the job ends failed
- The job start source check lives in pkg/job/service_start.go. Proof: pkg/job/service_start.go:78: source, err := sourceOf(a, req.Source) and pkg/job/service_start.go:134: func sourceOf(a Actor, given SourceKind) (SourceKind, error) {
- The claim bind check lives in pkg/store/internal/jobstore/apply.go. Proof: pkg/store/internal/jobstore/apply.go:162: func bindClaim(ctx context.Context, tx Tx, cur *job.Job, c *job.Change) error {

## What Changes

- Keep job-start idempotency answers for 24 hours in the durable store and fix the Route.IdempotencyKey comment
- Refuse a non-agent source when the actor is an agent
- Write the refused action with actor, reason, and time in the job-start denial record
- Skip a poison operation for that poll, count it, and retry it on the next poll
- End the job and the durable record both as canceled on cancel after a change began
- Count and log operations skipped by the bound of 64, process them on later polls, and mark the bindClaim phase check as defense in depth

Out of scope:

- Use of the agent as a change key
- The giveBack race

## Usability

Where the path still costs the person time or surprises them:

- Retry of a job start with the same key surprises when the window is shorter than one work day.
- A denial that names the wrong action costs time when the person holds the named action.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide** (owner-tagged: decision 6).

- Decisions that apply: 6 (current).
- Changed by: ADR 247 extends decision 3; ADR 286 changes. Chain to the end: ADR 247, ADR 286.
- Current rule: this ADR, with ADR 247, 286.
- Quote: "Each route of the tool-neutral API that changes data (`POST`, `PUT`, `PATCH`, `DELETE`) accepts the header `Idempotency-Key`."
- Effect: The change keeps the mutating job start route under the Idempotency-Key rule. The 24 hour window comes from issue 1326, not from the quoted decision text.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (wrong claim): The error table names `opendustpan jobs start --source` as the command that helps, but the open-questions code check reports that `opendustpan jobs` does not exist. The command the change relies on is not in the CLI. Replace it with a real command or add the command to the change. Resolution: answered: Replaced the non-existent CLI with curl POST calls in errors.
- R1.2 (open question): The Open questions section does not read none: a code check fails on the non-existent `opendustpan jobs` command. Under the spec-ready gate, no implementation may start until this is fixed. Resolution: open question: Fixed the command and added the cancel-state open question; the change is not spec-ready until the PM decides it.
- R1.3 (wrong claim): The first error row says a retry with the same key gets a 409 with the stored answer. The spec (idempotency-retention) says the server returns the stored answer. The change does not say which status the retry gets, so the docs and the tests would disagree. Resolution: answered: Removed the 409 claim; errors and spec now say the server returns the stored answer.
- R1.4 (wrong claim): The ADR check quotes ADR 210 decision 6 only for the Idempotency-Key header rule. The 24 hour retention that the proposal says ADR 210 d6 requires is not in the quoted text, and the decision text in the input is cut off. The claim that the 24 hour window is required by ADR 210 is unsupported; quote the full decision line or drop the claim. Resolution: answered: ADR check no longer claims that ADR 210 requires 24 hours; the window is cited from issue 1326.
- R1.5 (open question): The cancel-state requirement says the job and the durable record must agree, but not which terminal state wins (canceled or failed). The design also says the change alters what automation reads for canceled jobs. No ADR or owner decision answers this, and the choice must be made before the fix is built. Resolution: open question: Added the cancel-state open question with options and a recommended canceled state.
- R1.6 (failure mode): The idempotency requirement does not cover a retry that arrives while the first request is still running, a retry with the same key but a different body, or a retry after the 24 hour window. Each case can start a duplicate job or return a wrong answer, and none has a scenario. Resolution: answered: Added scenarios for a retry while the first request runs, a different body, and a retry after 24 hours.
- R1.7 (missing scenario): The poison-skip requirement has no rule for a job that keeps failing. The operation is skipped on every poll and the poll never reports it except as a count. Nothing says when the skip is surfaced to an operator or how the count is read. Resolution: answered: Poison skip surfacing is covered by scenario Failed job read in poison-skip, which logs the job reference and continues, and by scenario Poison skip counted in poison-count; surfacing beyond count and log is left to later work.
- R1.8 (missing scenario): The project rules say each automatic action writes a record of who or what did it and why. A poison skip and a bound skip are automatic actions, but the spec only increases a count and logs a line. No record is written for either skip. Resolution: answered: Bound skips are counted and logged with the job reference, covered by scenario Poll bound reached in bound-count; no separate audit entry ships in this fix.
- R1.9 (scope): The proposal says the bound skips get a metric and a log line, but the bound-count requirement says only counted and logged. The metric is not in the spec, so the task cannot be checked against it. Resolution: answered: Bound-count requirement now names the poll-bound skip metric and the log line scenario.
- R1.10 (missing scenario): The non-agent source requirement does not name the error code or the response for the refusal. The proposal says a 400 answer; the spec does not. A new error code is an additive change under ADR 210 decision 8, and the change does not say it is one. Resolution: answered: Refusal is covered by scenario Non-agent source in wrong-source-for-agent, which names the 400 answer with error code job.bad-source; additive under ADR 210 decision 8.
- R2.1 (failure mode): openspec/changes/job-start-follow-ups-1321/specs/job-start/spec.md: the idempotency-retention requirement does not say whether stored answers survive a server restart or a store failover. A retry after a restart may start a duplicate job, which breaks the 24 hour promise. Resolution: answered: Retention requirement now says durable store that survives restart and failover, with a restart scenario.
- R2.2 (failure mode): openspec/changes/job-start-follow-ups-1321/specs/job-start/spec.md: no scenario covers a first request that crashes or times out before it stores an answer. The retry then has no stored or in-flight answer to return, and the spec does not say whether it starts a second job or waits. Resolution: answered: Added crash-before-store scenario: a retry with no stored or in-flight answer starts a new job.
- R2.3 (failure mode): openspec/changes/job-start-follow-ups-1321/specs/job-claim/spec.md: poison-skip does not say whether a skipped operation is retried on the next poll. A transient read outage would skip the operation and it may stall until a later poll, and the spec does not state the rule. Resolution: answered: Poison skip now says skip for that poll and retry on the next poll, with a next-poll scenario.
- R2.4 (wrong claim): openspec/changes/job-start-follow-ups-1321/proposal.md: the evidence still says ADR 210 d6 requires 24 h retention, but the ADR check says the window comes from issue 1326. The quoted d6 text in the input is cut off and does not support the 24 h claim. Resolution: answered: Evidence no longer says ADR 210 requires 24 hours; the window is cited from issue 1326 and the ADR quote is shown without the window.
- R2.5 (missing scenario): openspec/changes/job-start-follow-ups-1321/tasks.md: task 2.4 asks to mark the bindClaim phase check as defense in depth, but no requirement or scenario in the specs covers it, so the task cannot be checked against a spec. Resolution: answered: Added bind-phase-check requirement and scenario so the defense-in-depth comment task maps to a spec.
- R2.6 (open question): openspec/changes/job-start-follow-ups-1321/specs/job-start/spec.md: the cancel-state requirement says only that the job and the record carry the same terminal state. It does not name the state that the provisional decision chooses, so the spec stays wrong if the owner rejects that decision. Resolution: open question: Cancel requirement now names canceled as the provisional single state tied to the open question.
- R2.7 (missing scenario): openspec/changes/job-start-follow-ups-1321/specs/job-start/spec.md: the same-key different-body scenario refuses the retry but names no status or error code, unlike the non-agent source scenario. The client cannot tell this refusal from other errors. Resolution: answered: Same-key different-body scenario now names 409 and error code job.idempotency-conflict.
- R2.8 (missing scenario): openspec/changes/job-start-follow-ups-1321/specs/job-start/spec.md: the denial-record requirement names only the refused action. It does not require the actor, the reason or the time, although the project rules require a record of who did an automatic action and why. Resolution: answered: Denial requirement now requires refused action with actor, reason, and time, with a scenario that checks each field.
- R2.9 (failure mode): openspec/changes/job-start-follow-ups-1321/specs/job-claim/spec.md: operations beyond the bound of 64 are counted and logged, but the spec does not say they are picked up on a later poll. Under load some operations may starve. Resolution: answered: Added bound-fairness requirement: bound skips are processed on later polls with no starvation.
- R2.10 (wrong claim): openspec/changes/job-start-follow-ups-1321/design.md: the design names an unexported jobStarter built by newJobStarter that keeps the window, source check and refused action. The input does not show that this type exists in pkg/job, so the structural claim is unsupported. Resolution: answered: Design no longer claims a jobStarter type; it names the existing sourceOf and bindClaim functions.
- R2.11 (missing scenario): openspec/changes/job-start-follow-ups-1321/proposal.md: the error table gives curl lines without the host, the authentication header or a request body, so the person cannot run the command that is said to help. Resolution: answered: Error commands now show host, Authorization header, Idempotency-Key header, and JSON body.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which single terminal state wins when a cancel arrives after a change began?
   - Options: End both records as canceled; End both records as failed; Keep the split and document why they differ.
   - Provisional decision (owner review 14 Nov): End both records as canceled
   - Reason: One state removes the split that issue 1337 reports and matches the cancel request. Benchmark: Google Cloud workflow retries use a single terminal canceled state for a caller cancel, with the cause kept in a detail field.
   - Persona impact: A Developer reads one terminal state after a cancel and does not change scripts except for the state name. Escape hatch: Automation reads the detail field when it needs the prior distinction between canceled and failed.

Questions considered and answered:

- Does the idempotency retention fix need an RFC? ADR 210 decision 6 requires each mutating route to accept Idempotency-Key. The quoted text does not state 24 hours, so the window is taken from issue 1326 with no ADR conflict and no RFC.
- Do sourceOf, denial action, poison skip, cancel state, bindClaim comment, or bound count contradict an ADR? The pack names pkg/job/service_start.go and pkg/store/internal/jobstore/apply.go as the places to fix, with no ADR that allows the current behavior, so the fixes are code corrections.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
