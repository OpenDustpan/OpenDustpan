---
version: 2
---

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
| The merge apply cannot check out the pinned commit sha. | The job ends as failed and names the missing commit sha. Merge again from a commit that the forge still serves. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{id}` |

## What Changes

- Merge of a pull request starts 1 apply per touched unit from the default branch at the merge commit.
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

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main reads review merge state in forge tests (`pkg/forge/github/review_test.go:285`)
- Main stores gate denied error code (`internal/errcode/catalog_gate.go:17`)
- Main enforces manual lock on stack start (`pkg/stack/manual_lock_test.go:210`)
- Main tests commit follow of a moved branch (`pkg/forge/github/commit_test.go:918`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does a push to the pull request branch start an apply? The roadmap step answers it: the merge starts an apply from the default branch at the merge commit, and a push to the pull request branch does not apply.
- Which gates and approvals guard the apply after the merge? The roadmap step answers it: the apply meets the gates and approvals of step 6, so this change reuses that gate and approval check with no new rule.
- When a new push reaches the default branch after the merge but before the apply starts, does the apply keep the merge commit or move to the new head? Decided in this change and fixed in the spec: the apply keeps the merge commit sha pinned at job creation and never moves to a later head.
- Which ADR decisions apply to apply after the merge? The context pack names no ADR decision text for this item, so this change cites no ADR decision. Webhook intake and plan behavior come from the depended changes, and approval behavior comes from policy-approvals.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
