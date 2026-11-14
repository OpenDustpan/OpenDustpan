Issue: #1033.

## Why

**Persona: developer.** A developer runs the Pulumi CLI against the server and the update stops at a gate with no plain reason. The developer wants each plan and each apply to meet the same rules as other runs, and to see the refusal text and the one command that shows the decision.

The user path this change must make work:

1. **CLI.** Run pulumi preview or pulumi up against the server from a workstation
2. **API.** Read the refusal with code and message, or the decision record for evaluate mode
3. **E2e.** Run the e2e suite with the real Pulumi CLI over TLS
4. **Docs page.** Follow the gates page for Pulumi jobs and CLI writes

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A gate in enforce mode refuses a Pulumi preview, update or CLI create | The server answers 403 with `{"code": 403, "message": "change refused by gate <id>: <message>"}` and writes a decision record with result deny. | `dustpan gate decisions get <decision-id>` |
| A fixed control refuses at the same point as a gate | The server answers 403 with `{"code": 403, "message": "change refused by gate <id>: <message>"}` and the record shows the fixed control state. | `pulumi preview --debug` |
| A gate refuses a Pulumi job at change.propose, change.planned or change.apply | The job ends refused, the job record holds the decision ID, and a watcher sees the refusal text from the job status. | `dustpan job get <job-id>` |

Evidence for the claims above:

- The gate model already names change.propose in the job service. Proof: pkg/job/service.go:61:	// Point is the decision point of the gate: change.propose or state.write.
- The gate model already names change.planned with schema change.planned/1.0. Proof: pkg/gate/testdata/facts.json:85:  "schema": "change.planned/1.0",
- The gate model already names change.apply. Proof: pkg/gate/gate.go:178:	PointChangeApply DecisionPoint = "change.apply"
- The state.write evaluation already exists on the OpenTofu path. Proof: internal/api/tofuhttp/tofuhttp.go:108:	// with no lock against the access limits and the gates of state.write.
- The issue scopes this change to Pulumi jobs and the Pulumi update create, and excludes change orders, profiles and the Catalog. Proof: Out:
- Change orders, profiles, the Catalog.

## What Changes

- Evaluate change.propose, change.planned and change.apply on Pulumi jobs with versioned fact documents
- Evaluate state.write at the Pulumi update create with the Pulumi error shape
- Write a decision record for enforce and evaluate outcomes at each point
- Ship e2e steps with the real Pulumi CLI and a docs page

Out of scope:

- Change orders
- Profiles and the profile decision points
- Catalog behavior
- OpenTofu paths beyond the depended slice

Depends on:

- add-job-service-routes-start-1
- state-write-state-facts-fixed
- `state-write-state-facts-fixed` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A refusal at four points can confuse: the CLI text must name the point, the gate and the one read command.
- Evaluate mode looks silent: the docs must show where the record appears.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request. A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Quote: "No page, not done. A reviewer refuses such a pull request when the page is not in it."
- Effect: The change ships its user docs page in the same pull request because a refusal text and a decision record are visible to a person.
- Conflict: none.

**ADR 222: One model for access, gates, approvals and facts** (owner-tagged: decision 2, 4).

- Decisions that apply: 2 (current), 4 (current).
- Changed by: ADR 286 changes; ADR 335 extends decision 7; ADR 351 extends; ADR 354 changes decision 5. Chain to the end: ADR 286, ADR 335, ADR 351, ADR 354.
- Current rule: this ADR, with ADR 286, 335, 351, 354.
- Quote: "Six decision points (owner). `change.propose`, `change.planned`, `change.apply`, `profile.save`, `profile.open` and `state.write` (ADR 224 decision 2)."
- Quote: "Fixed controls stay fixed (owner)."
- Effect: This change applies the six fixed points to Pulumi jobs and Pulumi CLI writes. It adds no new point and no new part. Fixed controls stay fixed and appear in the record.
- Conflict: none.

**ADR 224: Gates at fixed decision points** (owner-tagged: decision 2, 13, 16).

