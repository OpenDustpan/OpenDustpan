---
version: 4
---

Issue: #1213.

## Why

**Persona: developer.** A developer pushes to a review request and wants a fresh plan for each stack and workspace that the change touches, without starting plans by hand.

The user path this change must make work:

1. **API.** Save a review-request-push trigger on the stack or workspace, with an optional paths filter.
2. **E2e.** Push to a review request on the fake forge and read the plan job.
3. **Console.** Read the plan job, its head commit and its log.
4. **Docs page.** Read the docs page for push plans and the fork rule.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A push from a fork starts nothing. | No plan starts and an audit entry records the fork push. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers` |
| A push that touches no path of the filter starts nothing. | No plan starts. Compare the paths of the trigger with the changed files of the push. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}` |
| A trigger is saved with an empty, absolute or escaping paths pattern. | The save is refused with a request error that names the pattern. | `POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers` |

Evidence for the claims above:

- Trigger already stores LastSaver and LastSaverID. Proof: pkg/trigger/trigger.go:190
- No Dispatcher.ReviewRequest exists yet. Proof: rg -n -F --glob '!*_test.go' 'Dispatcher.ReviewRequest' pkg internal cmd matches 0
- The source policy package exists. Proof: pkg/source exists true

## What Changes

- Trigger gains the Paths field, which defaults to the working directory of the code location of the unit.
- Push intake matches units on repository and branch, filters on paths, refuses fork pushes with an audit entry and replaces the older plan of the same trigger.
- Started jobs record the source kind forge.

Out of scope:

- Schedule and follows triggers
- Pre-merge apply and apply after the merge
- The full fork code policy in pkg/source
- The plan comment and the commit status on the review request

Depends on:

- run-triggers
- git-host-webhook-intake-post
- add-job-service-routes-start-1
- `run-triggers` (order of the milestone review)
- `git-host-webhook-intake-post` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A push that starts nothing surprises the author, so the trigger detail shows the repository, the branch and the paths filter.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Trigger domain stores LastSaver and LastSaverID. (`pkg/trigger/trigger.go:190`)
- The trigger.Dispatcher port exists. (`pkg/trigger/ports.go:173`)
- Trigger test doubles build a Dispatcher. (`pkg/trigger/triggertest/doubles.go:47`)
- The make check target exists. (`Makefile:14`)
- The trigger store constructor exists. (`pkg/store/internal/triggerstore/triggerstore.go:110`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 267: Run triggers: three kinds, a plan only, started through the job service** (not owner-tagged).

- Decisions that apply: 9 (current), 15 (current), 19 (changed).
- Changed by: ADR 273 changes decision 14; ADR 299 changes decisions 14 and 19; ADR 307 extends decision 8; ADR 310 changes decision 22; ADR 338 changes decision 14; ADR 369 changes decision 14; ADR 370 changes decision 14. Chain to the end: ADR 273, ADR 299, ADR 307, ADR 310, ADR 338, ADR 369, ADR 370, ADR 286, ADR 328, ADR 359, ADR 368.
- Current rule: this ADR, with ADR 273, 299, 307, 310, 369, 370.
- Quote: "A trigger starts a plan and nothing else. The acceptance criteria name a plan for each kind. The port that a trigger uses to start a job can start a plan and nothing else."
- Quote: "A push records `forge`."
- Quote: "A unit matches when the review request targets the repository and the branch of its code location."
- Effect: The change starts only plans through the job service, records the source kind forge, and matches units on repository, branch and paths.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request. A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Effect: The change ships its docs page in the same pull request.
- Conflict: none.

## Open questions

Questions considered and answered:

- How does a push match units and what does it start? ADR 267 decisions 9, 15 and 19 answer it: a plan only, through the job service, source kind forge, match on repository and branch with the paths default.
- Does the change need new tooling? No. It is a field and a matcher in the trigger domain with conformance cases.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
