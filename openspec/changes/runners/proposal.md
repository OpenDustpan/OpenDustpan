Roadmap: `docs/roadmap.md:259` (Runners).

## Why

**Persona: runner operator.** A runner operator runs one pool for Pulumi jobs and OpenTofu jobs. The operator creates a join token, joins runners, and trusts that a dead runner loses its claim and another runner takes the job while the log stays readable.

The user path this change must make work:

1. **API.** Create a pool and a join token from the docs page, then join a real runner with the token on standard input.
2. **CLI.** Start a Pulumi plan job and an OpenTofu plan job; each runner polls and claims work for its pool.
3. **E2e.** Kill a runner during a job; the claim expires and another runner takes the job; read the live log while it runs.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Second join with one join token fails. | {"code":"token-used","message":"join token already used; create a new join token with POST /api/dustpan/v1/namespaces/{namespace}/runner-pools/{pool}/join-tokens"} | `POST /api/dustpan/v1/namespaces/{namespace}/runner-pools/{pool}/join-tokens` |
| Plan job with unmatched labels stays queued. | {"code":"queued","message":"job stays queued; no runner reports labels <labels>"} | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{id}` |
| Killed runner loses its claim after lease expiry. | {"code":"claim-expired","message":"claim lease expired; claim returned to queue"} | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{id}` |

Evidence for the claims above:

- Runner protocol code exists in internal/api/runnerapi and pkg/runner with conformance suite pkg/runner/runnertest. Proof: internal/api/runnerapi, pkg/runner, pkg/runner/runnertest
- Job e2e files cover claims, jobs API and plan jobs. Proof: e2e/claim_gate_test.go, e2e/jobs_api_test.go, e2e/jobs_plan_test.go
- No e2e file names a join token flow. Proof: Join token e2e: [] with e2e_count 0

## What Changes

- Pool join-token create, revoke and list under /api/dustpan/v1/namespaces/{namespace}/runner-pools; one-use join with SHA-256 store
- Poll, claim, renew and heartbeat for jobs of both tools in one pool
- Lease expiry with takeover and live ordered log read stream

Out of scope:

- Runner binary, coordinators, launcher, isolation and sandbox runs
- Plan and apply job execution, stored plans, profiles in jobs
- Log upload, chunk hash check and session writes (add-job-session-paths-runner)
- Cross-instance wake of long poll (wake-long-poll-when-operation)
- Policy checks, approvals, pull-request automation, write-back
- Console changes
- TFE agent token refusal copy beyond the join-token route message

Depends on:

- add-job-service-routes-start
- add-job-session-paths-runner
- wake-long-poll-when-operation
- add-e2e-step-runs-real-runner-pool-route-move

## Usability

Where the path still costs the person time or surprises them:

- A join failure names the one command that creates a fresh token.
- A queued job with unmatched labels shows which labels miss a runner.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 131: The runner protocol** (owner-tagged: decision 5).

- Decisions that apply: 1 (current), 2 (current), 4 (current), 5 (current), 7 (current), 8 (current).
- Changed by: ADR 139 extends decision 6. Chain to the end: ADR 139, ADR 282, ADR 304, ADR 343, ADR 310.
- Current rule: this ADR, with ADR 139.
- Quote: "Two callers, two credentials. The runner (the coordinator) calls the paths of the runner, with a proof of its runner key."
- Quote: "The long poll (owner). A poll waits for work for at most 50 seconds (`protocol.MaxPollWait`), below the idle timeout of common proxies."
- Effect: Change follows the runner protocol paths, credentials, poll, renew and heartbeat rules. Log upload and hash checks stay in add-job-session-paths-runner. Cross-instance wake stays in wake-long-poll-when-operation.
- Conflict: none.

**ADR 133: Runner identities, join tokens, handoff tokens and job sessions** (owner-tagged: decision 2, 7).

- Decisions that apply: 1 (current), 2 (extended), 3 (current), 7 (current), 8 (current).
- Changed by: ADR 204 extends; ADR 350 extends decision 2. Chain to the end: ADR 204, ADR 350, ADR 248, ADR 286.
- Current rule: this ADR, with ADR 204, 350.
- Quote: "A runner is not a principal. A runner has its own credential kind: a runner key in one pool."
- Quote: "Join tokens (owner). A principal with `namespace:manage-runners` creates a join token for one pool."
- Effect: Change uses one-use join tokens and runner keys, refuses expired and retired runners, and never uses a principal credential for runners.
- Conflict: none.

**ADR 282: Runners and remote runs: the project accepts RFC 6** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 4 (extended).
- Changed by: ADR 304 changes decision 5; ADR 343 extends decision 4. Chain to the end: ADR 304, ADR 343, ADR 310.
- Current rule: this ADR, with ADR 304, 343.
- Quote: "The project accepts RFC 6. Its decided parts are the decisions of ADRs 100 to 103 and 130 to 139, with the amendment of decision 4 to ADR 139."
- Effect: Runner binary lives outside this change; this change covers only server pool, join, claim, lease and log read paths.
- Conflict: none.