- Decisions that apply: 2 (extended), 3 (current), 7 (current), 13 (current), 16 (changed), 17 (current).
- Changed by: ADR 241 extends decision 16; ADR 286 changes; ADR 313 extends decision 1; ADR 317 changes decision 1; ADR 335 changes decision 16; ADR 335 extends decision 9; ADR 335 extends decision 13; ADR 342 changes decision 16; ADR 353 changes decision 14; ADR 353 extends decision 2; ADR 354 extends. Chain to the end: ADR 241, ADR 286, ADR 313, ADR 317, ADR 335, ADR 342, ADR 353, ADR 354, ADR 248, ADR 343, ADR 319, ADR 365.
- Current rule: this ADR, with ADR 241, 286, 313, 317, 335, 342, 353, 354.
- Quote: "Six decision points (owner)."
- Quote: "Modes. A gate in `enforce` mode gives its outcome."
- Quote: "A gate in `evaluate` mode is evaluated and written in the record, with no effect."
- Quote: "A diagnostic error of the engine for a gate in `enforce` mode stops the change with `gate.ErrRuleFailed`."
- Quote: "`state.write`: writes from a person's own CLI (owner, 2026-10-18)."
- Quote: "Break-glass accounts meet the gates."
- Effect: Pulumi jobs evaluate change.propose, change.planned and change.apply with a recheck at apply and a return to approval wait on a new requirement. The Pulumi update create and its cancel and force-unlock evaluate state.write. Evaluate mode records without effect. An engine error fails closed in enforce mode and records without effect in evaluate mode. Break-glass sessions still meet gates.
- Conflict: none.

**ADR 226: Facts: typed, versioned fact documents** (owner-tagged: decision 1).

- Decisions that apply: 1 (current), 2 (changed).
- Changed by: ADR 240 changes decision 4; ADR 241 extends decision 4; ADR 258 extends decision 7; ADR 263 changes decision 7; ADR 286 changes; ADR 342 extends decision 4; ADR 353 changes decisions 2 and 4; ADR 353 changes decision 10; ADR 353 extends decision 13; ADR 354 extends decisions 3 and 4. Chain to the end: ADR 240, ADR 241, ADR 258, ADR 263, ADR 286, ADR 342, ADR 353, ADR 354, ADR 297, ADR 248, ADR 313, ADR 343, ADR 358, ADR 366, ADR 365, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 240, 241, 258, 263, 286, 342, 353, 354.
- Quote: "One schema for each decision point. Each decision point has a fact schema with a version."
- Quote: "A fact (owner). A fact is a typed value that the server produces before a decision point."
- Effect: Each evaluation uses the versioned fact schema of its decision point and names the schema version in the decision record. Facts never decide.
- Conflict: none.

**ADR 335: Gate decisions: deny gates, held locks, unlocked declares and the engine port** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A deny gate on a cancel or a force-unlock stays (owner, 2026-11-07)."
- Effect: A deny gate at state.write can refuse a Pulumi update cancel or force-unlock and no rule passes it; the change defines the refusal and the record.
- Conflict: none.

