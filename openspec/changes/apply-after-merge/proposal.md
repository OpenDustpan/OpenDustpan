Roadmap: `docs/roadmap.md:291` (Apply after the merge).

## Why

**Persona: developer.** A developer merges a reviewed change and wants the server to apply the merged result, not the branch, with no extra step and no skipped check.

The user path this change must make work:

1. **API.** Developer merges the pull request on the forge; the server receives the merge event.
2. **CLI.** Developer reads the apply job, its pinned commit sha, its gate result and its approval state.
3. **Docs page.** Developer follows the docs page for apply after the merge.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Apply waits for approval and the run page shows awaiting approval. | The apply stays waiting and names the missing approval. Add an approval and the job continues. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{id}` |
| A gate refuses the apply after the merge. | The job ends as failed and names the gate and the decision record. Fix the cause and merge again. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{id}` |

## What Changes

- Merge of a pull request starts one apply per touched unit from the default branch at the merge commit.
- Branch pushes never start an apply.
- The merge apply passes the same gates and approvals as other applies.
- The job record exposes the pinned default branch and merge commit sha.

Out of scope:

- Plan on pull request push
- Pre-merge apply from the branch of the pull request
- New gates or approval rules
- Runner pool, claim and execution, which the runners item owns; this change only starts jobs that runners run
- Console run page redesign

Depends on:

