Issue: #1257.

## Why

**Persona: developer.** A Developer whose state write is refused needs to open the decision record that the refusal names, to see which gate refused and what to fix next.

The user path this change must make work:

1. **API.** Developer runs a state write through the tool API and gets a refusal that names a decision record ID.
2. **API.** Developer opens the record through the new read route to see which gate refused and why.
3. **Docs page.** Developer follows the docs page that shows the read call as written.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Caller with namespace access but without the gate read action reads an existing record of the namespace | 403 with code gate.denied and the record ID; the body links to the record read route and names the docs read command. | `curl /api/dustpan/v1/namespaces/{namespace}/decision-records/{id}` |
| Caller with no namespace access or no sign-in reads any record ID, or any caller reads a missing, expired, malformed, or foreign ID | 404 with code gate.not-found and no record content. | `curl /api/dustpan/v1/namespaces/{namespace}/decision-records/{id}` |

Evidence for the claims above:

- Issue #1257 asks for the read route of ADR 229 decision 2 with its actions. Proof: Issue #1257: A refusal at `state.write` names a decision record ID, but no route reads a record. The user guide says so. Add the read route (ADR 229 decision 2) with its actions.
- The state.write decision point exists in the server code. Proof: rg -n -F 'state.write' shows pkg/job/service.go:61, pkg/job/service.go:89, pkg/job/service.go:91, and cmd/opendustpan/main.go:1811.

## What Changes

- New route GET /api/dustpan/v1/namespaces/{namespace}/decision-records/{id}: read one decision record by gdr- ID with the namespace gate read action and installation:read-gates for installation limit records.
- Return stable errors with the record ID and link; 403 gate.denied for a denied in-scope read and 404 gate.not-found for missing, expired, malformed, foreign, or no-access reads with no content.
- Change the approval refusal to name the record ID and the record read command and to name no change-order command until the change-order slice ships it.

Out of scope:

- Write routes for decision records; records stay append-only by the server.
- Simulate route and starter packs.
- Console run page and CLI output for records.
- The real change-order command; the approval refusal names the record read command until the change-order slice ships it.

## Usability

Where the path still costs the person time or surprises them:

- A Developer who copies the record ID from a refusal finds the read route on the docs page without knowledge of gate scopes.
- A refusal that names only the record read command still needs one more step to reach the fix until the change-order slice adds the real command.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 229: Decision records, the simulate route and starter packs** (owner-tagged: decision 1).

- Decisions that apply: 1 (current), 2 (current), 5 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "Every block is explained. Each refusal, each approval wait and each warning of a change names the ID of its record. The API error, the console and the CLI link to it."
- Quote: "One record for each evaluation (owner). Each evaluation of a decision point for one change writes one decision record, with the ID prefix `gdr-`."
- Quote: "Storage. A record is namespace data. The store checks the fence and writes it in the same transaction as the step that it explains."
- Effect: The change adds the read side of the record rule: stored records are read back with the gate read actions, and each refusal, wait and warning names its record ID with a link.
- Conflict: none.

**ADR 230: Gate configuration: actions, routes, errors and audit entries** (not owner-tagged).

- Decisions that apply: 2 (current), 3 (changed), 4 (extended).
- Changed by: ADR 268 changes decision 3; ADR 286 changes; ADR 335 extends decision 4; ADR 355 changes decision 3; ADR 355 changes decision 4. Chain to the end: ADR 268, ADR 286, ADR 335, ADR 355.
- Current rule: this ADR, with ADR 268, 286, 335, 355.
- Quote: "`installation:read-gates` | the installation | read the gates, limits, approval rules, ownership rules and path sets of the installation |"
- Quote: "Routes (PM). Under the prefix of ADR 210, the scope is part of the path."
- Quote: "Errors. `pkg/gate` holds the sentinels, with these status codes and stable error codes (ADR 182):"
- Effect: The new read route lives under the ADR 210 prefix with the scope in the path, declares its read actions, and uses the gate error table with the record ID in each error.
- Conflict: none.

