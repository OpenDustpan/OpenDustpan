---
version: 2
---

Roadmap: `docs/roadmap.md:304` (CLI-driven remote execution).

## Why

**Persona: developer.** A Developer runs OpenTofu against the server and wants tofu plan to run on a runner from the CLI and stream its log, with secret variables masked and a plain refusal of the apply until the apply design is decided.

The user path this change must make work:

1. **API.** Set execution-mode agent on the workspace through the API.
2. **CLI.** Run tofu plan with the cloud block and read the runner log in the CLI.
3. **CLI.** Read the refusal of tofu apply that names local execution.
4. **Docs page.** Follow the docs page for remote execution from setup to plan.
5. **E2e.** Run the real OpenTofu CLI against a workspace with execution-mode agent and a test runner.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| tofu plan on a workspace with execution-mode local runs locally | The workspace runs locally and never reaches a runner. Set execution-mode to agent through the API and run the plan again. | `tofu plan` |
| tofu apply on a speculative-only workspace is refused | The server answers with a JSON:API error that says it runs speculative plans only and names local execution. Run a plan, or run locally. | `tofu plan` |
| A principal without the run grant starts a run and gets no job | The server refuses the run and names the run grant on the workspace. Ask for the grant and run the plan again. | `tofu plan` |

Evidence for the claims above:

- Runner protocol and domain code exist in the server. Proof: Pack code lists internal/api/runnerapi, pkg/runner and pkg/runner/runnertest.
- The e2e suite already drives runner and job paths. Proof: Pack e2e lists e2e/jobs_api_test.go, e2e/jobs_plan_test.go, e2e/job_reconcile_test.go and e2e/claim_gate_test.go.
- The workspace routes of the TFE-compatible API exist. Proof: Pack main lists the routes /api/v2/organizations/{}/workspaces/{} and /api/v2/workspaces/{}.
- The apply on a runner is not proved by the pack: the roadmap step says the apply needs a design. Proof: Roadmap step: ADR 254 serves plans only, and the apply stays open in RFC 30.

## What Changes

- Speculative plan of an agent workspace runs as a plan job on a runner with fixed arguments and the signed log read.
- Run variables stay sealed, reach only the runner of the claim, and never reach a log, an audit entry or an answer.
- Poll routes answer without 404 during a remote plan, and every apply route keeps the speculative-only refusal.

Out of scope:

- Runner pools, claims, leases, loss of a runner during a job
- Profiles and secrets providers
- Stored plans and the apply on a runner, so the roadmap apply part stays open
- Pull request automation and apply after the merge
- Console run page
- The remote backend

Depends on:

- add-job-service-routes-start-1
- add-job-session-paths-runner-2
- `runners` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The Developer must find execution-mode in the workspace settings without reading server logs.
- The tofu apply refusal must name the one next step: run a plan, or run locally.
- The streamed log must show job progress in the CLI without opening the console.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Workspace lock mismatch refusal exists. (`internal/errcode/catalog_workspace.go:12`)
- Workspace locked refusal exists. (`internal/errcode/catalog_workspace.go:15`)
- Runner not-found refusal exists. (`internal/errcode/catalog_runner.go:16`)
- Context of a workspace survives a rename in tests. (`pkg/dustpan/context_test.go:504`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 254: Remote execution for OpenTofu starts with speculative plans on runners** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current), 3 (current), 4 (changed), 5 (current), 7 (current), 8 (current), 9 (current), 10 (current).
- Changed by: ADR 300 changes decision 6; ADR 310 extends decision 4; ADR 310 extends decision 5; ADR 350 changes decision 4. Chain to the end: ADR 300, ADR 310, ADR 350.
- Current rule: this ADR, with ADR 300, 310, 350.
- Quote: "Slice 1 serves speculative plans only. It serves no apply."
- Quote: "Each workspace keeps `execution-mode` `local` until a person with the action to manage the workspace sets it to `agent` through the API (ADR 17)."
- Quote: "The server refuses a configuration version with `speculative=false` with a JSON:API error"
- Effect: The change keeps slice 1: speculative plans only, opt-in per workspace, file set upload, plan job with change.propose gates, secret run variables, refusal of every apply route, poll answers without 404, and e2e with the real CLI. The apply mapping stays open per decision 10.
- Conflict: none.

**ADR 300: PM answers on runs, recorded** (not owner-tagged).

- Decisions that apply: 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "OpenTofu sends no token to `log-read-url`. The log read therefore uses an HMAC-signed URL."
- Effect: The log read keeps the short-lived signed URL bound to one job, phase, principal and cap, with the check of job read logs at mint and at read.
- Conflict: none.

**ADR 350: OpenDustpan runners replace HCP Terraform agents, and a gap in security is a documented gap** (owner-tagged (all decisions)).

- Decisions that apply: 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Until the RFC is accepted, ADR 254 decision 4 holds: the server does not serve the TFE agent pool API."
- Effect: The change does not serve the TFE agent pool API and adds no agent pool path.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. The roadmap step says tofu apply runs the apply on a runner, but the apply design is open. Does the owner accept this change as the plan slice only, with the apply as a later item?
   - Options: Accept the plan slice now and split the apply into a later item after the apply ADR; Hold this change until the apply design is accepted.
   - Provisional decision (owner review 14 Nov): Accept the plan slice now and split the apply into a later item after the apply ADR.
   - Reason: The apply needs decisions on approvals, gates, stored plan and auto-approve that no ADR makes yet. The plan slice is safe alone and fails closed. Benchmark: Google: ship the smallest safe slice first and leave the wider surface for a later change.
   - Persona impact: The Developer gets remote plans now and a clear refusal of the apply; no current flow breaks. Escape hatch: The operator sets the workspace back to execution-mode local and the CLI runs locally.
2. How do is-confirmable and actions/apply map to the approvals and gates of change apply, including auto-approve and the saved plan of -out?
   - Options: Map them to change apply approvals and gates and apply the stored plan of the run; Run the apply as a plain job without approvals and gates.
   - Provisional decision (owner review 14 Nov): Map them to change apply approvals and gates and apply the stored plan of the run.
   - Reason: ADR 254 decision 10 names this mapping as open and it needs its own ADR before any apply ships. Benchmark: AWS: IAM needs an explicit allow before a mutating action runs; HCP Terraform needs an approval before a confirmable apply.
   - Persona impact: The Developer sees the same tofu apply flow with a wait for approval when a rule needs one. Escape hatch: Set the workspace back to execution-mode local.

Questions considered and answered:

- Does remote execution change workspaces that never opted in? ADR 254 decision 2: each workspace keeps execution-mode local until a person sets it to agent through the API.
- How are run variables and logs kept secret? ADR 254 decision 5 and ADR 300 decision 6: seal variables in the job spec, mask them in the log, read the log through a signed URL checked at mint and at read.
- Does this change add the apply on a runner? No. ADR 254 decisions 7 and 10 keep the apply refused and open for the apply design. This change records it as an open question for the owner.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
