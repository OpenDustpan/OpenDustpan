---
version: 3
---

## Why

**Persona: operator.** The operator runs installations in two zones and must move a namespace to another installation for cost or network reasons. Main has no named starter, no route, no audit record and no rollback for a move. The operator cannot plan the write block or prove who moved the namespace.

The user path this change must make work:

1. **API.** The operator starts a move with the NEW route POST /api/dustpan/v1/namespaces/{namespace}/moves, with an access token that holds the NEW action installation:move-namespace and a fresh authentication.
2. **API.** The operator reads the move state with the NEW route GET /api/dustpan/v1/namespaces/{namespace}/moves/{move} and lists moves with the NEW route GET /api/dustpan/v1/namespaces/{namespace}/moves.
3. **CLI.** The CLI is generated from the API description. The operator runs the move with the generated command for the NEW operations.
4. **API.** The operator rolls back with the NEW route POST /api/dustpan/v1/namespaces/{namespace}/moves/{move}/rollback when the state is not completed.
5. **Docs page.** The operator reads one docs page for the move: the target, the write block, the name conflicts and the rollback.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The target holds a collection of the same name with another stable ID | 409 with the NEW error code `collection.name-conflict`; the answer names the collection | `GET /api/dustpan/v1/namespaces/{namespace}/moves/{move}` (NEW route) |
| A write reaches the source while a move is in state started or copying | 423 with the NEW error code `namespace.move-in-progress`; the answer holds Retry-After | `GET /api/dustpan/v1/namespaces/{namespace}/moves/{move}` (NEW route) |
| A second move starts while one move is live | 409 with the NEW error code `namespace.move-conflict` | `GET /api/dustpan/v1/namespaces/{namespace}/moves` (NEW route) |
| The target is not in the allowlist of installations | 400 with the NEW error code `namespace.move-target-unknown`; the answer repeats nothing of the input | `GET /api/dustpan/v1/settings` |
| The caller has no fresh authentication | 403 with the error code `mfa.fresh-step-required` | `POST /api/dustpan/v1/namespaces/{namespace}/moves` (NEW route) |

Evidence for the claims above:

- Main has collection routes under the namespace path Proof: route GET/POST /api/dustpan/v1/namespaces/{}/collections in the route table of the pack
- Main has placement rule routes under the namespace path Proof: route GET/POST /api/dustpan/v1/namespaces/{}/placement-rules in the route table of the pack
- Main has the namespace domain package Proof: pkg/namespace
- RFC 37 is a draft that decides nothing; part 1 of this split is the placement map and forwarding Proof: docs/rfc/0037-federation.md

## What Changes

- The NEW routes POST and GET /api/dustpan/v1/namespaces/{namespace}/moves, GET /api/dustpan/v1/namespaces/{namespace}/moves/{move} and POST /api/dustpan/v1/namespaces/{namespace}/moves/{move}/rollback, with the NEW action installation:move-namespace.
- The NEW audit entries namespace.move-start, namespace.move-complete, namespace.move-rollback and namespace.move-fail.
- The move is a NEW durable operation on the source primary. The write block on the source has a maximum of 30 minutes. At the limit the move fails and rolls back.
- Handoff is target-first: the target commits the import with its failover version set to the source version plus one and becomes primary, then the source marks itself demoted with the same version. A crash between the two steps resumes on the source from the stored move record, and the target never accepts a write before its commit.
- The move carries the default collection and the catch-all placement rule by stable ID when they exist. A namespace with no default collection or no catch-all rule moves with none. A rule that names a collection absent from the source is refused before the copy.
- A name conflict is found by a NEW check call from the source to the target peer API with the stored credential of the peer from part 1.

Out of scope:

- The placement map and the request forwarding (part 1, 37-namespace-placement-routing-1)
- Asynchronous replication of namespaces
- Terraform CLI support

Depends on:

