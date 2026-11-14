---
version: 2
---

Roadmap: `docs/roadmap.md:295` (Pre-merge apply, the option).

## Why

**Persona: namespace administrator.** A namespace administrator manages units that need fast iteration on short-lived branches. The default apply after the merge blocks those teams, so they need an explicit per-unit option to apply an approved pull request from its branch.

The user path this change must make work:

1. **Docs page.** Set applyTiming pre-merge on the unit from the docs page.
2. **API.** Approve the review request and request a branch apply.
3. **Console.** Watch the apply job timeline and log.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Apply requested from the branch on a unit with the default | The server refuses the branch apply and names applyTiming pre-merge and the unit. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |
| Approval is missing or stale at apply time | The server refuses the apply because the approval is missing, targets an older head, or is stale for the current branch head. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |
| No plan on the exact branch head | The server refuses the apply because no plan exists on the exact branch head commit. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |

Evidence for the claims above:

- The milestone step requires a pre-merge apply option with a refusal on default units. Proof: docs/roadmap.md:295
- The item is not shipped. Proof: status: not shipped
- No ADR is given in the context pack for this item. Proof: adrs: []

## What Changes

- Add per-unit applyTiming value with pre-merge option and post-merge default, with effective value in the API.
- Start the apply pinned to the branch head commit only when the unit sets applyTiming pre-merge with approval and plan on that exact commit, re-checked at job start and before the first change.
- Refuse a branch apply on a default unit, with no plan on the exact head, or with a missing or stale approval, with an error that names the cause.

Out of scope:

- Apply after the merge from the default branch
- Pull request plan, status, and summary comment
- Review modes other than the approved modes of this milestone

Depends on:

- apply-after-merge
- pull-request-automation-first-slice
- report-plans-on-review-request
- mirror-approvals-between-review-request
- `apply-after-merge` (order of the milestone review)
- `report-plans-on-review-request` (order of the milestone review)
- `mirror-approvals-between-review-request` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The option name in the refusal matches the setting name in the docs page.
- The units read shows the effective value so a developer sees why a branch apply was refused.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has test TestRestoreMutatedTargetCannotApply (`pkg/backup/restore_service_test.go:979`)
- Main has test TestBackupTargetComesOnlyFromTheOption (`pkg/dustpan/dustpan_test.go:1268`)
- Main defines error code request.body-invalid (`internal/errcode/catalog_request.go:7`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Can this option slice ship before RFC 27 is accepted?
   - Options: Ship the option slice now with pinned SHA and named refusal; Wait until RFC 27 is accepted before any pre-merge apply.
   - Provisional decision (owner review 14 Nov): Ship the option slice now with pinned SHA and named refusal
   - Reason: The step says the design gives the behavior, and the slice pins the exact commit and refuses on any head move. Benchmark: Google forbids submit of unreviewed code by default and gates exceptions behind explicit per-target opt-in with pinned commits and re-check at submit time
   - Persona impact: A namespace administrator keeps the safe default unless the team accepts the risk; no persona loses the default path. Escape hatch: Teams that need fast iteration set applyTiming pre-merge per unit; all other units stay on the safe default.
2. Which route carries the branch apply request and its authorization actions?
   - Options: Reuse the job start route with authorization actions for branch apply; Add a new branch apply route with its own authorization actions.
   - Provisional decision (owner review 14 Nov): Reuse the job start route with authorization actions for branch apply
   - Reason: Every endpoint declares its actions, and reuse keeps one job path with a pinned SHA. Benchmark: AWS refuses a state-changing call with a named precondition error and points to the exact setting that allows it
   - Persona impact: A namespace administrator and a developer use the same documented request. Escape hatch: The refusal names applyTiming pre-merge.

Questions considered and answered:

- Should pre-merge apply become the default? No. The milestone step keeps apply after the merge as the default and pre-merge apply as a per-unit option.
- Does the milestone step fix the refusal status code and wording? No. The step requires only an error that names the option. This change chooses the refusal shape and wording.
- Does the apply use the approved branch head or a pinned commit SHA? A pinned commit SHA, with a plan on that exact SHA.
- What happens when two units disagree on the option? Each unit decides alone.
- Does RFC 27 govern this behavior? RFC 27 is a draft, not an accepted ADR, so there is no ADR conflict. The owner decides whether this slice ships first (see open questions).

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