**ADR 343: Runner work: tool imports, sweeps and the runner repository** (owner-tagged (all decisions)).

- Decisions that apply: 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The runner repository is an exception for the runner only (owner, 2026-11-07)."
- Effect: Change builds no runner binary and keeps all runner code out of this repository, as the exception allows only the runner repository.
- Conflict: none.

**ADR 350: OpenDustpan runners replace HCP Terraform agents, and a gap in security is a documented gap** (owner-tagged (all decisions)).

- Decisions that apply: 2 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A TFE agent token is refused (owner, 2026-11-07). A TFE agent token is long-lived and has many uses."
- Effect: Change refuses a request to create a TFE agent token with a JSON:API error that names the pool join-token route.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (owner decision conflict): The spec and design say joined runners are used via a join token, but the change does not address ADR 133 decision 7 (must-rejoin after 30 days offline), which is owner-tagged and not covered by any requirement or scenario in specs/runner-pools/spec.md. Resolution: answered: Added must-rejoin requirement for a runner offline more than 30 days.
- R1.2 (missing scenario): No requirement covers poll or claim with a runner whose key is expired or retired (ADR 133 decisions 7 and 8); the spec only covers the one-use join and claim of jobs. Resolution: answered: Added retired and expired-key poll and renew refusal requirements.
- R1.3 (failure mode): The design lists token replay and a nonce cache, but no scenario tests a replayed poll proof or a stale Dustpan-Proof; the HTTP adapter hostile test has no task in tasks.md. Resolution: answered: Added replayed and stale proof refusal requirement and hostile-test task.
- R1.4 (missing scenario): No scenario covers the log upload hash check (Dustpan-Content-SHA256 mismatch, ADR 131 decision 10), although the design names corrupt output as a risk. Resolution: answered: Log hash check moved to add-job-session-paths-runner per M.1; no hash requirement kept here.
- R1.5 (failure mode): Lease renewal by a runner that does not own the claim is named as a hostile test, but no requirement or task covers it; the renew owner check is not tested. Resolution: answered: Added renew owner-check requirement and task.
- R1.6 (open question): The proposal promises that an unmatched-label job shows which labels miss a runner, but no requirement or scenario defines that output; the error table only gives a generic no-work message. Resolution: answered: Unmatched-labels requirement names missing labels in the queued-job read.
- R1.7 (adr conflict): The ADR check cites ADR 139 decision 4 as current while ADR 282 decision 4 supersedes it; the spec-ready gate fails on this E007 error, so the ADR check is stale. Resolution: answered: No ADR 139 entry.
- R1.8 (wrong claim): The ADR check says decision 4 of ADR 139 is current in the Decisions list, but the decisions list gives ADR 133 decision 2 as extended by ADR 350; the claim that ADR 350 decision 2 makes the join token the only way to add a runner is not in the quoted text. Resolution: answered: Narrowed ADR 350 effect to the quoted refusal and route message.
- R1.9 (open question): The lease length, the renew interval and the takeover delay are not stated anywhere; no ADR answers how long a dead runner's lease lasts. Resolution: open question: Lease timings stay an open question for PM.
- R1.10 (scope): Tasks 2.1 require the console run view check, but the proposal's Out of scope and What Changes do not list a console change, so the task misses or adds work outside the item. Resolution: answered: Removed console run view check; console is explicit out of scope.
- R1.11 (missing scenario): No scenario covers a join token that is expired, revoked or refused for a TFE agent token route (ADR 350 decision 2), although the proposal lists the TFE refusal. Resolution: answered: Added expired, revoked and TFE-agent-token refusal scenarios.
- R2.1 (open question): Lease length, renew interval and takeover delay are undefined; ADR 131 gives only the poll and heartbeat timings, so the lease-expiry requirement cannot be built or tested. Decision belongs to the owner-delegated PM (proposal open question 1 is pending). Resolution: open question: Lease length, renew interval and takeover delay stay an open question for PM decision.
- R2.2 (wrong claim): The ADR check quotes ADR 133 decision 7 and ADR 350 decision 2 as owner-tagged, but the 'Effect' of ADR 350 claims the join token is the only way to add a runner, and the links say so; the proposal's ADR check text is not supported by the quoted decision, which says only that TFE agent token create requests are refused. Resolution: answered: Narrowed ADR 350 effect to the quoted refusal and route message only.
- R2.3 (wrong claim): Error table and tasks cite GET /api/dustpan/v1/jobs/{id} and GET /api/dustpan/v1/pools/{pool}/runners as help commands; the change's own spec-ready gate reports both routes as absent from the API description, so the user-facing help is unverified. Resolution: answered: Routes now use the namespaced runner-pools prefix from the route table.
- R2.4 (adr conflict): The proposal still cites ADR 139 as a current decision basis while ADR 282 decision 4 supersedes its decision 4; the ADR check must name ADR 282 and the owner-tagged repository rule (ADR 343 decision 6) instead. Resolution: answered: No ADR 139 entry; ADR 282 decision 4 plus ADR 343 decision 6 cover the runner-repository rule.
- R2.5 (missing scenario): Claim takeover after a runner's lease expires has no scenario for a second expiry, a retired runner's live claim during takeover, or a claim whose owner returns after takeover (stale owner renewing a lost claim). Resolution: answered: Added takeover scenarios for second expiry, retired live claim, and stale owner renew.
- R2.6 (failure mode): The one-use join has no requirement for a join that succeeds in the store but whose response is lost; the runner retries and gets token-used with no way to learn its runner ID, and the design does not state how the runner recovers. Resolution: open question: Added open question on lost join response recovery.
- R2.7 (missing scenario): The live log stream has no scenario for a reader that disconnects and resumes, or for a job that ends or is cancelled while the stream is open (ADR 131 decision 9 cancel path). Resolution: answered: Kept read-stream resume and cancel-close scenarios; upload cover moved to add-job-session-paths-runner.
- R2.8 (failure mode): Poll long-wait behavior (50 s wait, 5 s re-check, ADR 131 decision 5) is not covered by any scenario; a proxy idle timeout or a missed durable-operation signal leaves a runner waiting with no work. Resolution: answered: Scoped long-poll requirement to base wait and recheck; wake moved to wake-long-poll change.
- R2.9 (missing scenario): Retired runner ends its job sessions (ADR 133 decision 8, error retired on session requests); the spec covers only poll and renew, not later session requests. Resolution: answered: Added retired session-request refusal scenario.
- R2.10 (scope): Tasks 2.1 require a docs page for runner join and pool run while the proposal's usability and Out of scope sections do not define that page; the task set does not name the docs-page check for each error command. Resolution: answered: Scoped docs task to the runner join guide page.
- M.1 (duplicate): The log upload and the log hash check duplicate add-job-session-paths-runner, and the long poll wait duplicates wake-long-poll-when-operation. The join-token routes do not use the runner-pools prefix of add-e2e-step-runs-real. Resolution: answered: Removed Log hash check requirement and log-upload cover to add-job-session-paths-runner; kept only ordered log read stream here. Scoped long poll to 50 second base with 5 second recheck and moved wake to wake-long-poll-when-operation. Moved join-token routes under /api/dustpan/v1/namespaces/{namespace}/runner-pools and added depends_on add-job-service-routes-start, add-job-session-paths-runner, wake-long-poll-when-operation and route-move change.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What lease length, renew interval and takeover delay does the server use for a dead runner claim?
   - Options: Fix claim lease, renew interval and takeover delay in the guide and surface effective values; Leave timings undocumented until load tests set them.
   - Provisional decision (owner review 14 Nov): Fix claim lease, renew interval and takeover delay in the guide and surface effective values
   - Reason: No ADR states the claim lease length; operators need a predictable takeover time. Benchmark: AWS: document the lease, renew interval and takeover delay as fixed timeouts in the user guide.
   - Persona impact: Runner operator sees predictable takeover time; no persona loses access. Escape hatch: Operator reads effective lease and renew values from the pool read route.
   - Owner-tagged ADRs involved: ADR 131 (their words do not change).
