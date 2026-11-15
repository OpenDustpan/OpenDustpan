---
version: 6
---

## Why

**Persona: site administrator.** A site administrator needs to let one named person write state when a gate is in error, with a record of who did it and why. The pkg/gate decision layer holds the pass logic. The pack shows no store, no audit writer, no grant check point, no route field and no proof kind for it. No ADR in the pack decides these shapes, so each one is an open question for the owner.

The user path this change must make work:

1. **API.** The administrator proves fresh authentication and asks for a break-glass pass for one unit with a reason. The server returns a pass that works once, for 15 minutes.
2. **API.** The developer sends the pass on the Pulumi update-create request or on the TFE state-version request.
3. **API.** The administrator reads the audit entry of the issue and of the use of the pass.
4. **Docs page.** A docs page explains the pass, the 15-minute limit, the single use and the audit entry.
5. **E2e.** BLOCKED by the owner decision on the route field and the proof kind: the e2e step against the real Pulumi CLI and OpenTofu cannot name its route field yet.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The pass is expired, used, unknown or for another unit. | The server answers 403 with an error code that says the pass is not valid. It writes no state and writes an audit entry. | `GET /api/audit/entries` |
| The caller holds no grant to issue a pass. | The server answers 403 and names the missing action. | `GET /api/server/caller/grants` |

Evidence for the claims above:

- The package pkg/gate exists. Proof: The pack lists the path pkg/gate with exists true.
- Main has a TFE state write handler and Pulumi update-create routes. Proof: internal/api/tfe/state_write.go:253 (Handler.writeStateVersionCreate) and internal/api/pulumi/routes.go:310 (Handler.updateCreateRoutes).
- Main has an audit placement function and a grant check. Proof: pkg/store/postgres/audit.go:283 (placeAuditEntry) and pkg/access/check.go:139 (Service.grantCheck).
- No ADR decides the store, the route field or the proof kind. Proof: The pack lists no ADR for this item, and the roadmap step says no ADR decides these shapes.

## What Changes

- After the owner decides: add the store of the pass and its baseline place.
- After the owner decides: write the audit entry of the issue and of the use of a pass.
- After the owner decides: check a grant where a pass is issued.
- After the owner decides: add the route field on the Pulumi update-create route and the TFE state-version routes.
- After the owner decides: add the fresh-authentication proof kind.

Out of scope:

- The gate decision layer itself, which exists in pkg/gate.
- Changing the words of any owner-tagged decision.

Depends on:

- break-glass-gates-in-error
- `break-glass-gates-in-error` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- The administrator needs one fresh proof and one request.
- Each error names the one command that helps.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The TFE state write handler exists. (`internal/api/tfe/state_write.go:253 (Handler.writeStateVersionCreate)`)
- The Pulumi update-create routes exist. (`internal/api/pulumi/routes.go:310 (Handler.updateCreateRoutes)`)
- The grant check exists. (`pkg/access/check.go:139 (Service.grantCheck)`)
- The audit placement function exists. (`pkg/store/postgres/audit.go:283 (placeAuditEntry)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Which store place, route field and proof kind does the break-glass pass use? Owner ADR 373 decision 3 decides it: "A pass is a row of a new table, written with its audit entry in one transaction. A request header carries the ID of the pass on the routes that take one. A person gets a pass only after a fresh authentication of ADR 227 decision 1. A pass is for one use, and it ends after 15 minutes." The header is X-Dustpan-Break-Glass-Pass (PM decision, rule 16, a name only).

- Where do single use and expiry get checked? In the store transaction, so that a race cannot use a pass twice. This holds for any store the owner chooses.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
