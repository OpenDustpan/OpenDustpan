---
version: 2
---

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
| A gate in enforce mode refuses a Pulumi CLI update create | The server answers 403 with `{"code": 403, "message": "change refused by gate <id>: <message>"}` and writes a decision record with result deny. | `dustpan gate decisions get <decision-id>` |
| A fixed control refuses at the same point as a gate | The server answers 403 with `{"code": 403, "message": "change refused by gate <id>: <message>"}` and the record shows the fixed control state. | `dustpan gate decisions get <decision-id>` |
| A gate refuses a Pulumi job at change.propose, change.planned or change.apply | The job stays refused, the job record holds the decision ID, and the decision read shows the deny text. | `dustpan job get <job-id>` |

Evidence for the claims above:

- The job service names the change.propose decision point in a comment. Proof: pkg/job/service.go:61:	// Point is the decision point of the gate: change.propose or state.write.
- The fact fixture uses schema change.planned/1.0. Proof: pkg/gate/testdata/facts.json:85:  "schema": "change.planned/1.0",
- The gate package names the change.apply point. Proof: pkg/gate/gate.go:178:	PointChangeApply DecisionPoint = "change.apply"
- The OpenTofu adapter checks the gates of state.write before a checked write. Proof: internal/api/tofuhttp/tofuhttp.go:108:	// with no lock against the access limits and the gates of state.write.
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
- Pulumi update cancel and force-unlock
- Pulumi stack import and other operation kinds

Depends on:

- add-job-service-routes-start-1
- state-write-state-facts-fixed
- `state-write-state-facts-fixed` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A refusal at four points can confuse: the CLI text must name the point, the gate and the one read command.
- Evaluate mode looks silent: the docs must show where the record appears.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Job service names change.propose and state.write points (`pkg/job/service.go:61`)
- Gate defines PointChangeApply as change.apply (`pkg/gate/gate.go:178`)
- OpenTofu adapter checks state.write gates (`internal/api/tofuhttp/tofuhttp.go:108`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request. A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation. A feature that a person or a client can see has a route, a setting, a command, a flag, a console page or an error that a user reads. A pull request that changes only internal code needs no page."
- Quote: "No page, not done. A reviewer refuses such a pull request when the page is not in it. A later pull request for the docs does not make the first one done."
- Effect: The change ships its docs page in the same pull request because the refusal text is visible to a person.
- Conflict: none.

**ADR 222: One model for access, gates, approvals and facts** (owner-tagged: decision 2, 4).

- Decisions that apply: 2 (current), 4 (current).
- Changed by: ADR 286 changes; ADR 335 extends decision 7; ADR 351 extends; ADR 354 changes decision 5. Chain to the end: ADR 286, ADR 335, ADR 351, ADR 354.
- Current rule: this ADR, with ADR 286, 335, 351, 354.
- Quote: "Six decision points (owner). `change.propose`, `change.planned`, `change.apply`, `profile.save`, `profile.open` and `state.write` (ADR 224 decision 2). The first three replace the kinds `catalog.request`, `job.start` and `changeorder.review`. `state.write` covers writes from a person's own CLI or CI and the tool-neutral state operations (ADR 224 decision 16, owner decision of 2026-10-18)."
- Quote: "Fixed controls stay fixed (owner). Workspace locks, manual locks, freezes with break-glass, override switches, module switches, quota reservations and quota counts, the source filter, safe delete (ADR 153 decision 5), the plan staleness check with the maximum plan age (ADR 102, ADR 191 decision 7) and the agent controls (modes, the confirmation step and the secret consent) are not gates."
- Effect: The change applies the fixed points to Pulumi jobs and Pulumi CLI writes. It adds no new point and no new part. Fixed controls stay fixed and appear in the record.
- Conflict: none.

**ADR 224: Gates at fixed decision points** (owner-tagged: decision 2, 13, 16).

- Decisions that apply: 2 (extended), 3 (current), 7 (current), 13 (current), 16 (changed), 17 (current).
- Changed by: ADR 241 extends decision 16; ADR 286 changes; ADR 313 extends decision 1; ADR 317 changes decision 1; ADR 335 changes decision 16; ADR 335 extends decision 9; ADR 335 extends decision 13; ADR 342 changes decision 16; ADR 353 changes decision 14; ADR 353 extends decision 2; ADR 354 extends. Chain to the end: ADR 241, ADR 286, ADR 313, ADR 317, ADR 335, ADR 342, ADR 353, ADR 354, ADR 248, ADR 343, ADR 319, ADR 365.
- Current rule: this ADR, with ADR 241, 286, 313, 317, 335, 342, 353, 354.
- Quote: "Modes. A gate in `enforce` mode gives its outcome. A gate in `evaluate` mode is evaluated and written in the record, with no effect. A new gate starts in `evaluate` mode unless the request names `enforce`."
- Quote: "Break-glass accounts meet the gates. A session of a break-glass account (ADR 70) is outside the access limits (ADR 223 decision 7). Gates, approval rules and fixed controls hold for each change of such a session."
- Effect: Pulumi jobs evaluate change.propose, change.planned and change.apply with a recheck at apply. The Pulumi update create evaluates state.write. Evaluate mode records without effect. An engine error fails closed in enforce mode.
- Conflict: none.

**ADR 226: Facts: typed, versioned fact documents** (owner-tagged: decision 1).

- Decisions that apply: 1 (current), 2 (changed).
- Changed by: ADR 240 changes decision 4; ADR 241 extends decision 4; ADR 258 extends decision 7; ADR 263 changes decision 7; ADR 286 changes; ADR 342 extends decision 4; ADR 353 changes decisions 2 and 4; ADR 353 changes decision 10; ADR 353 extends decision 13; ADR 354 extends decisions 3 and 4. Chain to the end: ADR 240, ADR 241, ADR 258, ADR 263, ADR 286, ADR 342, ADR 353, ADR 354, ADR 297, ADR 248, ADR 313, ADR 343, ADR 358, ADR 366, ADR 365, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 240, 241, 258, 263, 286, 342, 353, 354.
- Quote: "A fact (owner). A fact is a typed value that the server produces before a decision point. A fact never decides. Only a gate, an approval default, an ownership rule or a fixed control decides."
- Quote: "One schema for each decision point. Each decision point has a fact schema with a version. The version has the form `<decision point>/<major>.<minor>`, for example `change.planned/1.0`. The schema is a Cedar schema. The decision record names the schema version."
- Effect: Each evaluation uses the versioned fact schema of its decision point and names the schema version in the decision record. Facts never decide.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: task "Add e2e steps with the real Pulumi CLI for enforce and evaluate" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part>".

Questions considered and answered:

- Do the four Pulumi points need a new decision point or an RFC? No. ADR 222 decision 2 and ADR 224 decision 2 fix the six points, and ADR 224 decision 16 covers the Pulumi update that a person creates.
- What do enforce mode, evaluate mode and engine errors do on Pulumi paths? ADR 224 decision 7: enforce gives its outcome, evaluate records with no effect. ADR 224 decision 13 fails closed on engine error.
- How does a deny at change.planned interact with the approval wait? ADR 222 decision 4: a deny refuses with no wait. A new requirement returns the change to approval wait. The staleness check stays fixed.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
