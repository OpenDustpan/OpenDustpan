---
version: 2
---

Issue: #890, #891, #894.

## Why

**Persona: developer.** Developer opens a pull request and wants the plan result of each stack and workspace without leaving the Git host, and wants one check that branch protection can require. Today no setting says when an apply runs, and no fixed check exists.

The user path this change must make work:

1. **E2e.** Push to a pull request that touches three units. Read one summary check, three unit checks and one comment with a table row for each unit: status, counts, resource addresses with their kind of change, and a link to the change order.
2. **E2e.** Push again. Read the same comment with the new results. No second comment exists.
3. **API.** Set the apply timing of a namespace, a collection or a unit through the settings API. Read the effective value and the level that set it.
4. **Docs page.** Copy the fixed summary check name from the docs page into the branch protection of the Git host.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A unit write of pre-merge apply while a wider level forbids overrides | The server answers 409 with a message that names the level that forbids the override. Nothing changes. | `GET /api/dustpan/v1/namespaces/{namespace}/settings` |
| A timing value that is not post-merge or pre-merge | The server answers 400 and changes nothing. | `GET /api/dustpan/v1/namespaces/{namespace}/settings` |
| The Git host does not answer on a check or comment write | The write fails with the answer of the Git host. The summary check is not reported as passed. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |

Evidence for the claims above:

- No code names applyTiming yet. Proof: rg -n -F --glob '!*_test.go' 'applyTiming' pkg internal cmd gives 0 matches.
- No code names allowOverride yet. Proof: rg -n -F --glob '!*_test.go' 'allowOverride' pkg internal cmd gives 0 matches.
- Issue 890 asks for one summary check, one check for each unit and exactly one edited comment. Proof: Issue #890 acceptance criteria: one push with three units gives one summary check, three unit checks and one comment.
- The settings API routes exist for the installation and for a namespace. Proof: Coverage gaps list GET /api/dustpan/v1/namespaces/{namespace}/settings (getNamespaceSettings) and GET /api/dustpan/v1/settings (getSettings).
- A comment marker and a comment upsert already exist. Proof: Pack tests TestCommentMarker (pkg/forge/comment_test.go:12) and TestUpsertCommentEditsOnlyItsOwn (pkg/forge/github/comment_test.go:48).

## What Changes

- The Forge port gets a method to create or update one check, with conformance cases.
- The summary check name is the constant opendustpan/plans, documented for branch protection. Unit checks use the name of the summary, a slash and the unit name.
- A comment renderer builds one table with a row for each unit and shows no value. It truncates a long list of addresses to 60,000 characters for the whole comment and links to the change order.
- The setting applyTiming (post-merge or pre-merge) with allowOverride is new at namespace and collection level, with a unit value. It uses the generic settings API and writes audit entries.
- Each check and comment write binds to the head commit of the plans and carries a version, so an older write cannot replace a newer one.

Out of scope:

- Comment commands, including plan, apply and state commands (a later change)
- Redaction of a plan comment
- Approval mirroring between a review request and its change order
- GitLab, until its Forge adapter lands
- The docs text that explains why post-merge apply is recommended

Depends on:

- start-plan-on-push-review
- git-host-webhook-intake-post
- add-job-service-routes-start-1
- `start-plan-on-push-review` (order of the milestone review)
- `git-host-webhook-intake-post` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The summary check name must be copied exactly from the docs page into branch protection.
- A 409 on the timing setting names the level that forbids the override.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- A comment marker helper exists in the Forge package. (`pkg/forge/comment_test.go:12 (func TestCommentMarker)`)
- The GitHub adapter edits only its own comment. (`pkg/forge/github/comment_test.go:48 (func TestUpsertCommentEditsOnlyItsOwn)`)
- The Forge service audits its check outcome. (`pkg/forge/service_test.go:501 (func TestCheckAuditsItsOutcome)`)
- The GitHub adapter reads the merge state of a review request. (`pkg/forge/github/review_test.go:285 (func TestReadReviewMergeState)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P5)**, Batch 1: default collection at namespace create: "B. A new namespace gets a stored, visible, audited `default` collection and a catch-all placement rule in the create transaction; an admin can delete either; the create request can opt out. Reason: works first time from the docs (Terraform Cloud "Default Project"); "no match, no unit" (ADR 51 d5, ADR 61 d3) still holds because the rule is explicit and stored."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 299: Review requests and change orders: one integration with the Git host** (owner-tagged (all decisions)).

- Decisions that apply: 25 (current).
- Changed by: ADR 328 extends decision 1; ADR 359 extends decision 1; ADR 359 extends decision 15; ADR 359 extends decision 25. Chain to the end: ADR 328, ADR 359.
- Current rule: this ADR, with ADR 328, 359.
- Quote: "The plan comment (owner). One comment on the review request, edited in place, with a summary and a link. By default the summary holds counts and resource addresses with their kind of change, and never a value."
- Effect: One comment, edited in place, holds counts and addresses with their kind of change and no value.
- Conflict: none.

**ADR 359: Review requests: mirrored approvals, checks, comment commands and apply timing** (owner-tagged: decision 4).

- Decisions that apply: 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The table in the plan comment (owner, 2026-10-18). The server edits one comment in place (ADR 299 decision 25). It has a table with one row for each unit. A row shows the status, the `+ ~ -` counts and a link to the change order."
- Effect: The comment has one table row for each unit with status, counts and a link to the change order.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "No page, not done. A reviewer refuses such a pull request when the page is not in it. A later pull request for the docs does not make the first one done."
- Effect: The docs page ships in the same pull request.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Do unit checks belong in this change? Yes. Issue 890 asks for one non-required check for each unit, and ADR 359 decision 4 and ADR 299 decision 25 fix the report.
- Do comment commands belong here? No. They need their own authorization actions and identity mapping, and the issue lists them as out of scope.
- Does the default collection change the timing levels? No. Owner decision, 13 Nov 2026 (queue P5): the default collection is a stored, audited collection. It is one normal level for the setting, with no hidden rule.
- Which commit does the check bind to? The head commit that the plans ran on. ADR 299 decision 18 reads the head fresh, and a difference fails closed.
- What is the summary check name? Provisional value opendustpan/plans, fixed as one constant. The docs page, the tests and the e2e step use it. A rename needs a new decision, and the owner may confirm it at review. It does not block the spec.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
