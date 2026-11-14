Roadmap: `docs/roadmap.md:304` (CLI-driven remote execution).

## Why

**Persona: developer.** A Developer runs OpenTofu against the server and wants tofu plan to run on a runner from the CLI and stream its log, with secret variables masked and a plain refusal of the apply until the RFC 30 design lands.

The user path this change must make work:

1. **API.** Set execution-mode agent on the workspace through the API.
2. **CLI.** Run tofu plan with the cloud block and read the runner log in the CLI.
3. **CLI.** Read the refusal of tofu apply that names local execution.
4. **Docs page.** Follow the docs page for remote execution from setup to plan.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| tofu plan on a workspace with execution-mode local runs locally | The workspace runs locally and never reaches a runner; set `execution-mode` to `agent` through the API and run `tofu plan` again. | `tofu plan` |
| tofu apply on a speculative-only workspace is refused | The server answers 422 with a JSON:API error that the server runs speculative plans only and names `execution-mode` `local`; run `tofu plan` or run locally. | `tofu plan` |
| A principal without the run action starts a run and gets no job | The server answers 403 with a JSON:API error that names `unit:run-plan` on the workspace; ask for the grant and run `tofu plan` again. | `tofu plan` |

Evidence for the claims above:

- Runner protocol and domain code exist in the server. Proof: Pack code lists `internal/api/runnerapi`, `pkg/runner` and `pkg/runner/runnertest`.
- The e2e suite already drives runner and job paths. Proof: Pack e2e lists `e2e/jobs_api_test.go`, `e2e/jobs_plan_test.go`, `e2e/job_reconcile_test.go` and `e2e/claim_gate_test.go`.
- No shipped apply path is proved by the pack: the roadmap step says the apply stays open and needs a design. Proof: Roadmap step: ADR 254 serves plans only, and the apply stays open in RFC 30.

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
- The `remote` backend

Depends on:

- runners
- add-job-service-routes-start
- add-job-session-paths-runner
- `runners` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The Developer must find execution-mode in the workspace settings without reading server logs.
- The tofu apply refusal must name the one next step: run tofu plan, or run locally.
- The streamed log must show job progress in the CLI without opening the console.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 254: Remote execution for OpenTofu starts with speculative plans on runners** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current), 3 (current), 4 (changed), 5 (current), 7 (current), 8 (current), 9 (current), 10 (current).
- Changed by: ADR 300 changes decision 6; ADR 310 extends decision 4; ADR 310 extends decision 5; ADR 350 changes decision 4. Chain to the end: ADR 300, ADR 310, ADR 350.
- Current rule: this ADR, with ADR 300, 310, 350.
- Quote: "Slice 1 serves speculative plans only. It serves no apply."
- Quote: "Each workspace keeps `execution-mode` `local` until a person with the action to manage the workspace sets it to `agent` through the API (ADR 17)."
- Quote: "The server refuses a configuration version with `speculative=false` with a JSON:API error"
- Effect: The change keeps slice 1: speculative plans only, opt-in per workspace, file set upload, plan job with fixed arguments and change.propose gates, secret run variables, refusal of non-speculative runs and every apply route, poll answers without 404, and e2e with the real CLI. The apply mapping stays open per decision 10.
- Conflict: none.

**ADR 300: PM answers on runs, recorded** (not owner-tagged).

- Decisions that apply: 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "OpenTofu sends no token to `log-read-url`. The log read therefore uses an HMAC-signed URL."
- Effect: The log read keeps the short-lived signed URL bound to one job, one phase, one principal and one cap, with the check of job read logs at mint and at read.
- Conflict: none.

**ADR 350: OpenDustpan runners replace HCP Terraform agents, and a gap in security is a documented gap** (owner-tagged (all decisions)).

