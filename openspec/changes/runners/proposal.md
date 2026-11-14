---
version: 2
---

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
| Second join with one join token fails. | {"code":"token-used","message":"join token already used; create a new join token with POST /pools/{pool}/join-tokens of the runner API"} | `POST /pools/{pool}/join-tokens` |
| Plan job with unmatched labels stays queued. | {"code":"queued","message":"job stays queued; no runner reports labels <labels>"} | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{id}` |
| Killed runner loses its claim after lease expiry. | {"code":"claim-expired","message":"claim lease expired; claim returned to queue"} | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{id}` |

Evidence for the claims above:

- Runner protocol code exists in internal/api/runnerapi and pkg/runner with conformance suite pkg/runner/runnertest. Proof: internal/api/runnerapi, pkg/runner, pkg/runner/runnertest
- Job e2e files cover claims, jobs API and plan jobs. Proof: e2e/claim_gate_test.go, e2e/jobs_api_test.go, e2e/jobs_plan_test.go
- No e2e file names a join token flow. Proof: Join token e2e: [] with e2e_count 0

## What Changes

- New pool join-token create, revoke and list under /api/dustpan/v1/namespaces/{namespace}/runner-pools; one-use join with SHA-256 store, 1 hour default and 7 day maximum
- Poll, claim, renew and heartbeat under ANY /runner/v1 for jobs of both tools in one pool with 60 second lease, 20 second renew and 50 second poll hold
- Lease expiry with takeover and live ordered log read stream with unit:read-logs

Out of scope:

- Runner binary, coordinators, launcher, isolation and sandbox runs
- Plan and apply job execution, stored plans, profiles in jobs
- Log upload, chunk hash check and session writes (add-job-session-paths-runner)
- Cross-instance wake of long poll (wake-long-poll-when-operation)
- Policy checks, approvals, pull-request automation, write-back
- Console changes

Depends on:

- add-job-service-routes-start
- add-job-session-paths-runner
- wake-long-poll-when-operation
- add-e2e-step-runs-real-runner-pool-route-move

## Usability

Where the path still costs the person time or surprises them:

- A join failure names the one command that creates a fresh token.
- A queued job with unmatched labels shows which labels miss a runner.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Runner error codes exist with runner.token-used (`internal/errcode/catalog_runner.go:27`)
- Runner error codes exist with runner.retired (`internal/errcode/catalog_runner.go:24`)
- Job state machine reaches every state from queued (`pkg/job/state_test.go:135`)
- Lost claim ends the step (`pkg/launcher/run/run_test.go:162`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 131: The runner protocol** (owner-tagged: decision 5).

- Decisions that apply: 1 (current), 2 (current), 4 (current), 5 (current), 7 (current), 8 (current).
- Changed by: ADR 139 extends decision 6. Chain to the end: ADR 139, ADR 282, ADR 304, ADR 343, ADR 310.
- Current rule: this ADR, with ADR 139.
- Quote: "Two callers, two credentials. The runner (the coordinator) calls the paths of the runner, with a proof of its runner key."
- Quote: "The long poll (owner). A poll waits for work for at most 50 seconds (`protocol.MaxPollWait`), below the idle timeout of common proxies."
- Effect: Change follows the runner protocol paths, credentials, poll, renew and heartbeat rules under ANY /runner/v1. Log upload and hash checks stay in add-job-session-paths-runner. Cross-instance wake stays in wake-long-poll-when-operation.
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
- Effect: Change refuses a request to create a TFE agent token with a JSON:API error that names POST /pools/{pool}/join-tokens of the runner API.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does the change need separate pools for Pulumi and OpenTofu? No. Server pool claim covers both tools; ADR 131 decision 4 says poll calls job.Claimer.Claim for jobs and sweep claimer for sweeps, and ADR 282 decision 1 accepts one runner design for both tools.
- Is the join token one-use with short life? Yes. ADR 133 decision 2 says one join, one-hour default, secret shown once with only SHA-256 kept, revokable.
- What happens when a runner dies during a job? Yes. ADR 131 decisions 7 and 8 give renew, heartbeat, lost-claim and takeover behavior; the milestone exit test kills a runner and expects lease expiry plus takeover by another runner.
- What lease length, renew interval and takeover delay does the server use? Provisional values for this change: claim lease 60 seconds, renew every 20 seconds, takeover after lease expiry plus 30 second grace. ADR 131 decision 5 gives the 50 second poll hold and 5 second recheck but no lease length, so this change fixes lease values in code and guide and surfaces effective values on the pool read.
- How does a runner recover when the join response is lost after the store insert? Retry with the same token always gets token-used. The first runner record stays for audit with no live claim. The operator retires the orphan record and creates a fresh token. ADR 133 decision 2 states one-use consume with token-used on second join and no retry rule.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
