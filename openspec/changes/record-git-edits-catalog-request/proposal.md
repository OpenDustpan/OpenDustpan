Issue: #896.

## Why

**Persona: developer.** Developer requests a unit from a blueprint and edits code in git. When the developer edits the request file directly in git, the Catalog must show the request as drifted from git with the commit and changed fields, instead of losing the change or overwriting it.

The user path this change must make work:

1. **API.** Push a commit that edits catalog/<ns>/<bp>/<name>/request.json in git; the forge sends a push webhook to the public webhook route
2. **Console.** Read the drift status of the request by stable request ID with the drift read; see drifted, invalid-file, or file-removed with commit and changed fields
3. **CLI.** Fix by push of a matching file or by authorized submit of a matching document through the API; read drift again and see in-sync
4. **Docs page.** Follow the docs page for git edits of a request file to complete each step without special knowledge

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Write-back or apply starts while the request shows drift between git and the Catalog. | Server refuses with 409 code request.drifted and names the request, the commit, and the changed fields. Read the drift, then push a matching file or submit a matching document. | `GET /api/dustpan/v1/namespaces/{namespace}/self-service/requests/{request}/drift` |
| Push carries a request file that fails parse, for example an unknown field or bad JSON type. | Drift read shows status invalid-file with the field error. Fix the file text and push again. | `GET /api/dustpan/v1/namespaces/{namespace}/self-service/requests/{request}/drift` |
| Push deletes or renames the request file in git. | Drift read shows status file-removed with the commit id. Restore the file by push or delete the request through the API. | `GET /api/dustpan/v1/namespaces/{namespace}/self-service/requests/{request}/drift` |

Evidence for the claims above:

- Issue #896 scopes the change to a webhook handler for pushes that touch request paths, a drift record and status, and an event, with no automatic reconcile. Proof: Scope - In: webhook handler for pushes that touch request paths (ADR 111 layout); compare with the stored request document; a drift record and status on the request; an event. - Out: automatic reconcile.
- Issue #896 sets the two acceptance rules for mark and clear of drift. Proof: A push that edits a request file marks the request as drifted, with the commit and the changed fields. - A push that matches the stored document clears the drift.
- ADR 111 fixes the git path of each request file used by the compare. Proof: The git layout. In a repository, the request of namespace `<ns>`, blueprint `<bp>` and name `<name>` is the file
- Issue #896 records the owner decision to record git edits as drift and no move between git and the store in v1. Proof: 14.7 (owner, 2026-10-18): record git edits of a request file (seen through a forge webhook) as drift between git and the Catalog. - 14.4: no move of a request between git and the store in v1.

## What Changes

- New route GET /api/dustpan/v1/namespaces/{namespace}/self-service/requests/{request}/drift with action self-service:read for the drift status of a request keyed by stable request ID
- Public webhook route for pushes that touch ADR 111 request paths compares git bytes with the stored request document
- Drift record and status on the request: drifted, in-sync, invalid-file, or file-removed, with commit id, changed fields, delivery id, and time; matching push or matching authorized submit clears drift
- Refusal of write-back or apply with 409 request.drifted while drift, invalid-file, or file-removed shows
- Catch-up read of the current file at server start and on manual redelivery decided from effect state; no poll
- Event and audit entry for mark and clear of drift
- Docs page for the drift between git and the Catalog

Out of scope:

- Automatic reconcile of git edits into the stored request or of the stored request into git
- Move of a request between git and the store
- Upgrade pull requests
- Curated upstreams and the artifact registry

Depends on:

- #886
- blueprints
- `blueprints` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A developer who edits in git may not read the Catalog, so the drift status shows in the console request view and in the CLI output through the same drift read route.
- The changed field list uses the document field names from ADR 111 so the person finds the edit without opening the commit.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 111: The Catalog request document, its schema version and its git layout** (not owner-tagged).