**ADR 365: Wording refinements edit an ADR in place, with a changelog (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "With the owner's word ("A lgtm", 13 November 2026), decision 3 now says that such a state is not a read failure: `state.available` is `false`, the decision record says why, and the write goes on under the gates that apply."
- Effect: A client-encrypted Pulumi state body is not a read failure: state.available is false, the record says why, and the write goes on under the gates.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (missing scenario): specs/pulumi-gates/spec.md has no scenario for a gate in enforce mode whose engine errors at change.propose, change.planned or change.apply on Pulumi jobs; ADR 224 decision 13 requires fail-closed with gate.ErrRuleFailed, and the design layer table requires this hostile test, yet no requirement or task maps to it. Resolution: answered: Added an engine-error scenario to the propose, planned and apply requirements and a failing engine-error test to tasks.
- R1.2 (missing scenario): The spec gives no scenario for the operation kind of a Pulumi update created by a person's CLI versus a runner update, although design.md Risks names double evaluation of change.apply and state.write for one runner update; the requirement covers only the CLI create. Resolution: answered: Added a state.write requirement that names the CLI operation kind and a scenario that a runner update skips state.write.
- R1.3 (failure mode): design.md names double evaluation of change.apply and state.write for one runner update as a risk, but no decision or task says which point runs for a runner update; the change is not buildable without this answer. Resolution: answered: Added the no-double-evaluation design decision and a task that wires it; runner updates meet the three change points only.
- R1.4 (open question): The change does not say how a deny at change.planned after the preview interacts with the approval wait, the approval rule requirement and the plan staleness check (ADR 222 decision 4); the spec leaves the job in 'approval wait or refused', an either-or state. Resolution: answered: Added the planned-deny requirement: a deny refuses with no approval wait, requirements come only from an allow with requirements, and the staleness check stays fixed.
- R1.5 (missing scenario): The spec has no scenario for the Pulumi refusal text and the decision record when the gate is a final gate or a fixed control refuses at the same point; the proposal error table names this case, but no requirement covers it. Resolution: answered: Added a final-gate and fixed-control scenario that names the gate and the record read.
- R1.6 (failure mode): The design layer table requires the HTTP adapter to reject a forged fact document and a cross-unit gate sent directly to the handler; no task in tasks.md maps to this hostile test, so the layer is unverified. Resolution: answered: Added a hostile handler task for a forged fact document and a cross-unit gate.
- R1.7 (failure mode): No requirement covers a restart or concurrent update between the evaluation at change.apply and the dispatch of the update phase, although ADR 224 decision 3 requires the recheck to hold at dispatch. Resolution: answered: Added an apply-recheck requirement that holds the recheck at dispatch across restart and concurrent update.
- R1.8 (adr conflict): ADR 224 decision 16 gives the state.write point for a Pulumi update created by a person's own CLI; the change also names an update create at operation kind for a Pulumi update, but the proposal's error table lists a deny shape of 403 while ADR 222 decision 2 requires the write-version record; the change does not state which operation.kind value it sends. Resolution: answered: The state.write requirement now states the operation kind value it sends for a person CLI create.
- R1.9 (wrong claim): The proposal's evidence says internal/api/tofuhttp/tofuhttp.go checks state.write gates and cites pkg/gate/testdata/facts.json for change.planned/1.0; these claims are not verified in the input and the ADR check does not cite the file for the state.write schema version. Resolution: answered: Rewrote evidence to cite only pack paths and the issue scope lines; fixed ADR quotes to exact spans.
- R1.10 (scope): The proposal's Out of scope lists OpenTofu paths beyond the depended slice, but the design's Decisions do not say whether the state.write evaluation at the Pulumi create reuses the OpenTofu handler code, so the boundary with the state-write-on-opentofu item is undefined. Resolution: answered: Added the reuse design decision that bounds this change against state-write-on-opentofu.
- R1.11 (open question): The Pulumi error shape with code and message is stated, but the change does not say which Pulumi CLI version it targets, although design.md Risks names CLI version drift in the refusal shape as unresolved. Resolution: answered: Considered list now records the CLI version answer: the e2e matrix versions with the same code and message shape.
- R2.1 (missing scenario): The state.write requirement does not name the operation.kind value of a person's Pulumi CLI create, although the ADR check and the adversarial answer R1.8 say it does. ADR 224 decision 16 gives the operation kind table. The build cannot match the gate facts without this value. Resolution: answered: State.write text now names operation kind update and the record holds it.
- R2.2 (missing scenario): ADR 335 decision 1 says a deny gate at state.write can refuse a Pulumi update cancel or force-unlock, and that no rule passes it. The spec has no scenario for cancel or force-unlock on the Pulumi path, so the refusal and its record are not defined. Resolution: answered: Added cancel and force-unlock deny scenarios under state.write per ADR 335 decision 1.
- R2.3 (missing scenario): ADR 224 decision 3 says a new requirement at change.apply puts the change back into the approval wait. The apply requirement has only a new deny scenario and a recheck-at-dispatch scenario. The approval-wait outcome is not covered. Resolution: answered: Added new-requirement scenario that returns the change to approval wait per ADR 224 decision 3.
- R2.4 (missing scenario): ADR 224 decision 13 covers an engine error in evaluate mode (it is recorded and does not stop the change). The spec has engine-error scenarios only for enforce mode, and no evaluate-mode engine error scenario at the three job points or at the Pulumi create. Resolution: answered: Added evaluate-mode engine-error scenarios at all four points: recorded with no stop.
- R2.5 (missing scenario): ADR 342 decision 3 with ADR 365 says a client-encrypted state body is not readable, so state.available is false and the write goes on under the gates. The state.write requirement does not cover a Pulumi update whose body is client-encrypted. Resolution: answered: Added client-encrypted scenario with state.available false and write going on per ADR 365.
- R2.6 (open question): The spec does not say how a refusal at the job points (change.propose, change.planned, change.apply) reaches the Pulumi CLI that started or watches the job. Only the CLI create has the 403 code and message shape. The error table in the proposal names no command for job refusals. Resolution: open question: Added job-refusal error entry and an open question on how the watcher surfaces the refusal.
- R2.7 (open question): The fact schema version of change.propose, change.apply and state.write is not named in the spec, though ADR 226 decision 2 requires a versioned schema per point in the record. The Open questions section answers this only in a note, not in a requirement. Resolution: open question: Each requirement now names the schema version in the record; open question covers the exact version strings for three points.
- R2.8 (wrong claim): The Why section gives code proofs (pkg/job/service.go:61, internal/api/tofuhttp/tofuhttp.go:108, pkg/gate/gate.go:178, pkg/gate/testdata/facts.json:85). The input contains no such code, yet the adversarial answer R1.9 says the evidence was rewritten to cite only paths and issue lines. The claims are unsupported and the proof is not shown. Resolution: answered: Evidence proofs now copy the pack code-fact lines exactly and cite the issue scope lines.
- R2.9 (scope): The design layer table requires a store port that keys gate rows by stable unit ID, with a rename hostile test. No task in tasks.md implements the store port or runs that rename test, so one of the three defense layers is unverified. Resolution: answered: Added store-layer rename task with unit-ID key check.
- R2.10 (failure mode): The spec does not cover a break-glass session that starts a Pulumi update. ADR 224 decision 17 says gates, approval rules and fixed controls hold for each change of such a session, and the job and create paths need a scenario for it. Resolution: answered: Added break-glass scenarios at job points and at state.write per ADR 224 decision 17.
- M.23 (dependency): The dependency 'state-write-on-opentofu' is not a change of this milestone, and the packs do not name it as an existing change. Resolution: answered: Replaced the unnamed dependency with the milestone changes add-job-service-routes-start-1 and state-write-state-facts-fixed; design reuse and out of scope now bound against those slices.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. How does a refusal at change.propose, change.planned or change.apply reach the Pulumi CLI that watches the job?
   - Options: Pulumi CLI watcher polls the job status and prints the refusal text from the job record; Pulumi CLI create path only; job refusals stay visible only in the API and the console.
   - Provisional decision (owner review 14 Nov): Pulumi CLI watcher polls the job status and prints the refusal text from the job record
   - Reason: Google engineering practice shows the error where the user runs the command and names the one record that explains it. Benchmark: Google
   - Persona impact: A developer who starts a Pulumi job from the API still sees the refusal through one documented read path and no extra setup. Escape hatch: The job record and the decision read stay available through the API even when the CLI shows only the refusal text.
   - Owner-tagged ADRs involved: ADR 224 (their words do not change).
2. Which fact schema versions do change.propose, change.apply and state.write use for Pulumi evaluations?
   - Options: Reuse the same major versions as the OpenTofu slice and name each version in the record; Freeze new versions change.propose/1.0, change.apply/1.0 and state.write/1.0 for Pulumi.
   - Provisional decision (owner review 14 Nov): Reuse the same major versions as the OpenTofu slice and name each version in the record
   - Reason: Google versioning practice keeps each schema version explicit so a reader can match the record to the schema. Benchmark: Google
   - Persona impact: A developer reads the schema version from the decision record with no extra lookup. Escape hatch: Each record names the schema version it used, so a reader can check the version even before the docs list every version.
   - Owner-tagged ADRs involved: ADR 226 (their words do not change).

Questions considered and answered:

- Do the four Pulumi points need a new decision point or an RFC? No. ADR 222 decision 2 and ADR 224 decision 2 fix the six points, and ADR 224 decision 16 covers the Pulumi update that a person creates. No RFC is needed.
- Which fact documents do the Pulumi evaluations use? ADR 226 decision 2 requires the versioned schema per point and the schema version in the record. The change reuses change.propose, change.planned, change.apply and state.write schemas.
- What do enforce mode, evaluate mode and engine errors do on Pulumi paths? ADR 224 decision 7: enforce mode gives its outcome, evaluate mode writes the record with no effect. ADR 224 decision 13 fails closed on engine error with gate.ErrRuleFailed.
- How does a deny at change.planned interact with the approval wait and the plan staleness check? ADR 222 decision 4: fixed controls stay fixed and the record shows the state of each fixed control that it checked. A deny refuses; it never opens an approval wait. The plan staleness check stays a fixed control.
- Which Pulumi CLI version does the refusal shape target? The supported Pulumi CLI versions are the versions that make e2e drives. The refusal keeps the shape the CLI already reads, code and message, so no version split is needed.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