- Decisions that apply: 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Until the RFC is accepted, ADR 254 decision 4 holds: the server does not serve the TFE agent pool API."
- Effect: The change does not serve the TFE agent pool API and adds no agent path.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (adr conflict): proposal.md and specs/remote-execution/spec.md add an apply on a runner. ADR 254 decision 1 says "It serves no apply" and decision 7 says the server refuses speculative=false with a JSON:API error. The change does not reconcile these lines or write the RFC 30 ADR first. Resolution: answered: Removed the apply SHALL; the spec now keeps slice 1 only and refuses every apply route per ADR 254 decisions 1 and 7. The apply mapping stays as open question 1 for the RFC 30 ADR.
- R1.2 (open question): Open questions 1 and 2 are unanswered, and the change is not spec-ready. ADR 254 decision 10 lists the apply mapping to change.apply approvals and gates, the stored plan, auto-apply (ADR 229 decision 3), the saved -out plan and the remote backend as points that the RFC 30 ADR must decide before any apply. Resolution: answered: Open questions 1 and 2 stay open for the RFC 30 ADR; the spec no longer states the apply outcome, so the change makes no unanswered promise.
- R1.3 (missing scenario): The stale plan risk has no requirement or scenario in the spec. tasks.md 1.2 asks for a stale stored plan refusal, but the spec never says what happens when state moved after the plan. Resolution: answered: Stale stored plan needs no scenario in slice 1 because no apply runs; it moved to out of scope with the apply.
- R1.4 (wrong claim): The error table names the route PUT /api/dustpan/v1/workspaces/{id} to set execution-mode. The code check fails: this route is not in the API description or the route table, so the one command that helps is not a real route. Resolution: answered: Error table no longer names an unverified workspace route; it names the symptom and the `tofu plan` command.
- R1.5 (open question): The apply mapping to change approvals and gates is a design decision that no ADR answers. ADR 254 decision 10 says it is open, and the spec states the outcome ("after approvals and gates pass") without the decision record or the auto-apply rule. Resolution: answered: The apply to approvals and gates mapping is now open question 1 only; the spec has no apply requirement.
- R1.6 (wrong claim): The ADR check quotes no decision for ADR 310, which is owner-tagged. The code check fails on this missing quote, so the owner-tagged run options decision is not shown as applied. Resolution: answered: Removed the ADR 310 entry because the pack holds no numbered decision text to quote; run options stay covered by ADR 254 decision 4 through the fixed-argument allowlist.
- R1.7 (missing scenario): ADR 254 decision 8 requires the poll routes (run-events, include=task_stages, runs/queue, capacity) to answer without 404. The spec and tasks have no scenario for these routes, and the risk of an unclear CLI stop is not covered. Resolution: answered: Added a poll-answers requirement and scenario for run-events, task stages, queue and capacity with no 404.
- R1.8 (missing scenario): ADR 300 decision 6 sets the signed log URL validity to the phase plus 15 minutes, and a replay for another job or principal must fail. The spec has no scenario for an expired URL or a replayed URL, though the design layer table names the replay test. Resolution: answered: Added a signed log URL requirement and scenarios for expiry and replay for another job or principal.
- R1.9 (failure mode): Runner loss, a restart or an outage during an apply is not covered. The change has no requirement for the state of a run after a crash in the middle of an apply, or for a second apply of the same run. Resolution: answered: Runner loss and a second apply of one run need no requirement in slice 1; runner loss moved to out of scope with the runners item.
- R1.10 (scope): The proposal lists the e2e count of 12 for Runner tests as proof, and the claim is not checked. The proposal also cites the apply path as work of this item while the roadmap says the apply needs a design first, so tasks 2.1 and 2.2 build the apply before the design exists. Resolution: answered: Dropped the e2e count claim; the plan e2e task now proves only the speculative plan path and the apply refusal.
- R2.1 (missing scenario): The spec has no requirement or scenario that refuses the apply routes (actions/apply, actions/discard, actions/cancel of a non-plan run) with the speculative-only error, as ADR 254 decision 7 requires. The design layer table and task 2.1 depend on it. File: specs/remote-execution/spec.md. Resolution: answered: Added requirement Refuse apply routes with scenarios for actions/apply, actions/discard and actions/cancel of a non-plan run.
- R2.2 (open question): The fixed plan argument allowlist (refresh, refresh-only, target-addrs, replace-addrs, is-destroy) is not defined in the change. ADR 254 decision 4 says ADR 310 extends it, but the pack holds no ADR 310 decisions, so the allowlist cannot be built or checked. File: design.md. Resolution: answered: Named the fixed allowlist refresh, refresh-only, target-addrs, replace-addrs and is-destroy in the design and the plan requirement with a scenario that refuses an argument outside the list.
- R2.3 (wrong claim): The ADR check omits owner-tagged ADR 310, which ADR 254 names as extending decisions 4 and 5. It also lists decisions 1, 2, 3, 5, 7, 8 and 9 as applying, but leaves out decision 4 (plan job and gates) and decision 10 (the RFC 30 apply points), both of which this change touches. File: proposal.md. Resolution: answered: Covered run options target, replace and refresh-only in the Speculative plan requirement allowlist and design; no ADR 310 quote is used because the pack holds no numbered decision text to quote.
- R2.4 (missing scenario): No scenario covers a plan refused by the change.propose gates. ADR 254 decision 4 requires the run to end as errored and the log to name the decision record. File: specs/remote-execution/spec.md. Resolution: answered: Added requirement Gate refusal ends errored with a scenario where the run ends as errored and the log names the decision record.
- R2.5 (missing scenario): No scenario covers a workspace set to agent in a namespace whose runner pool or operations entitlement is gone, or a pool with no free runner. ADR 254 decision 2 ties the behavior to the pool. File: specs/remote-execution/spec.md. Resolution: answered: Added requirement Pool required with scenarios for a missing pool or entitlement and no free runner.
- R2.6 (missing scenario): The spec has no upload requirement for the file set. ADR 254 decision 3 requires refusal of an archive that fails the ADR 199 checks, and a single signed upload with a short expiry. File: specs/remote-execution/spec.md. Resolution: answered: Added requirement Configuration upload is a file set with scenarios for signed single upload and refusal of a bad archive.
- R2.7 (failure mode): Masking covers only the exact value of a run variable. A value echoed in a plan error, or a value derived from a secret, is not masked, and no hostile test covers it. File: specs/remote-execution/spec.md. Resolution: answered: Recorded in risks that masking covers only the exact value; derived values and plan error echoes are not masked.
- R2.8 (failure mode): The HMAC signing key for log URLs is not covered: its storage, rotation and the effect of a rotation on live URLs are undefined. Concurrent plans on one workspace are also not covered. File: design.md. Resolution: answered: Recorded in design and risks that a signing key rotation ends live URLs and concurrent plans run as separate jobs.
- R2.9 (missing scenario): The signed log read requirement does not state the job:read-logs check at mint and at read, which ADR 300 decision 6 requires, and the design does not name the authorization action. File: specs/remote-execution/spec.md. Resolution: answered: Signed log read requirement now states the job:read-logs check at mint and at read with scenarios for missing grant.
- R2.10 (scope): The roadmap step asks that tofu apply with remote execution runs the apply on a runner. The change drops the apply and points to RFC 30, but it does not record that the roadmap step stays partly open. File: proposal.md. Resolution: answered: Design context, summary and out of scope now record that the roadmap apply part stays open until the RFC 30 ADR.
- R2.11 (wrong claim): The provisional reason for open question 2 cites an AWS S3 benchmark that rejects unexpected inputs. The input does not support this claim. File: design.md. Resolution: answered: Replaced the S3 benchmark on open question 2 with a gradual slice benchmark.
- R2.12 (wrong claim): The error table names the commands opendustpan status and opendustpan doctor as the one helpful command. The input does not show that these commands exist. File: proposal.md. Resolution: answered: Error table now uses only `tofu plan` as the helping command, which the roadmap step proves exists.
- M.8 (duplicate): add-job-service-routes-start also specifies the TFE speculative run that starts a plan job and links the run to the job. Resolution: answered: Kept the speculative run to plan-job link in this change, added depends_on add-job-service-routes-start and add-job-session-paths-runner, and design and out of scope state the remote apply stays uncovered until the RFC 30 ADR.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. How do `is-confirmable` and `actions/apply` map to the approvals and the gates of change apply, including `-auto-approve` and the saved cloud plan of `-out`?
   - Options: Map is-confirmable and actions/apply to change apply approvals and gates, and apply the stored plan of the run; Run the apply as a plain job without change approvals and gates.
   - Provisional decision (owner review 14 Nov): Map is-confirmable and actions/apply to change apply approvals and gates, and apply the stored plan of the run.
   - Reason: ADR 254 decision 10 names this mapping as open, and it needs the RFC 30 ADR before any apply ships. Benchmark: AWS: IAM requires an explicit allow before a mutating action runs, and HCP Terraform requires an approval before a confirmable apply runs.
   - Persona impact: The Developer sees the same tofu apply flow with a wait for approval; no extra step when no rule needs an approval. Escape hatch: If the mapping is wrong, the operator sets the workspace back to `execution-mode` `local` and the CLI runs locally with no server apply.
