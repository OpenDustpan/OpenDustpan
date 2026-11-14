Issue: #1253.

## Why

**Persona: developer.** A Developer deploys a stack or workspace. A gate in error in enforce mode stops every write of its scope until a person saves the gate again, with no path forward. The Developer needs a narrow audited break-glass to pass the error once and ship the fix.

The user path this change must make work:

1. **CLI.** Developer runs the Pulumi CLI or OpenTofu against the server; a state.write gate in enforce mode errors and the write is refused.
2. **API.** Developer reads the decision record through the tool-neutral API to see the gate in error.
3. **Docs page.** Developer retries the write with break-glass, a reason, and fresh authentication as the docs page describes.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Gate in error stops the write | Write refused: a gate is in error. Read the decision record through the API, then retry with break-glass and a reason. Check server health with `opendustpan status`. | `opendustpan status` |
| Break-glass without grant or fresh auth | Break-glass refused: missing grant, bad reason, or stale authentication. The write is refused and no pass is used. Check the installation with `opendustpan doctor`. | `opendustpan doctor` |

Evidence for the claims above:

- Change.BreakGlass is not implemented today. Proof: Issue #1253 body: Change.BreakGlass is not implemented: a change that sets it returns gate.ErrUnavailable.
- state.write gates run before tool writes. Proof: internal/api/tofuhttp/tofuhttp.go:135 admit asks the state.write guard before each checked write.
- The override audit kind, reason, fresh authentication and 15-minute single use come from the issue, not from an ADR. Proof: Issue #1253 body: the action `namespace:break-gate-error`, a reason, a fresh authentication, an audit entry of the kind `override`, and a pass of 15 minutes for the same principal, unit and operation kind, used once.

## What Changes

- Implement Change.BreakGlass at state.write for gates in error only: action namespace:break-gate-error, reason of at most 500 characters, fresh authentication, audit entry of kind override.
- Grant a 15-minute pass for the same holder, unit stable ID and exact operation kind, usable once with a conditional consume in the same transaction; the decision record keeps result error with a break-glass marker.
- Keep deny, warn and approval outcomes unchanged; a deny with break-glass still refuses, including error combined with deny.

Out of scope:

- Break-glass for deny gates
- Editing or disabling the gate in error
- Change-order decision points
- HTTP backend state.write route beyond Pulumi and TFE-compatible tool routes

## Usability

Where the path still costs the person time or surprises them:

- A gate in error stops all writes with no path forward; the error must name the break-glass and the record read.
- The 15-minute single-use limit surprises callers that retry scripts; the refusal must name expiry and use.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 224: Gates at fixed decision points** (owner-tagged: decision 13, 16).

- Decisions that apply: 13 (current), 16 (changed).
- Changed by: ADR 241 extends decision 16; ADR 286 changes; ADR 313 extends decision 1; ADR 317 changes decision 1; ADR 335 changes decision 16; ADR 335 extends decision 9; ADR 335 extends decision 13; ADR 342 changes decision 16; ADR 353 changes decision 14; ADR 353 extends decision 2; ADR 354 extends. Chain to the end: ADR 241, ADR 286, ADR 313, ADR 317, ADR 335, ADR 342, ADR 353, ADR 354, ADR 248, ADR 343, ADR 319, ADR 365.
- Current rule: this ADR, with ADR 241, 286, 313, 317, 335, 342, 353, 354.
- Quote: "An error fails closed (owner). A diagnostic error of the engine for a gate in `enforce` mode stops the change with `gate.ErrRuleFailed`. It is never an allow and never a deny. The record has the result `error` and the Cedar message. An error of a gate in `evaluate` mode is in the record and st"
- Quote: "`state.write`: writes from a person's own CLI (owner, 2026-10-18). Version 1 evaluates the gates of `state.write` before each of these writes:  | Write | When the point runs | `operation.kind` | | --- | --- | --- | | a Pulumi update that a person's own Pulumi CLI creates | at the create of the"
- Effect: Follows the fail-closed error rule and the state.write gate point. The audit kind override and the 15-minute single use come from issue #1253, not from this ADR.
- Conflict: none.

**ADR 335: Gate decisions: deny gates, held locks, unlocked declares and the engine port** (owner-tagged (all decisions)).