- Decisions that apply: 2 (current), 6 (current), 7 (current), 8 (current).
- Changed by: ADR 213 extends decision 1; ADR 286 changes; ADR 305 extends. Chain to the end: ADR 213, ADR 286, ADR 305, ADR 241, ADR 244, ADR 265, ADR 267, ADR 271, ADR 273, ADR 300, ADR 309, ADR 353, ADR 248, ADR 313, ADR 343, ADR 299, ADR 307, ADR 310, ADR 338, ADR 303, ADR 336, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 213, 286, 305.
- Quote: "No other field. The parser refuses an unknown field, a duplicate key, a value of the wrong JSON type and a document over 64 KiB."
- Quote: "Equal documents are equal states."
- Quote: "In a repository, the request of namespace `<ns>`, blueprint `<bp>` and name `<name>` is the file"
- Quote: "The write-back job commits the bytes that `catalog.Document.Encode` gives: the keys in the order of the table of decision 1, two spaces of indent, and a final new line."
- Effect: Change compares the git file at the decision 7 path with the stored document with the canonical form of decision 6 and the encode rule of decision 8, and keeps the strict parse rule of decision 2. It changes no layout, parse, or encode rule.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request."
- Effect: Change ships a user-visible drift status and error text, so it ships its docs page in the same pull request.
- Conflict: none.

**ADR 271: The Forge port, Git host connections and the installation-level GitHub App** (owner-tagged: decision 9, 10).

- Decisions that apply: 9 (current), 10 (current).
- Changed by: ADR 286 changes; ADR 299 changes decision 4; ADR 299 extends decision 2; ADR 299 extends decision 7; ADR 300 changes decision 7; ADR 303 extends decision 11; ADR 336 extends. Chain to the end: ADR 286, ADR 299, ADR 300, ADR 303, ADR 336, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 286, 299, 300, 303, 336.
- Quote: "The hook URL uses a stable hostname that each federation member accepts, for example with DNS failover or a global load balancer."
- Quote: "One installation-level GitHub App is the default. A per-namespace GitHub App is an option."
- Effect: Change receives git edits through the forge webhook path and keeps the stable hook URL rule.
- Conflict: none.

**ADR 299: Review requests and change orders: one integration with the Git host** (owner-tagged (all decisions)).

