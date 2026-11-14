---
version: 2
---

Issue: #896.

## Why

**Persona: developer.** Developer requests a unit from a blueprint and edits code in git. When the developer edits the request file directly in git, the Catalog must show the request as drifted from git with the commit and changed fields, instead of losing the change or overwriting it.

The user path this change must make work:

1. **API.** Push a commit that edits catalog/<ns>/<bp>/<name>/request.json in git; the Git host sends a push webhook to the public webhook route
2. **Console.** Read the drift status of the request by stable request ID; see drifted, invalid-file, or file-removed with commit and changed fields
3. **CLI.** Fix by a push of a matching file; read the drift again and see in-sync
4. **Docs page.** Follow the docs page for git edits of a request file to complete each step without special knowledge

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Write-back starts while the request shows drift between git and the Catalog. | Server refuses with 409 code request.drifted and names the request, the commit, and the changed fields. Read the drift, then push a matching file. | `GET /api/dustpan/v1/namespaces/{namespace}/self-service/requests/{request}/drift` |
| Push carries a request file that fails parse, for example an unknown field or bad JSON type. | Drift read shows status invalid-file with the field error. Fix the file text and push again. | `GET /api/dustpan/v1/namespaces/{namespace}/self-service/requests/{request}/drift` |
| Push deletes or renames the request file in git. | Drift read shows status file-removed with the commit id. Restore the file by push or delete the request through the API. | `GET /api/dustpan/v1/namespaces/{namespace}/self-service/requests/{request}/drift` |

Evidence for the claims above:

- Issue #896 scopes the change to a webhook handler for pushes that touch request paths, a drift record and status, and an event, with no automatic reconcile. Proof: Scope - In: webhook handler for pushes that touch request paths (ADR 111 layout); compare with the stored request document; a drift record and status on the request; an event. - Out: automatic reconcile.
- Issue #896 sets the two acceptance rules for mark and clear of drift. Proof: A push that edits a request file marks the request as drifted, with the commit and the changed fields. - A push that matches the stored document clears the drift.
- ADR 111 fixes the git path of each request file used by the compare. Proof: The git layout. In a repository, the request of namespace `<ns>`, blueprint `<bp>` and name `<name>` is the file
- Issue #896 records the owner decision to record git edits as drift and no move between git and the store in v1. Proof: 14.7 (owner, 2026-10-18): record git edits of a request file (seen through a forge webhook) as drift between git and the Catalog. - 14.4: no move of a request between git and the store in v1.
- Issue #896 says the drift record needs a short ADR. Proof: ADR 111 gives the layout; the drift record needs a short ADR.

## What Changes

- New route GET /api/dustpan/v1/namespaces/{namespace}/self-service/requests/{request}/drift with action self-service:read for the drift status of a request keyed by stable request ID
- Public webhook route for pushes that touch ADR 111 request paths compares git bytes with the stored request document
- Drift record and status on the request: drifted, in-sync, invalid-file, or file-removed, with commit id, changed fields, delivery id, and time; matching push clears drift
- Refusal of write-back with 409 request.drifted while drift, invalid-file, or file-removed shows
- Catch-up read of the current file at server start and on forge redelivery of the same delivery id; no poll
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

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Error code drift.start-conflict exists for drift conflicts (`internal/errcode/catalog_drift.go:8`)
- Catalog request error codes exist for invalid request bodies (`internal/errcode/catalog_request.go:7`)
- Forge service test covers audit of check outcome (`pkg/forge/service_test.go:501`)
- API description test covers drift (`pkg/dustpan/apidesc_test.go:87`)

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
- Effect: Change compares the git file at the decision 7 path with the stored document using the canonical form of decision 6 and the strict parse of decision 2. It changes no layout, parse, or encode rule.
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
- Effect: Change uses webhooks with no poll, one catch-up read at start, and forge redelivery, through the Forge port.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- How to detect a git edit without false drift from white space or key order? ADR 111 decisions 6 to 8 answer it: compare canonical bytes of the stored document with the parsed git file at the ADR 111 path.
- Should the server overwrite git or the stored request when they differ? Issue #896 answers it: out of scope. The change records drift only.
- Does this change need a docs page? ADR 214 decision 1 answers it: yes, the drift status is user-visible.
- How does the server recover a missed push without polling? ADR 299 decision 29 answers it: no poll; one catch-up read at start and forge redelivery.
- Does a cleared drift record stay with commit id and time? Provisional PM decision: the record stays with the commit id and time of the clearing push, as AWS drift detection keeps the last evaluation. The short drift-record ADR writes it down. No persona is upset, and the audit log holds the history.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
