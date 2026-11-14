---
version: 2
---

Issue: #1213.

## Why

**Persona: developer.** A developer pushes to a review request and wants a fresh plan for each stack and workspace the change touches without starting plans by hand.

The user path this change must make work:

1. **API.** Save a review-request-push trigger on the stack or workspace with its paths filter.
2. **E2e.** Push to the review request on the fake forge.
3. **Console.** Read the plan job, its head commit, and its log.
4. **Docs page.** Read the docs page for push plans and the fork rule.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A push from a fork starts nothing. | No plan starts and an audit entry records the fork push. Check the trigger target repository and the fork flag from the forge event. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers` |
| A push that touches no path of the filter starts nothing. | No plan starts. Check the paths of the trigger against the changed files of the push. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}` |

Evidence for the claims above:

- Trigger already stores LastSaver and LastSaverID. Proof: pkg/trigger/trigger.go:190
- No Dispatcher.ReviewRequest port exists yet. Proof: rg -n -F --glob '!*_test.go' 'Dispatcher.ReviewRequest' pkg internal cmd matches 0
- The source policy package exists at pkg/source. Proof: pkg/source exists true

## What Changes

- Trigger gains the Paths field with default to the working directory of the unit code location.
- Push intake matches units on repository and branch, filters on paths, refuses fork pushes with an audit entry, and replaces the older plan of the same trigger.
- Started jobs record source kind forge and run author code with unit profile credentials.

Out of scope:

- Schedule and follows triggers
- Pre-merge apply and apply after merge
- Full fork code policy in pkg/source beyond refuse and audit
- Plan comment and commit status on the review request

Depends on:

- run-triggers
- git-host-webhook-intake-post
- add-job-service-routes-start-1
- `run-triggers` (order of the milestone review)
- `git-host-webhook-intake-post` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A push that starts nothing surprises the author; the trigger detail shows the matched repository, branch, and paths filter.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Trigger domain stores LastSaver and LastSaverID with validation. (`pkg/trigger/trigger.go:190`)
- trigger.Dispatcher port exists for trigger starts. (`pkg/trigger/ports.go:173`)
- Trigger test doubles expose a Dispatcher over store, starter and fences. (`pkg/trigger/triggertest/doubles.go:47`)
- Trigger store constructor exists. (`pkg/store/internal/triggerstore/triggerstore.go:110`)
- The make check target exists. (`Makefile:14`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 267: Run triggers: three kinds, a plan only, started through the job service** (not owner-tagged).

- Decisions that apply: 1 (current), 9 (current), 14 (changed), 15 (current), 19 (changed).
- Changed by: ADR 273 changes decision 14; ADR 299 changes decisions 14 and 19; ADR 307 extends decision 8; ADR 310 changes decision 22; ADR 338 changes decision 14. Chain to the end: ADR 273, ADR 299, ADR 307, ADR 310, ADR 338, ADR 286, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 273, 299, 307, 310, 338.
- Quote: "A trigger belongs to one unit and has one kind. The unit is the unit that the trigger starts. The kinds are `review-request-push`, `schedule` and `follows` (24.1). A unit is a stack or a workspace of any tool, and the tr"
- Quote: "A trigger starts a plan and nothing else. The acceptance criteria name a plan for each kind. The port that a trigger uses to start a job can start a plan and nothing else. No trigger starts an apply in these slices (RFC"
- Quote: "The principal of a start that no person makes (question 3). - The start runs as the namespace service principal. That principal holds only the action that starts a plan. - For a job that a trigger starts, check 2 of ADR"
- Quote: "The source kinds (question 4). A `schedule` trigger records the source kind `schedule`. A push records `forge`. A `follows` start records the new source kind `trigger`. A manual fire keeps the source kind of the caller ("
- Quote: "A push to a review request (question 8). - A unit matches when the review request targets the repository and the branch of its code location. - The list `paths` of the trigger defaults to the working directory of the cod"
- Effect: The change starts only review-request-push plans through the job service with source kind forge, repository and branch match, paths filter, start as the namespace service principal, and profile credentials by attachment authority.
- Conflict: none.

**ADR 338: The namespace service principal holds a stored grant** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The principal still holds nothing else (owner, 2026-11-07). The grant covers the start of a plan only. The principal still has no token and no session. The rule of ADR 267 decision 14 about the read check of the last saver does not change."
- Effect: The push start runs as the namespace service principal with only the stored start-of-plan grant; the job gets unit profile credentials through the profile attachment authority and not through the principal token.
- Conflict: none.

**ADR 273: Output sharing: the read-outputs action and sharing rules on the producer** (not owner-tagged).

- Decisions that apply: 5 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "**A job token reads only what a rule names and the starter can read (15.5, option c).** A job token reads the outputs of another unit only when both checks pass at each read:"
- Effect: A push-started job reads producer outputs only when a sharing rule names it and the last saver of the trigger holds the read actions.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request. A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Effect: The change ships the user docs page for push plans in the same pull request.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: main_facts[4]: "where" is not file:line (func Name); a code check fails: task "Add failing dispatcher tests for push intake." runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part>"; a code check fails: task "Implement Paths, Dispatcher.ReviewRequest, forge source kind, service-" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part; a code check fails: and 5 more.

Questions considered and answered:

- How does a push match units and what does it start? ADR 267 decisions 1, 9, 14, 15 and 19 answer it as changed by ADR 273, 299, 338: one review-request-push trigger per unit, plan only through the job service, source kind forge, match on repository and branch with paths default, start as the namespace service principal.
- Which principal starts the job and which grants decide its reads? ADR 338 decision 3 and ADR 273 decision 5 answer it: the namespace service principal starts the plan with only the stored start grant, output reads use the last saver of the trigger, and profile credentials come from the attachment authority.
- Which field gives the fork flag and what happens when the flag is missing? The Forge port gives the fork flag from head repository versus base repository; a missing flag fails closed with refusal and audit.
- How are push deliveries authenticated and deduplicated? The Forge port authenticates each delivery by signature with delivery-ID deduplication before the dispatcher runs; a failed check starts nothing.
- What does the dispatcher do when the forge payload truncates the changed-file list? It matches every trigger of the same repository and branch within the open-job limit and a per-push cap, and records the truncation on the job.
- Does the change need new tooling? No. The fix is a field and a matcher in the trigger domain with conformance cases, checked by make check and an e2e step against a fake forge.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
