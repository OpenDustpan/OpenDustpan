---
version: 2
---

Issue: #1214.

## Why

**Persona: namespace administrator.** A namespace administrator sets follows triggers so a source apply starts plans of its followers. Without caps and a depth guard one source or one loop starts an unbounded number of plans, which blocks runners and hides the cause.

The user path this change must make work:

1. **API.** List triggers of the unit and followers of the source to see current counts
2. **API.** Save a follows trigger with POST or PUT; on refusal read the cap number or the loop path from the error
3. **Docs page.** Follow the trigger limits docs page to ask an installation administrator to change a cap when the team needs more followers
4. **API.** Read the audit entry and the log line when a deep chain stops at run time

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Save above the per-unit cap is refused with 422 and the cap number 20. | Save refused: unit already has 20 triggers. Remove a trigger or ask an administrator to raise the cap. Check caps with `GET /api/dustpan/v1/settings`. | `POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers` |
| Save above the per-source cap is refused with 422 and the cap number 50. | Save refused: source already has 50 followers. Remove a follower or ask an administrator to raise the cap. Check caps with `GET /api/dustpan/v1/settings`. | `POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers` |
| Save that makes a follows cycle is refused with 422 and names the loop path. | Save refused: the follows chain makes a cycle through the named units in order. Change the source to break the loop, then save again. | `PUT /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}` |

Evidence for the claims above:

- Issue 1214 asks for caps on trigger.Store.Put, a depth counter with refusal above a fixed depth, audit entry and log line, and a docs page. Proof: Part of #909. ## Outcome A source with many followers, or a loop that a restore or a row of another version creates, cannot start an unbounded number of plans. Scope: The caps on `trigger.Store.Put`, with conformance cases first. A depth counter in the origin of the job that a trigger starts, and a refusal in the dispatcher above a fixed depth. The refusal writes an audit entry and a log line. A docs page (ADR 214).
- This part enforces 20 followers per unit and 50 followers per source, refuses a cycle at save, stops chains deeper than 10 at run time, and needs version write and trigger start first. Proof: This part enforces 20 followers per unit and 50 followers per source. It refuses a cycle at save time and stops chains deeper than 10 at run time. It does not change trigger start or output compare. It needs the version write and trigger start first.
- No trigger.Store.Put implementation exists outside tests in the checked tree. Proof: rg -n -F --glob '!*_test.go' 'trigger.Store.Put' pkg internal cmd -> matches 0

## What Changes

- Enforce at most 20 triggers per unit and at most 50 followers per source at trigger.Store.Put, read at each save
- Refuse a follows cycle at save with serialized per-namespace writes and name the loop path in order
- Stop trigger chains deeper than 10 at run time with a depth counter in the job origin, one audit entry, and one log line
- Ship the docs page for trigger limits and the depth guard

Out of scope:

- Trigger start on apply and output-changed compare
- Schedule and review-request-push triggers
- Console run page and trigger list views

Depends on:

- write-state-version-unit-state
- write-state-version-unit-state-follows-trigger-start
- `write-state-version-unit-state-follows-trigger-start` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- A refused save shows two numbers at once when both caps bind; the person fixes one cap at a time.
- A loop path lists unit references with tool and namespace, which is long for chains across tools.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Trigger cycle error code exists. (`internal/errcode/catalog_trigger.go:7`)
- Trigger invalid error code exists. (`internal/errcode/catalog_trigger.go:13`)
- Trigger not-found error code exists. (`internal/errcode/catalog_trigger.go:16`)
- A followers query test exists in the trigger store suite. (`pkg/store/internal/triggerstore/triggerstore_test.go:198`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 267: Run triggers: three kinds, a plan only, started through the job service** (not owner-tagged).

- Decisions that apply: 7 (current), 10 (current), 17 (current), 20 (current).
- Changed by: ADR 273 changes decision 14; ADR 299 changes decisions 14 and 19; ADR 307 extends decision 8; ADR 310 changes decision 22; ADR 338 changes decision 14. Chain to the end: ADR 273, ADR 299, ADR 307, ADR 310, ADR 338, ADR 286, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 273, 299, 307, 310, 338.
- Quote: "A unit has at most 20 triggers. A source has at most 50 followers. An administrator can change both."
- Quote: "Cycles (question 6). The server refuses a cycle at save. An adapter serializes the trigger writes of one namespace, so that two saves at the same time cannot skip the check. A depth guard at run time refuses a chain that"
- Quote: "**A `follows` trigger names a source unit, and the server refuses a cycle at save.** After the source applies, a plan of the unit starts. The source is a unit reference, so a unit of one tool can follow a unit of another"
- Quote: "The error of a cycle names the path. It names the units of the loop in order. The adapter can add the names that the tool of each unit uses."
- Effect: This change implements the caps of decision 20, the save-time cycle refusal of decisions 7 and 17, and the run-time depth guard of decision 17 with the path names of decision 10. Cap refusals use error code trigger.invalid. Cycle refusals use error code trigger.cycle. The fixed depth 10 comes from the pack scope, not from the ADR.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request. A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Effect: The trigger caps and the depth guard are visible as errors and settings, so the change ships its docs page in the same pull request.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- What are the follower caps and when does a changed cap apply? ADR 267 decision 20 answers it: 20 triggers per unit and 50 followers per source, changeable by an administrator and read at each save with no cache. The installation settings resource holds both caps with defaults 20 and 50.
- How are trigger cycles refused and what does each 422 body hold? ADR 267 decisions 7 and 17 answer it: refuse the save, serialize trigger writes of one namespace, and name the loop in order per decision 10. Cap refusals use trigger.invalid with the cap number. Cycle refusals use trigger.cycle with the loop path.
- What stops a loop that a restore or another version row creates and where does the number 10 come from? ADR 267 decision 17 answers the guard layer only: a second layer at run time. The fixed depth 10, the audit entry, and the log line come from the issue scope and the roadmap scope in this pack, not from the ADR. No open question stays because the pack fixes the number.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
