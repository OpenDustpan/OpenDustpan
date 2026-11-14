---
version: 2
---

Issue: #1157.

## Why

**Persona: developer.** A developer runs the Pulumi CLI and OpenTofu against the server and must see one predictable approval wait when the namespace requires it, instead of an apply that skips review or blocks without a reason.

The user path this change must make work:

1. **API.** Namespace administrator sets the approval default through the API and reads the docs page.
2. **CLI.** Developer runs a Pulumi stack update and an OpenTofu workspace apply through the tool CLIs against the server.
3. **API.** Developer sees approval wait, a second person approves, and the job continues.
4. **Docs page.** Developer reads the docs page for the default, the wait state and the approve command.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Apply refused for a principal with only plan rights. | 403 with the required action and the decision record. Run `GET /api/dustpan/v1/namespaces/{namespace}/approval-default` to read the default that requires approval. | `GET /api/dustpan/v1/namespaces/{namespace}/approval-default` |
| Job stopped by a deny gate at a job decision point. | Stop with the message of each deny and the record. Run `GET /api/dustpan/v1/namespaces/{namespace}/approval-rules` to read the rules and the record. | `GET /api/dustpan/v1/namespaces/{namespace}/approval-rules` |

Evidence for the claims above:

- Slice 1 stores approval defaults and rules and jobgate is tested but wired nowhere. Proof: Issue #1157 body: Slice 1 stores approval defaults and approval rules (REST routes) and has a `job.Decider` adapter (`pkg/gate/jobgate`) that is tested but wired nowhere, because no job service runs.
- pkg/gate/jobgate implements job.Decider. Proof: rg -n -F --glob '!*_test.go' 'job.Decider' pkg internal cmd matches 4, first lines pkg/gate/jobgate/jobgate.go:39:// Decider is a job.Decider. It allows every start, and adds the approval gate
- No live caller wires jobgate.Decider. Proof: rg -n -F --glob '!*_test.go' 'jobgate.Decider' pkg internal cmd matches 0 with empty first_lines
- The change path exists as a package. Proof: Path pkg/gate/jobgate exists: true.

## What Changes

- Job service evaluates stored approval defaults through jobgate.Decider at change.propose, change.planned and change.apply for Pulumi and OpenTofu jobs.
- Applies with a requirement wait in approval wait and resume after approvals; applies with no default need no approval.
- E2E proves the wait and resume path on both tools with decision records.

Out of scope:

- New approval rule kinds or gate policy language
- Console run page timeline and diff rendering
- Pre-merge apply and pull request automation behavior
- Stored plan hash binding beyond plan staleness reset
- State.write, profile.save and profile.open evaluation
- Job cancel and timeout while in approval wait (job service change)
- Deleted unit while in approval wait (unit lifecycle change)

Depends on:

- add-job-service-routes-start
- several-approval-requirements-one-change
- gate-evaluation-must-apply-dev
- `several-approval-requirements-one-change` (order of the milestone review)
- `gate-evaluation-must-apply-dev` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A developer sees two tool flows with one wait word and one approve command.
- The wait names the requirement, the approvers still needed and the decision record.
- An apply that waits after a default changed surprises the developer; the wait names the new requirement and the record.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- pkg/gate/jobgate implements job.Decider and is wired nowhere (`pkg/gate/jobgate/jobgate.go:41`)
- job.Decider port exists for the job service (`pkg/job/gate.go:440`)
- jobgate Decider constructor and input shape live in jobgate (`pkg/gate/jobgate/jobgate.go:46`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 222: One model for access, gates, approvals and facts** (owner-tagged: decision 2, 4, 8).

- Decisions that apply: 2 (current), 4 (current), 8 (current).
- Changed by: ADR 286 changes; ADR 335 extends decision 7; ADR 351 extends; ADR 354 changes decision 5. Chain to the end: ADR 286, ADR 335, ADR 351, ADR 354.
- Current rule: this ADR, with ADR 286, 335, 351, 354.
- Quote: "Six decision points (owner). `change.propose`, `change.planned`, `change.apply`, `profile.save`, `profile.open` and `state.write` (ADR 224 decision 2)."
- Quote: "Fixed controls stay fixed (owner). Workspace locks, manual locks, freezes with break-glass, override switches, module switches,"
- Quote: "Words. "Gate" names the stored rule. The job state that waits for people is the **approval wait**, and its wire value stays `awaiting-approval`"
- Effect: The change uses the fixed decision points, the approval wait name, and the plan staleness fixed control.
- Conflict: none.

**ADR 224: Gates at fixed decision points** (owner-tagged: decision 2, 13, 14).

- Decisions that apply: 2 (extended), 3 (current), 7 (current), 13 (current), 14 (changed).
- Changed by: ADR 241 extends decision 16; ADR 286 changes; ADR 313 extends decision 1; ADR 317 changes decision 1; ADR 335 changes decision 16; ADR 335 extends decision 9; ADR 335 extends decision 13; ADR 342 changes decision 16; ADR 353 changes decision 14; ADR 353 extends decision 2; ADR 354 extends. Chain to the end: ADR 241, ADR 286, ADR 313, ADR 317, ADR 335, ADR 342, ADR 353, ADR 354, ADR 248, ADR 343, ADR 319, ADR 365.
- Current rule: this ADR, with ADR 241, 286, 313, 317, 335, 342, 353, 354.
- Quote: "Six decision points (owner).  | Decision point | When | Replaces | | --- | --- | --- | | `change.propose` | before a job,"
- Quote: "The recheck at `change.apply`. `change.apply` evaluates the gates again with the facts of that moment. A recheck only adds."
- Quote: "Modes. A gate in `enforce` mode gives its outcome. A gate in `evaluate` mode is evaluated and written in the record, with no effect."
- Quote: "An error fails closed (owner). A diagnostic error of the engine for a gate in `enforce` mode stops the change with `gate.ErrRuleFailed`."
- Quote: "Combining (owner). For each unit, in this order: 1. Any `deny` gives `deny`, with the message of each `deny`. 2. Else each named approval rule"
- Effect: The change wires evaluation at change.propose, change.planned and change.apply, keeps combine, recheck-only-adds, evaluate mode and fail-closed error rules.
- Conflict: none.

**ADR 226: Facts: typed, versioned fact documents** (owner-tagged: decision 1, 3).

- Decisions that apply: 1 (current), 3 (extended).
- Changed by: ADR 240 changes decision 4; ADR 241 extends decision 4; ADR 258 extends decision 7; ADR 263 changes decision 7; ADR 286 changes; ADR 342 extends decision 4; ADR 353 changes decisions 2 and 4; ADR 353 changes decision 10; ADR 353 extends decision 13; ADR 354 extends decisions 3 and 4. Chain to the end: ADR 240, ADR 241, ADR 258, ADR 263, ADR 286, ADR 342, ADR 353, ADR 354, ADR 297, ADR 248, ADR 313, ADR 343, ADR 358, ADR 366, ADR 365, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 240, 241, 258, 263, 286, 342, 353, 354.
- Quote: "A fact (owner). A fact is a typed value that the server produces before a decision point. A fact never decides."
- Quote: "A missing fact is a typed value (owner). Each fact always has a value of its type. A fact that a producer can fail to give belongs"
- Effect: The change uses stored approval defaults with server-produced facts and fails closed when a needed fact is unavailable.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does any live job path already evaluate approval defaults? No. Issue #1157 states slice 1 stored approval defaults and approval rules and pkg/gate/jobgate is tested but wired nowhere because no job service runs; the rg proof shows job.Decider is implemented in pkg/gate/jobgate/jobgate.go with zero uses of jobgate.Decider outside it.
- Which decision point must a running job evaluate? ADR 224 decisions 2 and 3 and ADR 222 decision 2 fix the points; this change evaluates at change.propose, change.planned and change.apply through the job service points that #899 and #968 provide, and leaves state.write, profile.save and profile.open to their own changes.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