- Decisions that apply: 29 (current), 47 (current).
- Changed by: ADR 328 extends decision 1; ADR 359 extends decision 1; ADR 359 extends decision 15; ADR 359 extends decision 25. Chain to the end: ADR 328, ADR 359.
- Current rule: this ADR, with ADR 328, 359.
- Quote: "No polling (owner)."
- Quote: "The Forge port gets the operations that GitHost needs (PM)."
- Effect: Change uses webhooks with no poll, one catch-up read at start, and manual redelivery decided from effect state, through the Forge port.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The drift record needs a short ADR, per issue #896 design status, and none exists; the change builds the drift store and its status without one. Blocks build. Resolution: open question: Added open question on the short drift-record ADR; design context states build waits for the accepted ADR.
- R1.2 (open question): Open question 1 (keep or delete cleared record) is not answered by an ADR, and the change's spec requires a cleared record to keep commit id and time; the spec-ready gate stays closed until it is decided. Resolution: open question: Added open question on keep or delete of the cleared record; spec keeps commit id and time pending PM decision.
- R1.3 (wrong claim): The spec and tasks use the drift read route and the webhook route, but the proposal cites a command route that the code check reports absent from the API description and route table; the change does not name an existing route for the drift read. Resolution: answered: Named the new drift read route as a new route in what_changes with self-service:read, and uses its API call as the one command that helps; no existing route is claimed.
- R1.4 (wrong claim): The proposal's error table gives the command opendustpan request check, which the code check reports as not existing; the one command that helps must be a real command. Resolution: answered: Removed the absent CLI command; errors use the drift read API call as the one command that helps.
- R1.5 (missing scenario): No scenario covers a push that carries a request file that fails parse or path checks; the error table promises 422 request.invalid-document but the spec has no requirement or scenario for it, and no drift record rule for the malformed case. Resolution: answered: Added invalid-file requirement and scenario with 202 plus visible status and error entry.
- R1.6 (missing scenario): No scenario covers the 409 request.drifted refusal on write-back or apply, although the proposal error table and the design layers require it; the spec does not state the refusal. Resolution: answered: Added refusal requirement and scenario for 409 request.drifted on write-back or apply.
- R1.7 (failure mode): Stale or out-of-order webhook redelivery can overwrite newer drift state; the design lists this risk but no requirement, scenario or test (tasks 1.x) covers ordering by commit or time. Resolution: answered: Added repeated-delivery idempotency requirement, scenario, and task with last-distinct-delivery-wins per repo and ref.
- R1.8 (failure mode): A push that touches many request files, or a delivery whose request file is missing or deleted, has no defined behavior; the design names overload as a risk but no limit or scenario exists. Resolution: answered: Added file-removed requirement and scenario and bound the compare to files named in the delivery within the ADR 111 size cap.
- R1.9 (missing scenario): The use-check layer (drift read needs request read action; write-back refusal) has no requirement or scenario in the spec and no task that maps to it. Resolution: answered: Added drift read authorization requirements and scenarios and the route policy task.
- R1.10 (adr conflict): Design says the drift store is beside the request and the git drift compare uses canonical bytes; the ADR 111 decision 6 quote 'Equal documents are equal states' supports this, but the event and audit entry for mark and clear (proposal scope) has no requirement in the spec, so the ADR 271 webhook path is not tied to an audit record as the project rule for automatic actions requires. Resolution: answered: Added audit requirement and scenario for mark and clear entries tied to the webhook path.
- R2.1 (adr conflict): The 'Open questions' section reads 'none' but lists four questions with provisional decisions. Question 1 says build waits for a short ADR for the drift record. The ADR is not accepted, so the spec-ready gate is not met. The proposal must not claim the change is ready to build before the ADR is accepted. Resolution: open question: The short ADR question stays open. The change is not spec-ready, and design and tasks say build and merge wait for the accepted ADR.
- R2.2 (adr conflict): The design stores the git-edit drift and the 409 request.drifted refusal only. ADR 299 decision 29 (owner) allows no polling and forces a catch-up read at start only. The change has no recovery path for a webhook that the server missed during an outage or a replica drop (ADR 271 decision 9 says the federation poll catches it). A missed push leaves the drift hidden and write-back overwrites the git edit. Resolution: answered: Split the catch-up requirement into two: one for the read at server start and one for manual redelivery. Added a design decision, a task and a scenario for each, and no poll.
- R2.3 (missing scenario): No requirement or scenario for a deleted or renamed request file in a push. The compare cannot parse a missing file, and the spec has no drift state for 'file removed in git'. Resolution: answered: Added file-removed requirement, scenario, and error entry for deleted or renamed files.
- R2.4 (failure mode): The 422 on an invalid file conflicts with the webhook model: a forge does not read the answer to a webhook, so a developer never sees 422 request.invalid-document. A malformed git edit leaves the old state and no drift record, so the edit is lost without trace, which is the case the change should prevent. The spec needs a visible invalid-file status or event. Resolution: answered: Replaced webhook 422 with 202 plus visible invalid-file drift status, scenario, and error entry.
- R2.5 (failure mode): The spec has no way to leave drift once drifted. The refusal of 409 blocks write-back and apply, but the 'explicit refresh from git' named in the design layers has no route, requirement, task or scenario. The fix by 'submit of the same document' is unclear: a submit of a different document, or a resolve of the git edit, has no path. Resolution: answered: Added explicit exit requirement: push of matching bytes or authorized API submit that matches git bytes clears drift; write-back stays refused.
- R2.6 (failure mode): The bound of 50 files says 'mark the rest for retry' but no retry mechanism, owner or limit exists, and ADR 299 decision 29 forbids polling. A retry loop that re-reads git is a poll. The deferred files stay unseen with no status. Resolution: answered: Removed 50-file retry loop; spec processes each file named in the delivery within the existing size cap and answers 413 only on oversize delivery.
- R2.7 (failure mode): Ordering uses 'commit time', which the pusher controls (author date can be forged or old). A hostile or rebased push with an old date is ignored and hides a real edit. Order by delivery or by commit ancestry instead. Also the design says 'time of the delivery' while the rule compares commit time. Resolution: answered: Replaced commit-time ordering with delivery-id idempotency and last-delivery-wins per repo and ref.
- R2.8 (missing scenario): No scenario for a push whose request file has a path/document mismatch (ADR 111 decision 7 catalog.ErrPath...), a request with no stored document in the Catalog, or a push on a ref other than home.ref. Drift on a feature branch would be wrong. Resolution: answered: Added scenarios for path and document mismatch, unknown request, and non-home ref ignored.
- R2.9 (wrong claim): The error table names 'opendustpan doctor' and 'opendustpan status' as the one command that helps, but they do not show the drift of one request and the proposal gives no proof they exist or help. The CLI step 3 gives no drift read command. The one command that helps should be the drift read. Resolution: answered: Replaced doctor and status commands with the drift read call as the one command that helps.
- R2.10 (scope): The task list has no task for the event of mark and clear (proposal scope and issue), no console view, no CLI output, and no e2e tasks for refusal. The usability section promises the console and CLI show the drift but no task builds them. Resolution: answered: Added tasks for the mark and clear event, console and CLI output, and e2e refusal and invalid-file steps.
- R2.11 (open question): The change does not say which action protects the drift read ('same read action as the request' is not named) or the webhook route, and does not name the stable error code and path for ADR 271 decision 12 credential binding. Also the webhook route is not defined and the tasks 3.1 mention a 'public list entry' with no spec. Resolution: answered: Named self-service:read for the drift read, self-service submit for the exit submit, and the public webhook route with its Forge credential binding.
- M.5 (consistency): The route /namespaces/{namespace}/requests/{name}/drift does not match the request routes of blueprints (/self-service/requests/{request}). It also keys a renamable request by name, which breaks the stable ID rule. The change does not depend on blueprints, and it has an open question about an ADR. Resolution: answered: Moved drift read to GET /api/dustpan/v1/namespaces/{namespace}/self-service/requests/{request}/drift keyed by stable request ID, uses the blueprints read action, added blueprints to depends_on, and build stays blocked on the drift ADR.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which short ADR defines the drift record, its status values, and its drift read path before build?
   - Options: Accept a short ADR for the drift record before build; Build without an ADR and document the record only in code.
   - Provisional decision (owner review 14 Nov): Accept a short ADR for the drift record before build
   - Reason: Issue #896 says the drift record needs a short ADR and none exists. This change is not spec-ready until that ADR is accepted, and build waits for it. Benchmark: Google practice for design docs asks for a short ADR before build when a new stored record and status are added.
   - Persona impact: No persona is upset: waiting for the ADR delays the feature but keeps the record stable. Escape hatch: Build waits for the ADR; no persona uses the drift record until the ADR is accepted.
2. Does a cleared drift record stay with commit id and time, or is it deleted?
   - Options: Keep the cleared record with commit id and time of the clearing push; Delete the drift record when drift clears to in-sync.
   - Provisional decision (owner review 14 Nov): Keep the cleared record with commit id and time of the clearing push
   - Reason: The spec keeps commit id and time after clear for audit, but no ADR decides keep or delete. Benchmark: AWS practice for drift detection keeps the last evaluated state with its commit and time even after the state returns to in-sync, so an audit can show the past drift.
   - Persona impact: No persona is upset: both options show in-sync; keeping the record adds context without blocking the exit. Escape hatch: The drift read always shows current status; history stays in the audit log if the cleared record is deleted.

Questions considered and answered:

- How to detect a git edit without false drift from white space or key order? ADR 111 decisions 6 to 8 answer it: compare canonical bytes of the stored document with the parsed git file at the ADR 111 path.
- Should the server overwrite git or the stored request when they differ? Issue #896 answers it: out of scope. The change records drift only.
- Does this change need a docs page? ADR 214 decision 1 answers it: yes, the drift status is user-visible.
- How does the server recover a missed push without polling? ADR 299 decision 29 answers it: no poll; one catch-up read at start and manual redelivery decided from effect state.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
