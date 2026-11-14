Issue: #905.

## Why

**Persona: namespace administrator.** A namespace administrator tests policy before it blocks work. Today a draft gate can only go live in enforce mode and surprise teams. This change lets the administrator replay drafts against past decisions and start from packs that stay safe in evaluate mode.

The user path this change must make work:

1. **Docs page.** Read the docs page for simulate and starter packs.
2. **API.** Post draft gates and draft approval rules with a record filter to the simulate route.
3. **Console.** List starter packs and install one pack, then review its gates in evaluate mode in the console.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| 403 on simulate when the caller lacks gate read rights | Access refused. The principal cannot use the simulate route. Ask a namespace administrator for gate read rights. | `GET /api/dustpan/v1/namespaces/{namespace}/gates` |
| 422 on simulate when the range matches more than 1,000 records | Filter invalid. Narrow the time range and send the request again. The server never truncates a range that matches more than 1,000 records. | `POST /api/dustpan/v1/namespaces/{namespace}/gates/simulate` |

Evidence for the claims above:

- Issue 905 asks for a simulate route over stored decision records and starter packs installed in evaluate mode. Proof: #905 Scope: POST .../namespaces/{namespace}/gates/simulate over stored decision records; starter packs shipped as data, installed in evaluate mode.
- ADR 229 defines the simulate route and the starter packs. Proof: docs/adr/0229-decision-records-simulate-and-starter-packs.md decisions 6 and 7.
- Issue 905 needs a docs page. Proof: #905 Acceptance criteria: Docs page (ADR 214).

## What Changes

- POST /api/dustpan/v1/namespaces/{namespace}/gates/simulate over stored decision records with stored and new results per record
- Starter gate packs shipped as data with list and install routes in evaluate mode with designed-against wording
- Docs page for simulate and starter packs

Out of scope:

- Compliance claims for packs; packs never say compliant
- Enforce mode install of pack gates
- Changes to live gates, jobs, or change orders from simulate

Depends on:

- add-gate-configuration-routes
- read-route-decision-records-change
- `add-gate-configuration-routes` (order of the milestone review)
- `read-route-decision-records-change` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- Policy authors must learn the record filter fields; examples on the docs page show a decision point with a time range.
- Skipped records leave no trace, so an author with narrow reach sees fewer rows with no signal about hidden changes.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 229: Decision records, the simulate route and starter packs** (owner-tagged: decision 6, 7).

