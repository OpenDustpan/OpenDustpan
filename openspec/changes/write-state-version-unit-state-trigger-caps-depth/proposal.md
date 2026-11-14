---
version: 3
---

Issue: #1214.

## Why

**Persona: namespace administrator.** A namespace administrator sets follows triggers so a source apply starts plans of its followers. Without caps and a depth guard one source or one loop starts an unbounded number of plans, which blocks runners and hides the cause.

The user path this change must make work:

1. **API.** List triggers of the unit with GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers to see current counts
2. **API.** Save a follows trigger with POST or PUT; on refusal read the cap number or the loop path from the error
3. **Docs page.** Follow the trigger limits docs page to ask an installation administrator to change a cap when the team needs more followers
4. **API.** Read the audit entry and the log line when a deep chain stops at run time

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Save above the per-unit cap is refused with 422 and the cap number 20. | Save refused: unit already has 20 triggers. Remove a trigger or ask an administrator to raise the cap. Check caps with `GET /api/dustpan/v1/settings`. | `GET /api/dustpan/v1/settings` |
| Save above the per-source cap is refused with 422 and the cap number 50. | Save refused: source already has 50 followers. Remove a follower or ask an administrator to raise the cap. Check caps with `GET /api/dustpan/v1/settings`. | `GET /api/dustpan/v1/settings` |
| Save that makes a follows cycle is refused with 422 and names the loop path. | Save refused: the follows chain makes a cycle through the named units in order. Change the source to break the loop, then save again. | `PUT /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}` |

Evidence for the claims above:

- Issue 1214 asks for caps on trigger.Store.Put, a depth counter with refusal above a fixed depth, audit entry and log line, and a docs page. Proof: Issue 1214 scope: The caps on `trigger.Store.Put`, with conformance cases first. A depth counter in the origin of the job that a trigger starts, and a refusal in the dispatcher above a fixed depth. The refusal writes an audit entry and a log line. A docs page (ADR 214).
- This part enforces 20 followers per unit and 50 followers per source, refuses a cycle at save, stops chains deeper than 10 at run time. Proof: Roadmap step of the pack: This part enforces 20 followers per unit and 50 followers per source. It refuses a cycle at save time and stops chains deeper than 10 at run time.
- Main writes unit state only in publishUnitState, inside the fenced transaction. No observer port exists or is added. Proof: PM decision on the parent change: publishUnitState (pkg/store/sqlite/unit_state.go:97) is the one write path and closes awaiting jobs with jobstore.CloseAwaiting.

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

- A refused save may show two cap numbers at once when both caps bind.
- A loop path lists unit references with tool and namespace, which is long for chains across tools.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Unit state is written only in publishUnitState inside the fenced transaction. It closes awaiting jobs with jobstore.CloseAwaiting. No observer port exists. (`pkg/store/sqlite/unit_state.go:97`)
- Trigger cycle error code exists. (`internal/errcode/catalog_trigger.go:7`)
- Trigger invalid error code exists. (`internal/errcode/catalog_trigger.go:13`)
- A followers query test exists in the trigger store suite. (`pkg/store/internal/triggerstore/triggerstore_test.go:198`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 267: Run triggers: three kinds, a plan only, started through the job service** (not owner-tagged).

- Decisions that apply: 7 (current), 10 (current), 17 (current), 20 (current).
- Changed by: ADR 273 changes decision 14; ADR 299 changes decisions 14 and 19; ADR 307 extends decision 8; ADR 310 changes decision 22; ADR 338 changes decision 14. Chain to the end: ADR 273, ADR 299, ADR 307, ADR 310, ADR 338, ADR 286, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 273, 299, 307, 310, 338.
- Quote: "A unit has at most 20 triggers. A source has at most 50 followers. An administrator can change both."
- Quote: "The error of a cycle names the path. It names the units of the loop in order."
- Effect: This change implements the caps, the save-time cycle refusal and the run-time depth guard. Cap refusals use trigger.invalid. Cycle refusals use trigger.cycle. Depth 10 comes from the pack scope.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request. A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Effect: The caps and the depth guard are visible as errors and settings, so the docs page ships in the same pull request.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- What are the caps and when does a changed cap apply? ADR 267 decision 20: 20 per unit, 50 per source, changeable by an administrator. Caps are read at each save with no cache.
- How are cycles refused? ADR 267 decisions 7, 10 and 17: refuse at save, serialize writes of a namespace, name the loop in order.
- Where does depth 10 come from? The pack roadmap scope fixes it. ADR 267 decision 17 names only the guard.
- Does this change need an observer port for state writes? No. The PM decision says publishUnitState is the one write path, and this change adds no observer.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
