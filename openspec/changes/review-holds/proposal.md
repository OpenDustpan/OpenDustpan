Roadmap: `docs/roadmap.md:726` (The review holds).

## Why

**Persona: developer.** Developer runs Terragrunt run-all for many workspaces and needs one reviewed change: no workspace applies before a person approves the change order.

The user path this change must make work:

1. **API.** Developer pushes Terragrunt run-all to a runner, the shim creates one change order with plan jobs and gated apply jobs
2. **Docs page.** Developer reviews the plan revision and sends the apply request from the docs page
3. **E2e.** Runner starts each apply job only after the apply request, each unit writes its own state version

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Apply job start before the apply request of a person | 403 with code change-order.apply-gated: change order is not approved for apply. Run the NEW command `opendustpan change-order apply-request --id <id>` after review. | `opendustpan change-order apply-request --id <id>` |

Evidence for the claims above:

- The pack lists no e2e file for an Apply job. Proof: Pack keywords Apply job: e2e [] with e2e_count 0
- The pack main list names no change-order route; it names only access-review and other routes. Proof: Pack main routes: ANY /api/server/access-review, ANY /api/server/namespaces/{}/access-review, ANY /api/server/namespaces/{}/collections/{}/access-review with no change-order entry

## What Changes

- Fan-out apply jobs stay gated until the apply request of a person on the change order.
- Apply job start before that request fails with 403 and the NEW error code `change-order.apply-gated` and no provider apply call.
- The NEW route `POST /api/dustpan/v1/change-orders/{id}/apply-request` stores the revision-bound apply request with approve action on the change order and an audit entry.
- The NEW command `opendustpan change-order apply-request --id <id>` sends the apply request.
- A new plan revision voids the stored apply request for pending starts; running jobs keep the old revision mark; a duplicate apply request returns the stored result.

Out of scope:

- Unit discovery
- Dependency order of apply jobs
- Post-start failure handling after one apply job fails
- Local run-all and run-all in team CI

Depends on:

- run-all-on-runner-fanout-plan-apply-jobs

## Usability

Where the path still costs the person time or surprises them:

- The person must find the pending apply request for the change order from one place.
- The blocked apply job must name the one command that sends the apply request.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main tests review of a collection access decision. (`pkg/access/review_collection_test.go:65`)
- Main tests that restore of a mutated target cannot apply. (`pkg/backup/restore_service_test.go:979`)
- Main defines request precondition error codes for gated writes. (`internal/errcode/catalog_request.go:42`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 304: Native run-all: fan-out applies in milestone 9** (owner-tagged (all decisions)).

- Decisions that apply: 2 (current), 5 (current).
- Changed by: ADR 310 changes decision 8. Chain to the end: ADR 310.
- Current rule: this ADR, with ADR 310.
- Quote: "A team can already change many units in one reviewed change (owner)."
- Quote: "The shim makes one job for each Terragrunt unit."
- Quote: "Each unit applies in its own apply job, with its own state version and its own history entry."
- Effect: The change keeps the change order review rules of decision 2 for fan-out applies. The apply request reads the stored approvals and signatures of decision 5 and blocks each apply job start until that approved state holds.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Can fan-out apply jobs start without a person review? No. ADR 304 decision 5 keeps approvals and signatures on the change order and keeps a person apply request before apply jobs start, so each apply job waits.
- Does the gate hold only for Terragrunt fan-out? No. The gate is the same job start check for every change order per ADR 304 decision 2. This change states the fan-out exit test but adds no Terragrunt-only bypass.
- Does this change decide what happens when one apply job fails after start? No. ADR 304 decision 10 leaves post-start failure handling to RFC 115. This change only blocks start before the approved state and defines running-job behavior at void time.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