- Decisions that apply: 6 (current), 7 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "`POST .../namespaces/{namespace}/gates/simulate` takes draft gates, draft approval rules and a filter of stored records"
- Quote: "It evaluates the drafts against the stored fact documents, and gives for each record the stored result and the new result."
- Quote: "A starter pack is a set of gates and approval rules that the server ships."
- Quote: "A pack installs in `evaluate` mode."
- Effect: The change follows decisions 6 and 7. ADR 286 carries no numbered decisions and only renames catalog and promotion, so it does not alter decisions 6 and 7.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Effect: The change ships its docs page in the same pull request.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (adr conflict): ADR 229 decision 6 says the simulate route skips unreadable records 'with no trace of them in the answer'. The spec (spec.md, Skip unreadable changes) and the proposal keep a skipped count in the answer. The count is a trace, so the spec conflicts with an owner-tagged decision. Resolution: answered: Removed the skipped count from the spec, design and usability; skipped records now leave no trace per ADR 229 decision 6.
- R1.2 (failure mode): The skipped count tells a caller that hidden changes exist and how many. Design risks and the hostile test (no row, no ID) do not cover this leak of existence. A count of zero or a hidden count is not defined. Resolution: answered: Removed the existence signal; the handler guard and hostile test now state no row, no ID and no count.
- R1.3 (open question): The spec and design disagree on pack gates that ask for enforce. design.md says install refuses any pack gate that asks for enforce. spec.md says install makes each gate in evaluate mode. The required behavior (refuse or rewrite) is not decided. Resolution: answered: Decided refuse: install refuses any pack gate that asks for a mode other than evaluate; spec and design now agree.
- R1.4 (missing scenario): No route, authorization action or object is defined for installing a starter pack or listing packs. The console install path in the proposal and tasks 2.2 depend on an endpoint that the change never declares, as the project rule for endpoints requires. Resolution: answered: Added pack list and pack install routes with authorization actions to the spec and tasks.
- R1.5 (open question): ADR 229 decision 6 replays draft gates and draft approval rules. The spec and tasks cover only draft gates. Replay of approval rules and their result per record is not specified. Resolution: answered: Added draft approval rules to the simulate requirement and scenarios.
- R1.6 (missing scenario): The 1,000-record limit and the 422 error for an invalid filter (time range, decision point) have no spec scenario. The proposal lists the 422 error, but the spec does not require it. Resolution: answered: Added the 1,000-record limit and invalid filter scenario with 422.
- R1.7 (missing scenario): No scenario covers a pack install when a gate name already exists in the namespace, or a re-install or upgrade of a versioned pack. Resolution: answered: Added the name-conflict and reinstall scenario for pack install.
- R1.8 (missing scenario): The design hostile test (a draft gate with a write side effect stores nothing) is not a spec scenario and not a task test. Only route-level no-side-effect is covered. Resolution: answered: Added the write side-effect scenario and a task test for it.
- R1.9 (missing scenario): design.md says the pack loader rejects compliance wording and enforce mode at load. The spec tests only the shipped text in CI, so a hostile pack loaded at runtime has no requirement. Resolution: answered: Added the runtime loader refusal requirement for hostile packs.
- R1.10 (failure mode): Replay of up to 1,000 records has no requirement for timeout, cancellation, partial results or a concurrent change to draft gates during replay. Resolution: answered: Added bounded replay from one snapshot with a timeout and no partial writes.
- R1.11 (wrong claim): The error table names CLI commands (dustpan access check, dustpan gates simulate with a --limit flag). The input gives no evidence that these commands exist or accept these flags. Resolution: answered: Replaced unproved CLI commands with API calls in the error table.
- R1.12 (wrong claim): The ADR check says ADR 286 changes the effect of decisions 6 and 7 'without change', but the ADR 286 text in the input has no decisions, so the effect on decisions 6 and 7 is not shown. Resolution: answered: Corrected the ADR 286 effect: it carries no numbered decisions and does not alter decisions 6 and 7.
- R2.1 (missing scenario): The spec does not say whether a pack install is all-or-nothing. The name-conflict scenario refuses only that gate, while design.md refuses the whole install when any pack gate asks for another mode. A partial install on a name clash is not defined. Resolution: answered: Pack install is now all-or-nothing: name-conflict scenario refuses the whole install with no gate made; design decision 2 updated to match.
- R2.2 (missing scenario): Design decision 2 says packs are versioned data, but no requirement covers re-install or upgrade of a versioned pack, or what happens to gates made by an earlier pack version. The reinstall scenario that the adversarial pass claims to have added is absent. Resolution: answered: Added versioned-pack reinstall requirement: same pack ID and version reinstall is refused with 409 and no change; install is atomic.
- R2.3 (failure mode): No requirement covers replay timeout, cancellation, or a concurrent edit of drafts during replay. The design hostile test and the risk section name these, and the adversarial pass claims they were added, but the spec has no such scenario and the tasks do not test them. Resolution: answered: Added bounded replay requirement with one snapshot, timeout and cancel scenario, and failing-test task.
- R2.4 (wrong claim): The routes for simulate, pack list and pack install are not given authorization actions or objects in the spec, as the project rule for endpoints requires. Tasks 2.1 and 2.2 claim these exist but the spec never declares them. Resolution: answered: Declared authorization actions and objects in spec text: simulate and pack list need namespace:read-gates on the namespace, pack install needs namespace:manage-gates on the namespace.
- R2.5 (wrong claim): The 422 error tells the caller to lower the record count, but the filter has no record-count input in the spec or design. The filter caps records only through the time range, so the help text names a control that does not exist. Resolution: answered: Fixed 422 help text to name only the time range; filter has no record-count input and the server never truncates.
- R2.6 (open question): Stored fact documents carry a fact schema version and path set versions (ADR 229 decision 1, ADR 226 decision 2). The change does not say how replay treats a record whose schema version differs from the current one. Resolution: answered: Added schema-version rule: records whose fact schema version the engine cannot read are skipped with no trace, covered in replay-filter requirement.
- R2.7 (failure mode): Two concurrent installs of the same pack, or a pack gate and a manual gate with the same name, can both pass the name check. No requirement covers this race. Resolution: answered: Added atomic install requirement with unique name and version checks in one transaction plus concurrent-install scenario and test.
- R2.8 (missing scenario): The bounded replay rule refers to a filter that names more than 1,000 records, but a filter names a time range, not a count. The spec does not say whether a range that matches more than 1,000 records is refused or truncated. Resolution: answered: Clarified bounded replay: a range that matches more than 1,000 readable records is refused with 422, never truncated.
- M.38 (dependency): The dependency is the issue ID #899, but the route needs the gate routes and the decision records. Resolution: answered: Replaced issue-ID dependency with change dependencies on add-gate-configuration-routes and read-route-decision-records-change, which supply the gate routes and stored decision records this change replays.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which stored grant guards POST gates/simulate: read-gates or manage-gates?
   - Options: namespace:read-gates; namespace:manage-gates.
   - Provisional decision (owner review 14 Nov): namespace:read-gates
   - Reason: Simulate stores nothing and changes no change, so read access matches least privilege and matches the read of decision records. Benchmark: AWS Verified Permissions gives a test bench that runs a policy set against a request with no effect.
   - Persona impact: Read rights let a policy author simulate without the right to change gates; manage rights upset authors who only review policy. Escape hatch: A namespace administrator with gate manage rights can still test by saving a gate in evaluate mode and reading live decision records.
   - Owner-tagged ADRs involved: ADR 229 (their words do not change).

Questions considered and answered:

- How does a policy author test a draft gate before enforce? ADR 229 decision 6 replaces draft-rule replay with the simulate route; drafts are evaluated against stored fact documents with no stored change, skipped records leave no trace.
- What wording and mode do starter packs use? ADR 229 decision 7 says packs are worded designed against a named framework, never claim compliance, install in evaluate mode, and each gate has a test file run by gate.RunTestFile.
- Does this change need a docs page? ADR 214 decision 1 needs a docs page in the same pull request for routes a person can see.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
