Roadmap: `docs/roadmap.md:643` (A runner job with an overlapping claim is refused).

## Why

**Persona: developer.** Developer changes one workspace while another team applies to the same workspace through runners. The developer needs a fast refusal that names the blocking claim, so no work is lost and no partial apply runs.

The user path this change must make work:

1. **API.** Developer starts runner job C on the same workspace while job A applies. Job C registers its claim on the register route of the dependency item.
2. **API.** Server refuses claim C with 409 that names blocking claim A, and moves job C to blocked. Developer reads the refusal with a job read.
3. **Docs page.** Developer reads the guide for refused claims and blocked jobs.
4. **E2e.** BLOCKED by the register route and the claim record of two-disjoint-claims: the e2e step cannot name its route and credential kind until that item ships. It then runs two runner jobs with real OpenTofu and a fake provider, with the job token of each runner phase, and checks that the provider log has no call from job C.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Claim refused because it overlaps a running claim | The server answers 409 with the blocking claim ID and the overlapping resource address. The job ends as blocked and makes no provider call. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job}` |

Evidence for the claims above:

- Runner code lives in the paths that the pack names. Proof: Pack keyword evidence for Runner lists internal/api/runnerapi, pkg/runner and pkg/runner/runnertest.
- An e2e file for the claim gate and e2e files for jobs exist. Proof: Pack keyword evidence for Runner lists e2e/claim_gate_test.go, e2e/jobs_api_test.go and e2e/jobs_plan_test.go.
- No claim record exists in main for this change. Proof: The pack lists no code_facts and no related_changes for a claim record in main.

## What Changes

- The server refuses a runner claim that overlaps an accepted claim of the same workspace, before any apply starts.
- The refusal names the blocking claim ID and the overlapping resource address.
- A refused runner job moves to status blocked and makes no provider call.
- The refusal writes an audit entry with both claim IDs.
- The block ends when the accepted claim completes, fails, or loses its lease.
- A docs page describes the refusal, the blocked status and the next step.

Out of scope:

- The claim record model and the register route (two-disjoint-claims)
- The merge of uploads (concurrent-applies-merge-core)
- History and audit of merges (concurrent-applies-merge-history-audit)
- The cases that keep the whole-unit lock (strict-case-takes-turns)
- The retry of the local wrapper (local-run-two-cases)
- A local run without the wrapper (local-run-without-wrapper)
- Pulumi CLI claims

Depends on:

- ADR 368 on main (pull request #3052)
- two-disjoint-claims
- `two-disjoint-claims` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A refusal without the blocking claim ID leaves the developer unsure which team blocks the apply.
- A blocked job without a stated next step leaves the developer unsure whether to wait or to start a new job.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The claim gate e2e step exists and is the baseline for claim behavior. (`e2e/claim_gate_test.go:49 (aGateStopsAClaim)`)
- The runner protocol HTTP adapter exists. (`internal/api/runnerapi/runnerapi.go:193 (NewHandler)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 368: Concurrent applies: apply claims, the server serial, fences and the closure test** (owner-tagged (all decisions)).

- Decisions that apply: 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The one `ConflictChecker` serves both the review requests of ADR 299 and the apply claims."
- Quote: "When the check cannot prove an edge, the answer is a conflict."
- Effect: The overlap check uses the closure test for a replacement. Until the closure test is built, an unproven closure is a conflict.
- Conflict: none.

**ADR 302: Milestone 8: concurrent applies on one state** (owner-tagged (all decisions)).

- Decisions that apply: 2 (current), 6 (current).
- Changed by: ADR 368 extends decision 6. Chain to the end: ADR 368.
- Current rule: this ADR, with ADR 368.
- Quote: "The server refuses overlapping claims before anything runs."
- Quote: "A runner job turns the refusal into the status "blocked"."
- Quote: "The code of milestone 8 comes after the project owner accepts that RFC as an ADR."
- Effect: The change follows the refusal rule of decision 2: overlapping claims are refused before apply and a refused runner job ends as blocked. ADR 368 answers the RFC gate of decision 6 for this change.
- Conflict: none.

**ADR 26: OpenTofu state** (not owner-tagged).

- Decisions that apply: 7 (current).
- Changed by: ADR 31 changes; ADR 44 changes decision 3; ADR 44 extends decision 6; ADR 45 changes decisions 4 and 5; ADR 220 changes decision 2. Chain to the end: ADR 31, ADR 44, ADR 45, ADR 220, ADR 35, ADR 221, ADR 281, ADR 239, ADR 335, ADR 62, ADR 273, ADR 286, ADR 363, ADR 272, ADR 340, ADR 364.
- Current rule: this ADR, with ADR 31, 44, 45, 220.
- Quote: "An unfenced blob write can leave an orphan blob. It can never publish a version."
- Effect: The overlap check and the claim insert run in one fenced transaction, so no unfenced write publishes a version.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- What happens when a runner job registers a claim that overlaps a running claim? ADR 302 decision 2: the server refuses the overlap before anything runs and the runner job ends as blocked.
- Does a refused runner job retry on its own? ADR 302 decision 2 gives the retry only to the local wrapper. A refused runner job stays blocked; the person starts a new job.
- Does this change cover Pulumi stacks? ADR 302 decision 3: OpenTofu is first, so this change covers workspaces only.
- What happens to a claim that meets a case that keeps the whole-unit lock? ADR 302 decision 2: work that the server cannot prove disjoint takes turns. The item strict-case-takes-turns specifies those cases. This change refuses only claims that name the same resource instance.
- What gates the code of this change? ADR 368 on main, and the code checks of the main facts.
- Does a replacement count as an overlap of its instance address? No. ADR 368 decision 6: one closure test in the one `ConflictChecker` decides. A closure that the check cannot prove is a conflict, so a replacement is a conflict until the closure test is built.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
