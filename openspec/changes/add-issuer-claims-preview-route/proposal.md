---
version: 1
---

Issue: #2162.

## Problem

A developer who wires a cloud trust rule needs to see the subject and claims a profile block would mint, without minting a token, so the rule matches on the first try.

## Evidence

- Issuer reference page exists on main. Proof: docs/reference/issuer-api.md
- Issuer HTTP adapter exists on main. Proof: internal/api/issuer/routes.go
- The issue states that the routes file and the reference page have no claims-preview. Proof: Issue text of #2162: internal/api/issuer/routes.go and the reference page have no claims-preview.

## Fix boundary

- Add the NEW route POST /api/dustpan/v1/namespaces/{namespace}/issuer/claims-preview with profile:read that answers claims and subject and signs nothing.
- Document the NEW route in docs/reference/issuer-api.md.

Out of scope:

- Issuer keys, rotation, export, settings and audit
- Signed token issue and login adapters
- Console subject line above the block editor

## Check

- Write NEW internal/api/issuer/claims_preview_test.go with failing handler test for claims and subject. Check: TestPreviewClaimsAnswersSubject fails on main.
- Write NEW internal/api/issuer/claims_preview_test.go with failing test that preview calls no signer and no adapter. Check: TestPreviewSignsNothing fails on main.
- Write NEW internal/api/issuer/claims_preview_test.go with failing refusal test for caller without profile:read. Check: TestPreviewNeedsProfileRead fails on main.
- Edit internal/api/issuer/routes.go to add NEW POST .../issuer/claims-preview with profile:read, called with an access token. Check: POST .../issuer/claims-preview answers claims and subject.
- Edit internal/api/route to declare the authorization action and pass the route and policy tests. Check: Route test in internal/api/route passes.
- Edit NEW internal/api/issuer/claims_preview_test.go to add parity test of preview and signed claims. Check: Table test in NEW internal/api/issuer/claims_preview_test.go passes.
- Edit docs/reference/issuer-api.md to document the NEW route and regenerate the API description. Confirmed by: CI (`make docs-lint`). Check: make docs-lint passes.
- Run usability check for preview from docs/reference/issuer-api.md. A person's step. Check: Persona completes preview from docs page alone.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The OIDC issuer fake exists for e2e tests. (`e2e/issuer.go:41 (func NewOIDCIssuerFake)`)
- The issuer HTTP adapter package exists on main. (`internal/api/issuer/routes.go:1 (package issuer)`)
- The issuer reference page exists on main. (`docs/reference/issuer-api.md:1 (file issuer-api)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 204: OpenDustpan as an OIDC issuer** (not owner-tagged).

- Decisions that apply: 12 (current).
- Changed by: ADR 248 changes decision 4; ADR 286 changes. Chain to the end: ADR 248, ADR 286.
- Current rule: this ADR, with ADR 248, 286.
- Quote: "A preview of the claims. `POST .../issuer/claims-preview` takes a profile, a block and a context: a unit, an action, a phase and a ref. It answers the claims and the subject that a token would carry. It signs nothing, mints no token and calls no adapter. It needs `profile:read` on the profile."
- Effect: Adds the route of decision 12 with profile:read and no signing.
- Conflict: none.

**ADR 205: The claims of an issued token, and the subject** (not owner-tagged).

- Decisions that apply: 1 (current), 6 (extended).
- Changed by: ADR 263 changes decision 4; ADR 286 changes; ADR 354 extends decisions 3 and 6. Chain to the end: ADR 263, ADR 286, ADR 354, ADR 358, ADR 366.
- Current rule: this ADR, with ADR 263, 286, 354.
- Quote: "A claim whose fact the server does not know is absent."
- Quote: "The subject (one-way door). `sub` is a list of `key:value` pairs, joined with `:`, in the fixed order of this list:"
- Effect: Preview answers the same claim and subject values that a signed token would carry, with unknown facts absent.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- What route answers the preview and what auth does it need? ADR 204 decision 12 names the route, its inputs and profile:read; ADR 205 names the claims and subject.
- Does the preview sign or call an adapter? ADR 204 decision 12 says it signs nothing, mints no token and calls no adapter; a test proves it.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