**ADR 355: Gate configuration paths and errors as shipped** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A gate configuration error that wraps `gate.ErrNotFound` has the status 404 and the code `gate.not-found`."
- Effect: Missing, expired, malformed and out-of-scope record IDs use the shipped 404 shape for gate objects.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): Spec-ready gate fails: proposal.md Open questions says a code check fails for the read route path (.errors[0].command and .tasks[1].items[0].check); the route is not in the API description, so the change cannot be built until the route is decided against the API description. Resolution: answered: The route is new and is now named in what_changes as New route GET /api/dustpan/v1/namespaces/{namespace}/decision-records/{id}; installation records use the installation scope.
- R1.2 (missing scenario): No requirement or scenario for a caller who lacks the gate read action on an existing record: the proposal table says 403 gate.denied, but the spec scenario only covers 404 for missing or foreign records, so the two outcomes conflict. Resolution: answered: Added requirement Denied read in scope with scenario No read action: an in-scope caller without the read action gets 403 gate.denied with no content beyond the ID and link.
- R1.3 (adr conflict): ADR 230 decision 4 (current) maps gate.ErrDenied to 403 gate.denied and gate.ErrNotFound-style errors per ADR 355; design decision 1 maps not-allowed reads to 404 while decision 2 maps them to 403 gate.denied. The design contradicts itself on the status of a denied read and the ADR error table. Resolution: answered: Design decision 1 now gives 403 for denied in-scope reads per ADR 230 decision 4 and 404 only for missing or out-of-scope IDs per ADR 355; decision 2 maps the sentinels to the same codes.
- R1.4 (missing scenario): The refusal scenario says the error body holds the gdr- ID and a link, but no requirement covers the approval-wait and warning blocks that ADR 229 decision 2 also requires to name the record ID; the scope of the link requirement is incomplete. Resolution: answered: Added requirement Each block names its record with scenarios Denied state write, Propose and apply refusals, and Approval wait and warning: each refusal, wait, and warning at each decision point carries the gdr- ID and link.
- R1.5 (failure mode): Hostile input is covered only for ID path separators and SQL metacharacters; no case for a malformed or non-gdr- prefixed ID, a percent-encoded namespace segment, or an ID of a record whose namespace is renamed or removed and recreated (stable ID rule in openspec/project.md). Resolution: answered: Added a not-found requirement with scenarios for malformed IDs, percent-encoded scope segments, and stable gdr- IDs after rename or remove-and-make-again; hostile tests cover the same inputs.
- R1.6 (failure mode): Records are retained for the audit retention of ADR 68 for state.write; a read after expiry or after backup restore has no defined response, so the link in the refusal can point to a record that no longer exists. Resolution: answered: Added the expired-record 404 scenario under the audit retention of ADR 229 decision 5, so a refusal link to a purged record gives gate.not-found.
- R1.7 (open question): Read of a record of an access limit on an installation object requires installation:read-gates (ADR 229 decision 5 and ADR 230 decision 2), but the design names only the namespace gate read action; which action guards each record scope is not stated. Resolution: answered: Design decision 2 and a Where requirement state that records of access limits on installation objects need installation:read-gates per ADR 229 decision 5.
- R1.8 (scope): Tasks 2.2 asks for the API description and docs page, but the proposal says the docs page must show the read call as written; no task verifies the docs command runs as written (project.md delight test). Resolution: answered: Added a docs task whose check runs the docs read command as written and opens the record from it.
- R1.9 (wrong claim): Proposal evidence cites pkg/job/service.go and cmd/opendustpan/main.go line numbers for state.write, but the 'no route reads a record' claim has no rg proof for the absence of any read route for gdr- records. Resolution: answered: Evidence claim 1 is now the request in issue #1257 quoted verbatim, not a code-absence claim; the pack supplies no API description entry for a decision-record read, which the route check confirms.
- R2.1 (adr conflict): Spec 'Read one decision record' and design decision 1 give 403 for an in-scope denied read, but ADR 229 decision 5 and ADR 230 decision 2 limit record access to the gate read actions; the spec 'Installation record guard' requires installation:read-gates but design Layers row only names installation:read-gates for installation records, leaving namespace-scope records' guard (gate read action on namespace) unnamed in the spec scenarios. Specs and design disagree on which action guards the record scope. Resolution: answered: Named the guard per scope: namespace records need the namespace gate read action and installation limit records need installation:read-gates, in the design, in the Installation record guard and Read namespace record requirements, and in the handler layer.
- R2.2 (open question): The approval refusal keeps the sentence naming a non-existent CLI command ('open a change order ... with the OpenDustpan CLI'); the proposal says the text surprises the user, yet the change ships it unchanged and no requirement says what the user should do instead. The delight rule requires each error to name the one command that helps. Resolution: answered: Replaced the non-existent change-order sentence with an interim refusal that names the record ID and the docs read command; added the Approval refusal interim command requirement and kept the real command out of scope.
- R2.3 (missing scenario): No scenario covers a caller with no namespace access at all (not just the read action) reading an existing in-scope record; the 403 versus 404 split for unauthenticated callers is not defined, which leaks existence of record IDs across scopes. Resolution: answered: Added the No namespace access requirement with 404 for callers with no namespace access and for unauthenticated callers, so existence does not leak.
- R2.4 (wrong claim): The design cites ADR 230 decision 4 for 403 gate.denied, but the proposal ADR check quotes only ADR 230 decision 2 and 3; the 'Changed by' chain lists ADR 355 changes decision 4, which the ADR check does not quote, so the claim that the error table is unchanged is unverified. Resolution: answered: Extended the ADR check to quote ADR 230 decision 4 and ADR 355 decision 2 verbatim and to list the Changed-by effect on the error table.
- R2.5 (failure mode): Concurrency with retention purge: a record read racing with the expiry purge or a backup restore is not covered; the response in that window is undefined. Resolution: answered: Added the Race with expiry requirement: a read that races retention purge or restore without the record gives 404 gate.not-found.
- R2.6 (missing scenario): The 'Each block names its record' requirement covers only state.write; ADR 229 decision 2 applies to every decision point, but no scenario covers change.apply or change.propose refusals. Resolution: answered: Widened Each block names its record to change.propose, change.planned, change.apply and state.write with a scenario for each point.
- R2.7 (scope): Task 2.2 requires the API description and docs page but the check is only openspec validate, which does not verify the API description or the docs page content. Resolution: answered: Split the wiring task into an API description check with make generate and a docs page check with make docs-lint plus a run-as-written curl check.
- R2.8 (open question): The redaction rule for a record that holds fact documents with secrets or path sets is not stated; the read route returns the full stored record, including fact documents, to any caller with the read action. Resolution: answered: Added the Secret masking requirement: the read masks secret values in fact documents unless the caller holds the secrets read action of the scope.

## Open questions

none

Questions considered and answered:

- Which route path does the read use? New route GET /api/dustpan/v1/namespaces/{namespace}/decision-records/{id} for namespace records; installation limit records are read through the installation scope with installation:read-gates per ADR 229 decision 5.
- When does a record read give 403 and when 404? An in-scope caller with namespace access but without the gate read action gives 403 gate.denied per ADR 230 decision 4; a caller with no namespace access, an unauthenticated caller, or a missing, expired, malformed, or foreign ID gives 404 gate.not-found per ADR 355 with no content, so the route does not disclose record existence.
- Does the record ID link cover waits and warnings too? ADR 229 decision 2 requires each refusal, approval wait and warning to name its record ID at every decision point; the spec covers change.propose, change.planned, change.apply and state.write.
- What does a read give after retention expiry or during a race with purge? ADR 229 decision 5 keeps state.write records for the audit retention; a read that races expiry, purge, or restore without the record gives 404 gate.not-found.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