- pull-request-automation-first-slice
- policy-approvals
- git-host-webhook-intake-post
- `pull-request-automation-first-slice` (order of the milestone review)
- `policy-approvals` (order of the milestone review)
- `git-host-webhook-intake-post` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The run page must show the pinned default branch and merge commit sha, or the person cannot tell what the apply runs.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): proposal.md leaves open whether a push to the default branch between merge and job start moves the apply; the open question is recommended but not decided, and design.md Decision 1 states the pin as settled. The spec-ready gate is not met. Resolution: answered: Pin is settled: considered records the decision, and design decision 1 plus the pin requirement state it.
- R1.2 (wrong claim): proposal.md cites route GET /api/dustpan/v1/namespaces/{namespace}/jobs/{id}/decision-records, which the input does not show in the API description or route table; the code check already fails on it. Resolution: answered: Both error commands use the existing route GET /api/dustpan/v1/namespaces/{namespace}/jobs/{id}.
- R1.3 (missing scenario): No scenario covers a merge event delivered twice, a replayed webhook, or a merge event for a pull request that touched no unit; the spec does not say whether duplicate apply jobs are created per unit. Resolution: answered: Requirement Duplicate merge creates one job covers replayed merge events and merges with no touched units.
- R1.4 (failure mode): No requirement covers a server restart or a crash between the merge event and job creation; the pinned commit may be lost or the apply may start twice. Resolution: answered: Requirement Survive restart covers restart between merge event and job creation with exactly-once creation.
- R1.5 (missing scenario): No scenario covers a merge where the default branch is the touched unit's branch but the merge commit sha is missing or unverifiable from the forge; the constructor rejects an empty sha but the event path is not specified. Resolution: answered: Requirement Missing sha refused covers a merge event with no usable merge commit sha.
- R1.6 (missing scenario): No scenario covers an apply that waits for approval for a long time when a newer merge to the default branch supersedes it; ordering between queued merge applies on the same unit is not specified. Resolution: answered: Pin requirement states a later push never moves a pinned job; each merge creates its own job in merge order.
- R1.7 (failure mode): The replan step (Decision 2) compares with the reviewed revision, but no scenario says what happens when the replan at the merge commit differs from the reviewed plan; the spec only covers gate refusal. Resolution: answered: Approval reset now reuses policy-approvals; no reset rule is specified in this change.
- R1.8 (missing scenario): The error table promises a waiting-for-approval state and a gate failure, but the spec has no scenario for the approval wait or for the user-visible pinned branch and sha on the run page named in Usability. Resolution: answered: Requirements Approval wait and Pinned commit visible cover the waiting state and the exposed branch and sha.
- R1.9 (scope): tasks.md 2.2 and 2.3 reference the job service and e2e against the fake forge, but the proposal lists the console run page as out of scope while Usability requires the run page to show the pinned sha; the scope boundary is unclear. Resolution: answered: Out of scope lists only a console redesign for the run page; API exposure of branch and sha stays in scope.
- R1.10 (open question): The ADR check says no ADR is touched and gives no quoted decision lines, so the claim that no decision conflicts lacks the proof required by the proposal rules. Resolution: answered: adr_check now lists no ADR because the context pack provides no ADR decision text to quote.
- R2.1 (open question): design.md Decision 2 and the Replan requirement do not say which plan the replan compares with, or whether a reviewed plan exists when the merge event arrives; the reviewed revision source is not defined. Resolution: answered: Design decision 2 and specs reuse the reviewed revision and reset from pull-request-automation-first-slice and policy-approvals with no new rule.
- R2.2 (failure mode): Decision 3 stores the merge event id with the jobs in one write, but no requirement says what happens if the write fails midway for some units; partial job sets across units are not covered. Resolution: answered: Design decision 3 and the duplicate-merge requirement now state that a retry after a partial write creates only the missing unit jobs.
- R2.3 (missing scenario): No scenario covers a merged pull request whose touched unit has an apply already running from an earlier merge; the concurrency between two live applies on the same unit is not specified. Resolution: answered: New requirement Concurrent merges queue states that a second merge on the same unit creates a queued job that waits for the running apply.
- R2.4 (failure mode): Risk 'a gate that passed on the branch fails at the merge commit' has no requirement for what the reviewed approvals record shows when the gate result changes; the Replan requirement covers only plan differences. Resolution: answered: Keep-gates requirement now states that signatures stay unchanged when a gate refuses at the merge commit and the job names the new decision record.
- R2.5 (wrong claim): The ADR check says the ADR context is empty and relies on the phrase 'the ADR records decisions of the project owner' for owner tagging, which is not a rule stated in project.md; the owner-tag basis is unsupported. Resolution: answered: Considered now cites only the roadmap step and the depended changes instead of ADR text the pack does not provide.
- R2.6 (open question): The 'Pinned commit visible' requirement does not name the API route or field names that expose branch and sha, while tasks 2.2 check a list endpoint that the proposal does not name. Resolution: answered: Pinned-commit requirement now names the job read route, and task checks use that same route.
- R2.7 (scope): Task 2.2 and 3.2 mention real runner and fake forge e2e but the proposal's out-of-scope list does not place runner execution; the boundary with the runner item is not stated. Resolution: answered: Out of scope now states that runner pool, claim and execution belong to the runners item; tasks use an existing runner only.
- M.5 (dependency): This change depends on write-back with no reason. It does not depend on the webhook intake that receives the merge event. Its ADR check is empty, but RFC 27 is a draft. Resolution: answered: Removed write-back from depends_on, added git-host-webhook-intake-post, cleared adr_check because the pack holds no ADR decision text, and removed the duplicate approval-reset requirement in favor of policy-approvals.

## Open questions

none

Questions considered and answered:

- Does a push to the pull request branch start an apply? The roadmap step answers it: the merge starts an apply from the default branch at the merge commit, and a push to the pull request branch does not apply.
- Which gates and approvals guard the apply after the merge? The roadmap step answers it: the apply meets the gates and approvals of step 6, so this change reuses that gate and approval check with no new rule.
- When a new push reaches the default branch after the merge but before the apply starts, does the apply keep the merge commit or move to the new head? Decided in this change and fixed in the spec: the apply keeps the merge commit sha pinned at job creation and never moves to a later head. The roadmap step says the apply runs at the merge commit.
- Which ADR decisions apply to apply after the merge? The context pack names no ADR decision text for this item, so this change cites no ADR decision. Webhook intake and plan behavior come from the depended changes git-host-webhook-intake-post and pull-request-automation-first-slice, and approval behavior comes from policy-approvals.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