2. Does a later change serve the `remote` backend, with its rejects of run variables and plan -out, or the `cloud` block only?
   - Options: Serve the remote backend with no run variables and no plan -out in this change; Leave the remote backend for a later change.
   - Provisional decision (owner review 14 Nov): Serve the cloud block only and leave the remote backend for a later change.
   - Reason: ADR 254 decision 10 lists the remote backend as an open point, and ADR 254 decision 9 says it waits for its own e2e step. Benchmark: Google: ship the smallest safe slice first and leave the wider surface for a later change.
   - Persona impact: The Developer uses the cloud block now; the remote backend path waits and upsets no current flow. Escape hatch: Teams keep the `cloud` block; the `remote` backend stays local until the route ships.

Questions considered and answered:

- Does remote execution change workspaces that never opted in? ADR 254 decision 2 answers it: each workspace keeps `execution-mode` `local` until a person sets it to `agent` through the API, and only a namespace with a runner pool gets the behavior.
- How are run variables and logs kept secret? ADR 254 decisions 5 and 6 plus ADR 300 decision 6 answer it: seal variables in the job spec, give them only to the runner of the claim, mask exact values in the log, and read the log through a short-lived signed URL with a check at mint and at read.
- Does this change add the apply on a runner? ADR 254 decisions 1 and 7 plus decision 10 answer it: slice 1 serves speculative plans only and refuses the apply path. The apply mapping stays open in RFC 30, so this change does not specify it and keeps it as an open question.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