- Decisions that apply: 2 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A break-glass for a gate comes later (owner, 2026-11-07). A break-glass for a `deny` gate, with a review after use, is a later feature. Until a later ADR accepts it, the break-glass of ADR 224 decision 13 passes the gates in error only."
- Effect: Implements only the error pass; deny gates still have no break-glass.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The proposal is not spec-ready: its own code checks fail because the commands 'opendustpan gate' and 'opendustpan auth' do not exist. The user path, the error table and tasks 1.3 depend on them, so the gate blocks implementation until the command surface is decided. Resolution: answered: Replaced nonexistent opendustpan gate and opendustpan auth commands with opendustpan status, opendustpan doctor, and API reads in errors, user path, and tasks.
- R1.2 (missing scenario): No spec scenario covers the refusals the design's handler layer requires: empty reason, reason over 500 characters, stale fresh authentication, missing namespace:break-gate-error grant. The design says 403 or 422 and no pass row, but the proposal and spec do not state the status or the message. Resolution: answered: Added break-glass refusal requirement with scenarios for empty reason, reason over 500 characters, stale auth, and missing grant.
- R1.3 (missing scenario): No spec scenario covers single use, replay, 15-minute expiry, or replay for another unit or operation kind. Task 1.1 names a test (ErrorPassesOnce) that the spec does not define, and the design's service layer requires these cases to fail closed with gate.ErrRuleFailed. Resolution: answered: Added scenarios for single use replay, 15-minute expiry, and wrong unit or operation kind failing closed.
- R1.4 (failure mode): Concurrency is not specified. Two concurrent writes can both read an unused pass. The design says to consume the pass in the same transaction, but no requirement says the consume is a conditional update that only one caller can win. Resolution: answered: Added atomic consume requirement with conditional update in the same transaction plus a concurrent-winner scenario.
- R1.5 (open question): The change does not say whether the pass covers the other state.write operation kinds. ADR 335 decision 3 gives unlocked-write, and ADR 241 decision 23 gives unit-import. The proposal lists only the single operation kind of the pass key, and no ADR answers this. Resolution: answered: State.write requirements now bind the pass to the exact operation.kind value so unlocked-write and unit-import each need their own pass.
- R1.6 (failure mode): No rule says what happens when the override audit write fails after the pass is consumed, or when the write fails after the pass is consumed. The store layer requires an override entry but the spec does not make the audit write part of the success path. Resolution: answered: Added audit-failure requirement: the audit write is in the success path and its failure fails the state write without consuming the pass.
- R1.7 (open question): The decision record of a break-glass write is not specified. ADR 224 decision 13 gives result error for an engine error, but the spec says only that the record names the break-glass, so the result of a passed write is undefined. Resolution: answered: Specified that the passed write keeps record result error with a break-glass marker while the permit changes.
- R1.8 (missing scenario): No scenario covers an error gate combined with a deny gate or an approval rule on the same unit. ADR 224 decision 14 gives a fixed order, and the spec says only that deny still refuses. Resolution: answered: Added error-plus-deny requirement and scenario: combined error and deny still refuses even with break-glass.
- R1.9 (open question): The pass key is the principal, but for delegated requests and break-glass accounts the subject and the actor differ. ADR 354 and ADR 224 decision 17 are not applied to the key, so it is unclear which identity the pass is bound to. Resolution: open question: Added as open question on subject versus delegated actor binding, decides owner.
- R1.10 (wrong claim): The evidence that gate.ErrUnavailable is returned on state.write gate errors cites stategate.go:478 and jobgate.go:166. The input does not show that the state.write path returns this error. The jobgate line is a job path, not a state.write path. Resolution: answered: Removed unsupported stategate and jobgate ErrUnavailable claim; evidence now cites only the issue text and the state.write guard.
- R1.11 (wrong claim): The error table names the commands 'opendustpan gate record get' and 'opendustpan auth whoami' as the one command that helps. The code check reports that these commands do not exist, so the usability claim is unsupported. Resolution: answered: Replaced unsupported gate record get and auth whoami commands with existing opendustpan status and doctor commands.
- R2.1 (owner decision conflict): Owner decision of 2026-11-07 (ADR 335 decision 2) limits break-glass to gates in error, and the change scopes it correctly; however the change keys the pass on the request subject while ADR 224 decision 13 binds nothing about subject, so the subject choice is an unanswered owner question that the change takes silently in the design (design.md Decision 2, subject key) and the spec (specs/state-write-break-glass/spec.md, Pass a gate in error once). Resolution: open question: Holder key kept provisional in spec and design; binding choice moved to the owner open question and no longer taken silently.
- R2.2 (open question): The provisional answer on delegated subject binding (proposal.md Open questions, item 1) is marked as owner-reviewed but ADR 354 is owner-tagged and extends gate subject naming; binding to the request subject while break-glass accounts meet gates under ADR 224 decision 17 is not specified, so the pass key for break-glass account sessions is undefined. Resolution: open question: Delegated and break-glass account session binding kept in the same owner open question; spec holder means request subject until the owner decides.
- R2.3 (wrong claim): The ADR check says ADR 224 decision 13 is 'Follows fail-closed error rule'; the change's audit requirement names kind override, but no ADR or owner decision in the input defines the override audit kind, so the claim that the audit entry follows the ADR is unsupported. Resolution: answered: ADR check effect and evidence now state the override kind comes from issue #1253, not from an ADR.
- R2.4 (failure mode): Design Decision 2 consumes the pass in the same transaction as the state write, but the spec requires audit-write failure to fail the write without consuming the pass; there is no requirement for a crash or restart between the consume and the commit, nor for pass expiry checked against the database clock versus server clock. Resolution: answered: Added store-clock expiry and crash requirement with scenarios: expiry reads the store clock and a crash before commit leaves the pass unused.
- R2.5 (missing scenario): No scenario covers a break-glass request for an unlocked-write or unit-import operation kind, although ADR 335 decision 3 and ADR 241 decision 23 give those kinds their own gate rules; the spec only says the pass binds to the exact kind. Resolution: answered: Added unlocked-write and unit-import scenarios that show each kind needs its own pass.
- R2.6 (missing scenario): No scenario covers a pass request when the gate in error is in evaluate mode, or when the error comes from a gate whose onError changed; ADR 224 decision 13 keeps evaluate-mode errors out of the stop, so the pass would be used for a write that needed no pass. Resolution: answered: Added evaluate-mode requirement and scenario: an evaluate-mode error proceeds with no pass and consumes none.
- R2.7 (failure mode): The reason of 1 to 500 characters is not specified for byte versus character count, nor for control characters or non-UTF-8 input, which is a hostile-input path for the audit entry. Resolution: answered: The specs requirement Refuse bad break-glass covers the finding: reason rule of 1 to 500 UTF-8 characters with no control characters plus scenarios for empty, overlong, and control-character reasons, so bad input refuses with 422 and creates no pass row.
- R2.8 (scope): Task 1.3 requires the Pulumi and TFE-compatible routes to carry the break-glass marker, but the proposal's 'Out of scope' does not name the HTTP backend route for state.write, which ADR 224 decision 16 also covers; the coverage of the route set is not stated. Resolution: answered: Out of scope now names the HTTP backend state.write route; tasks wire only Pulumi and TFE-compatible routes.
- R2.9 (missing scenario): The error table promises the command 'opendustpan status' helps with a gate in error, but no scenario or task checks that the status output names the gate in error or the pass; the usability claim is not tested. Resolution: answered: Added status-output scenario and task check that opendustpan status names the gate in error.
- M.35 (gap): The tasks have no 'Failing tests first' task, but project.md requires it. Resolution: answered: First tasks section is now Failing tests first with the scenario-to-test mapping: TestStateWriteBreakGlass_ErrorPassesOnce, TestStateWriteBreakGlass_ReplayRefuses, TestStateWriteBreakGlass_BadReasonRefuses, and TestStateWriteBreakGlass_DenyRefuses fail on main before the fix.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. For a delegated request, is the break-glass pass bound to the request subject or to the human actor behind the delegation, and how do break-glass account sessions bind?
   - Options: Bind the pass to the request subject; Bind the pass to the human actor behind delegation.
   - Provisional decision (owner review 14 Nov): Bind the pass to the request subject
   - Reason: Binding to the request subject matches the Cedar principal of ADR 224 decision 4 and the delegated subject rule, and AWS binds emergency access to the assumed role session. Benchmark: AWS
   - Persona impact: Developer sees one rule: the same subject retries the write; no extra step. Escape hatch: Bind the pass to the request subject until the owner decides; delegated callers retry under the same subject.
   - Owner-tagged ADRs involved: ADR 224, ADR 354 (their words do not change).

Questions considered and answered:

- Does break-glass pass a deny gate? No. ADR 335 decision 2 limits the pass to gates in error; deny gates keep no pass until a later ADR.
- What happens today when Change.BreakGlass is set? ADR 224 decision 13 fails closed with gate.ErrRuleFailed; the issue states Change.BreakGlass today gives gate.ErrUnavailable, so this change wires that field to the decision 13 pass.
- Does the pass cover every state.write operation kind? Yes. The pass key holds the exact operation.kind value of the refused write, so unlocked-write and unit-import each need their own pass; no pass crosses kinds.
- What is the decision record result of a passed write? The passed write keeps record result error with a break-glass marker and proceeds; only the permit changes, not the gate result.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