2. How does a runner recover when the join response is lost after the store insert?
   - Options: Retry with same public key returns the existing runner ID; Retry always gets token-used and operator creates a fresh token.
   - Provisional decision (owner review 14 Nov): Retry always gets token-used and operator creates a fresh token
   - Reason: ADR 133 states one-use consume with token-used on second join and does not state a retry rule. Benchmark: AWS: a create call that times out after the write returns the existing ID on retry with the same client token.
   - Persona impact: Runner operator avoids an orphan runner record; no persona loses access. Escape hatch: Runner operator creates a fresh join token and joins again; the orphan runner record stays for audit and is retired.
   - Owner-tagged ADRs involved: ADR 133 (their words do not change).

Questions considered and answered:

- Does the change need separate pools for Pulumi and OpenTofu? No. Server pool claim covers both tools; ADR 131 decision 4 says poll calls job.Claimer.Claim for jobs and sweep claimer for sweeps, and ADR 282 decision 1 accepts one runner design for both tools.
- Is the join token one-use with short life? Yes. ADR 133 decision 2 says one join, one-hour default, secret shown once with only SHA-256 kept, revokable.
- What happens when a runner dies during a job? Yes. ADR 131 decisions 7 and 8 give renew, heartbeat, lost-claim and takeover behavior; the milestone exit test kills a runner and expects lease expiry plus takeover by another runner.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
