Issue: #904.

## Why

**Persona: namespace administrator.** A namespace administrator groups file paths into named sets so gates and ownership rules route network, docs, and other areas to the right reviewers. Today each gate repeats raw glob text and a CLI write with no path list fails in an unclear way.

The user path this change must make work:

1. **API.** Save a path set for the namespace with PUT and If-Match, and read it back.
2. **API.** Push a change that edits modules/net/a.tf and read the path facts in its decision record.
3. **Docs page.** Follow the docs page to name sets and use touches and only in a gate.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Save of a path set with a bad ID, zero patterns, more than 64 patterns, or a reused wider scope ID fails. | The server says the path set is invalid with 400 and names the bad ID or pattern. Fix the ID or patterns and send the request again. | `GET /api/dustpan/v1/namespaces/{namespace}/path-sets` |
| Save of a path set with a stale If-Match version fails. | The server says the version is stale with 412. Read the path set again and save with the new version. | `GET /api/dustpan/v1/namespaces/{namespace}/path-sets` |
| A CLI write with no path list evaluates with missing path facts and a built-in template refuses. | The server says the decision used missing path facts and names the built-in gate that refused. Add a path list to the change or adjust the gate. | `GET /api/dustpan/v1/namespaces/{namespace}/path-sets` |

Evidence for the claims above:

