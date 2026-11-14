---
version: 2
---

Roadmap: `docs/roadmap.md:287` (Pull request automation, first slice).

## Why

**Persona: developer.** Developer pushes a change for review and wants a plan for each touched stack or workspace with one status and one comment to read.

The user path this change must make work:

1. **E2e.** Push code and open a review request on the fake GitHub API, which sends a signed webhook delivery to the server.
2. **API.** List the stacks and workspaces of the namespace with GET /api/dustpan/v1/namespaces/{namespace}/units, using an access token of a principal that can read units.
3. **E2e.** Read the one commit status and the one summary comment on the fake GitHub API, then push again and read the same edited comment.
4. **Docs page.** Read the review request plan docs page.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Push to the review request started no plan. | No plan started because no unit matched the push. List the units of the namespace to confirm the affected units, then fix the code location and push again. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |
| Status or summary comment is missing or stale after the push. | The forge write failed in the report change. Read the review request on the forge to confirm, then push again for a fresh delivery. | `gh pr view <number> --json statusCheckRollup,comments` |

Evidence for the claims above:

- The milestone step requires one commit status and one summary comment per pull request, edited on each new push. Proof: A push to a pull request starts a plan for each unit that the change touches. The fake GitHub API gets one commit status and one summary comment for each pull request. A new push edits the same comment and does not add one.
- The change is the roadmap item at docs/roadmap.md:287 with status not shipped. Proof: docs/roadmap.md:287
- The context pack names no ADR for this item. Proof: The context pack lists adrs as empty and gives no decision text.

## What Changes

- Push to a review request yields plan jobs with one status and one summary comment through composed changes.
- Each new push edits the same comment.
- E2e step and docs page prove the composed first slice.

Out of scope:

- Webhook intake, signature check, and delivery deduplication
- Plan start on push to a review request
- Status and summary comment writes and edits
- Apply after the merge
- Pre-merge apply option
- Console run page
- Write-back of unit config edits to git
- Forges other than the fake GitHub API

Depends on:

- git-host-webhook-intake-post
- report-plans-on-review-request
- start-plan-on-push-review
- `report-plans-on-review-request` (order of the milestone review)
- `start-plan-on-push-review` (order of the milestone review)
- `git-host-webhook-intake-post` (order of the milestone review)
- `run-triggers` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- Developer waits for many plan jobs behind one status and must open each plan link in the single comment.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Comment marker helper has test TestCommentMarker (`pkg/forge/comment_test.go:12`)
- GitHub comment upsert edits only its own comment with test TestUpsertCommentEditsOnlyItsOwn (`pkg/forge/github/comment_test.go:48`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Which part does this change own? Push intake, plan start, and status and comment writes stay owned by git-host-webhook-intake-post, start-plan-on-push-review, and report-plans-on-review-request. This change only proves the composed milestone step 7 flow.
- How are the missing failure scenarios covered? Empty match, duplicate delivery, closed review request, fork, ordering, retry, and storage stay owned by the three composed changes. This change proves the happy path plus the empty-match path.
- Which ADRs does the change touch? The context pack lists adrs as empty and gives no decision text, so no ADR decision applies to this composition step.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