- 37-namespace-placement-routing-1
- `37-namespace-placement-routing-1` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- The operator finds the peer list, the name-conflict fix and the rollback command on one docs page.
- The move status shows the copied bytes, the state and the one rollback command.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main reads placement rules in the inventory handler (`internal/api/inventory/placement.go:302 (func placementRuleOf)`)
- Main checks that a fence belongs to the same namespace (`pkg/gate/access.go:449 (func sameNamespaceFence)`)
- Main reads the failover version in the restore worker test rig (`pkg/store/storetest/restore_worker_cases.go:534 (func workerRig.mustRestoredOnce)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P5)**, Batch 1: default collection at namespace create: "B. A new namespace gets a stored, visible, audited `default` collection and a catch-all placement rule in the create transaction; an admin can delete either; the create request can opt out. Reason: works first time from the docs (Terraform Cloud "Default Project"); "no match, no unit" (ADR 51 d5, ADR 61 d3) still holds because the rule is explicit and stored."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 4: Namespaces, home regions and failover-version fencing** (not owner-tagged).

- Decisions that apply: the ADR as a whole.
- Changed by: ADR 38 extends; ADR 248 changes; ADR 286 changes. Chain to the end: ADR 38, ADR 248, ADR 286, ADR 42, ADR 48, ADR 70, ADR 151, ADR 210, ADR 212, ADR 253, ADR 72, ADR 142, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 369, ADR 154, ADR 217, ADR 220, ADR 247, ADR 232, ADR 235, ADR 301, ADR 365, ADR 73, ADR 75, ADR 103, ADR 126, ADR 166, ADR 240, ADR 249, ADR 280, ADR 298, ADR 149, ADR 297.
- Current rule: this ADR, with ADR 38, 248, 286.
- Quote: "Every namespace carries a `failover_version` that increases on each
  promotion."
- Effect: The move keeps one primary for the namespace and keeps the fence on every write.
- Conflict: none.

**ADR 248: Federation model: installations own namespaces, request forwarding, asynchronous replication and manual failover** (not owner-tagged).

- Decisions that apply: 6 (current), 14 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "The fence stays. Each write in a namespace carries a `namespace.Fence`, and the store checks it in the same transaction, as ADR 4 decides."
- Quote: "Background work runs on the primary installation."
- Effect: The handoff sets the failover version one above the source version, and background work runs on the new primary only.
- Conflict: none.

**ADR 72: Scale out on one database with durable operations** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 73 changes decision 7; ADR 75 extends decision 10; ADR 103 changes decision 6; ADR 126 changes decision 6; ADR 151 extends decision 5; ADR 166 extends decision 1; ADR 240 extends decision 6; ADR 249 extends decision 11; ADR 280 extends; ADR 298 extends. Chain to the end: ADR 73, ADR 75, ADR 103, ADR 126, ADR 151, ADR 166, ADR 240, ADR 249, ADR 280, ADR 298, ADR 322, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 146, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 129, ADR 154, ADR 210, ADR 217, ADR 220, ADR 286, ADR 297, ADR 354, ADR 281, ADR 302, ADR 337, ADR 325, ADR 326, ADR 362.
- Current rule: this ADR, with ADR 73, 75, 103, 126, 151, 166, 240, 249, 280, 298.
- Quote: "Each claim has a fencing token that increases on each takeover."
- Effect: The move runs as a durable operation with a lease and a fencing token.
- Conflict: none.

**ADR 227: One fresh-authentication window** (owner-tagged: decision 2).

- Decisions that apply: 2 (current).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends. Chain to the end: ADR 301, ADR 320, ADR 339, ADR 354, ADR 332.
- Current rule: this ADR, with ADR 301, 320, 339, 354.
- Quote: "The installation has one setting, `freshAuthSeconds`, with the default 900 (15 minutes)."
- Effect: Start and rollback of a move use the one fresh authentication window.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does a move start need a fresh authentication? Yes. ADR 227 decision 2 (owner) sets one window for each step named fresh. A start or rollback with no fresh step answers mfa.fresh-step-required.
- Can two primaries exist? ADR 248 decision 6 keeps the fence. The target commits first with version plus one; the source then demotes. The source refuses writes in all states from started on.
- What if the default collection or catch-all rule is deleted? Owner decision, 13 Nov 2026 (queue P5): an admin can delete either. The move carries what exists and refuses only a rule that names a missing collection.
- Which version is bumped? The version of the namespace is one number. The target sets source version plus one at its commit. The source records the same number when it demotes.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