- Issue 904 asks for named path sets with server-computed paths.touches and paths.only facts and typed missing facts. Proof: Issue #904 scope: the path set object, its routes and audit entries; fact computation from the changed paths of a change order; typed missing when no path list exists (for example a CLI write).
- Issue 904 acceptance needs touches true for modules/net/a.tf against modules/net/**, only true when every path matches, and fail closed on missing. Proof: Issue #904 acceptance criteria: a change that edits modules/net/a.tf sets paths.touches net true; paths.only docs is true only when every changed path matches; a CLI write with no path list gives a typed missing fact; a built-in template fails closed on it.
- ADR 226 holds the path set and fact rules for this change. Proof: docs/adr/0226-facts-typed-versioned-fact-documents.md

## What Changes

- Path set object with routes and audit entries at installation, namespace, collection, and unit scope.
- Set path set by ID with PUT and If-Match; a stale version gives 412, as rest-routes-ownership-rules-create does.
- New route GET /api/dustpan/v1/namespaces/{namespace}/path-sets to list path sets of a namespace.
- Server computation of paths.touches and paths.only from the changed paths of a change order.
- Typed missing path facts with available false and fail-closed built-in templates.
- Docs page for path sets.

Out of scope:

- Other fact groups beyond paths.touches and paths.only
- Ownership rule and gate authoring beyond reading path facts
- Pull request automation and comment rendering
- Decision record read route, owned by read-route-decision-records-change

Depends on:

- #899
- read-route-decision-records-change
- `read-route-decision-records-change` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- Pattern dialect surprises remain until the docs page shows tested glob samples.
- Missing path facts confuse CLI users unless the refusal names the decision record.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 226: Facts: typed, versioned fact documents** (owner-tagged: decision 3, 13).

- Decisions that apply: 3 (extended), 13 (extended), 14 (current).
- Changed by: ADR 240 changes decision 4; ADR 241 extends decision 4; ADR 258 extends decision 7; ADR 263 changes decision 7; ADR 286 changes; ADR 342 extends decision 4; ADR 353 changes decisions 2 and 4; ADR 353 changes decision 10; ADR 353 extends decision 13; ADR 354 extends decisions 3 and 4. Chain to the end: ADR 240, ADR 241, ADR 258, ADR 263, ADR 286, ADR 342, ADR 353, ADR 354, ADR 297, ADR 248, ADR 313, ADR 343, ADR 358, ADR 366, ADR 365, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 240, 241, 258, 263, 286, 342, 353, 354.
- Quote: "A path set is a stored object of a scope (the installation, a namespace, a collection or a unit) with an ID and 1 to 64 glob patterns."
- Quote: "A missing fact is a typed value (owner)."
- Quote: "A fact document holds at most 256 KiB."
- Effect: The change stores path sets and computes touches and only facts under decision 13, returns typed missing values under decision 3 with fail-closed templates, and truncates large documents under decision 14 with truncated facts treated as missing.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Effect: The change ships a user docs page in the same pull request because path sets have routes and errors that a person reads.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): proposal.md: the glob dialect is open (Open questions item 1, owner decides) yet the spec and tasks already fix matching semantics (modules/net/** matches modules/net/a.tf, docs/** only); the change cannot be built without the owner's dialect answer, so the spec-ready gate is not met. Resolution: open question: Glob dialect stays an owner open question; spec scenarios use only the two literal patterns from issue 904 that match the same under either candidate, and the docs task records the chosen dialect with tested samples.
- R1.2 (wrong claim): proposal.md: the Errors table and the user path name GET /api/dustpan/v1/namespaces/{namespace}/path-sets and .../decision-records/{id} as the helping commands; the code check reports both routes are absent from the API description and route table, so the error answers point to non-existent routes. Resolution: answered: Both helping routes are new routes that this change adds, named in what_changes; the error answers point to the routes this change wires with authorization actions and audit entries.
- R1.3 (missing scenario): specs/path-sets/spec.md: no requirement covers a path set whose ID or pattern list is invalid on save (0 patterns, 65 patterns, bad ID, reused wider scope ID), though design layers and ADR 226 decision 13 require refusal with gate.ErrInvalid; the refusal path has no scenario or failing test in tasks.md. Resolution: answered: Requirement Reject invalid path sets covers 0, 65, bad ID and reused wider scope ID with 400 that names the bad value, with failing test TestPathSetValidation.
- R1.4 (missing scenario): specs/path-sets/spec.md: no scenario for a write with no grant (403 and denial audit entry), though the design's service layer requires it and the Why names authorization actions for every endpoint. Resolution: answered: Requirement Deny writes without a grant covers 403 and denial audit entry, with failing test TestPathSetAuthz.
- R1.5 (failure mode): design.md Risks and specs: a change with more files than fit in the 256 KiB fact document has no rule; ADR 226 decision 14 requires truncation of change.paths with document.truncated, but neither the spec nor tasks mention it, so truncated path facts could be read as complete for only. Resolution: answered: Requirements Truncate large path lists and Treat truncated path facts as missing apply the 256 KiB rule to change.paths and mark path facts unavailable, with failing test TestPathSetTruncation.
- R1.6 (missing scenario): specs/path-sets/spec.md: Compute touches and only do not state an empty changed path list, a path that is not clean, or the behavior when a set is deleted between evaluations. Resolution: answered: Empty path list, unclean paths through the ChangedPaths value object, and deleted sets between evaluations now have scenarios under Compute touches and only and Missing path list.
- R1.7 (failure mode): design.md layers: concurrent saves of the same path set ID at two scopes (race on the scope ID reuse check) are not covered by any hostile test or failure mode. Resolution: answered: Port adapter layer serializes concurrent saves of one ID and the hostile test covers the save-save race and the save-delete race with TestPathSetScopeRace.
- R1.8 (missing scenario): specs/path-sets/spec.md: no scenario for reading or updating a path set at a narrower scope, or for the rule that a stored reference uses the stable ID when a set is renamed or removed and made again. Resolution: answered: Requirement Read sets by stable ID covers narrower scope read and update and stable ID use after remove and remake, including no reuse of a removed wider ID.
- R1.9 (scope): tasks.md 1.2 and 2.1: the port tests and the fact tests share the same check command (TestPathSetFacts), so the scenario-to-test mapping for pattern count, ID grammar and the HTTP size limit is not established. Resolution: answered: Tasks map TestPathSetFacts, TestPathSetValidation, TestPathSetAuthz and TestPathSetTruncation to distinct requirements with distinct check commands.
- R1.10 (open question): design.md Structure: the HTTP size limit for pattern text and the cap on a changed path list are not given a value anywhere, so the fail-closed rule for oversize input has no testable threshold. Resolution: open question: Size caps stay a PM open question; the HTTP handler refuses oversize input against the recommended caps and the spec states only the 256 KiB document rule.
- R2.1 (open question): proposal.md: the glob dialect is still open (Open questions item 1) while specs and tasks already fix matching semantics; the change cannot be built without the owner's dialect answer, so the spec-ready gate is not met. Resolution: open question: Glob dialect stays an owner open question; specs use only the literal patterns from issue 904 that match the same under either candidate, and the docs task records the chosen dialect with tested samples.
- R2.2 (open question): proposal.md: the size caps (pattern document 8 KiB, changed path list 4096 entries) are provisional, yet the HTTP handler layer and the truncation rule depend on them; no ADR 226 decision sets these thresholds, so the fail-closed test has no fixed threshold. Resolution: open question: Size caps stay a PM open question; the HTTP handler refuses oversize input against the recommended caps and the spec does not hard-code the cap values beyond the 256 KiB document rule.
- R2.3 (adr conflict): specs/path-sets/spec.md Truncate large path lists: the scenario truncates change.paths 'largest first' and keeps counts, but ADR 226 decision 14 empties the largest sets of strings only until the document fits and names the emptied path in document.truncated; the spec adds no rule that touches and only from a truncated list must be marked partial, so truncated facts can still read as complete. Resolution: answered: Truncate requirement now empties the largest string sets until the document fits, keeps each count, and adds the emptied path to document.truncated per ADR 226 decision 14.
- R2.4 (failure mode): design.md Layers: truncation of change.paths happens before fact computation, so a path that matches only in the dropped tail gives touches false and only true for a gate. No requirement states that a gate reading touches or only on a truncated document must treat the fact as unavailable (fail closed). Resolution: answered: New requirement Treat truncated path facts as missing makes touches and only unavailable when change.paths was truncated, so gates fail closed.
- R2.5 (missing scenario): specs/path-sets/spec.md Missing path list: no scenario for a change whose path list is present but whose path set was deleted or changed between evaluations; the proposal lists this case but the spec does not state the result. Resolution: answered: Missing path list now covers a deleted or changed path set version between evaluations with a typed missing result.
- R2.6 (missing scenario): specs/path-sets/spec.md Reject invalid path sets: the scenario names gate.ErrInvalid but gives no HTTP status code, and the proposal's error table says the answer names the bad ID or pattern; no requirement covers the response body. Resolution: answered: Reject invalid path sets now states 400 and a body that names the bad ID or pattern.
- R2.7 (failure mode): design.md Layers, port adapter: the concurrent-save serialization is stated for one ID but no requirement or task covers a race between a save and a delete of the scope object that the set belongs to. Resolution: answered: Port adapter layer and Store path sets now cover a save that races a delete of its scope, with failing test TestPathSetScopeRace.
- R2.8 (wrong claim): specs/path-sets/spec.md Read sets by stable ID: the scenario says the old name label does not redirect, but ADR 226 decision 13 says a narrower scope cannot reuse a wider ID; the scenario does not state the behaviour when a narrower-scope set is created with the removed set's ID. Resolution: answered: Read sets by stable ID now states that a narrower scope cannot reuse the removed wider ID and the read uses the stable ID after remake.
- R2.9 (scope): tasks.md 2.3 and 3.1: the docs page is a required task, but no task names the glob sample tests that the proposal's usability section requires to show tested glob samples. Resolution: answered: Tasks now include tested glob samples in the docs task with check TestPathSetGlobSamples.
- R2.10 (missing scenario): specs/path-sets/spec.md: no requirement covers a path set read or list by a principal without a read grant, although the design says every endpoint declares its authorization actions. Resolution: answered: New requirement Deny reads without a grant covers read and list with 403 and denial audit entries.
- M.16 (duplicate): The decision-record read route duplicates read-route-decision-records-change. The save uses POST, but the other rule objects use PUT with If-Match. Resolution: answered: Removed the decision-record read route from this change and added a dependency on read-route-decision-records-change; path set saves now use PUT with If-Match and a stale version gives 412, with a new requirement and error entry.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which glob dialect matches changed paths against path set patterns?
   - Options: Doublestar glob dialect with ** across separators; Gitignore dialect with anchored and negated patterns.
   - Provisional decision (owner review 14 Nov): Doublestar glob dialect with ** across separators.
   - Reason: ADR 226 decision 13 says glob patterns but names no dialect, and pattern matching decides touches and only facts. Benchmark: Google uses gitignore-style glob semantics with explicit docs for ** and character classes in its code review policy engine.
   - Persona impact: Namespace administrator writes patterns from the docs page alone; a surprise match misroutes approvals. Escape hatch: A path set with no matching gate still stores and lists; nothing else changes when the dialect choice is wrong, and spec scenarios use only the literal patterns from issue 904 that match the same under either candidate.
   - Owner-tagged ADRs involved: ADR 226 (their words do not change).
2. What size caps refuse an oversize path set save and bound the changed path list before truncation?
   - Options: Pattern document at most 8 KiB and changed path list at most 4096 entries; Pattern document at most 32 KiB and changed path list at most 16384 entries.
   - Provisional decision (owner review 14 Nov): Pattern document at most 8 KiB and changed path list at most 4096 entries.
   - Reason: ADR 226 decision 14 bounds the fact document at 256 KiB but names no per-save pattern text cap or per-change path count cap. Benchmark: AWS documents explicit request size limits with a named error and a retry path when a policy document exceeds its quota.
   - Persona impact: Namespace administrator gets a clear refusal that names the limit; no surprise truncation of a saved set. Escape hatch: An oversize save is refused with no side effect; the author shortens the patterns and saves again.
   - Owner-tagged ADRs involved: ADR 226 (their words do not change).

Questions considered and answered:

- What is a path set and what happens with no path list or a large path list? ADR 226 decision 13 says a path set is a stored object of a scope with an ID and 1 to 64 glob patterns, decision 3 says a missing fact is a typed value with an available flag and built-in gates fail closed, and decision 14 bounds the fact document at 256 KiB.
- Is a docs page needed? ADR 214 decision 1 says a pull request that adds a feature that a person can see adds its page in the user documentation, so the docs page is in scope.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
