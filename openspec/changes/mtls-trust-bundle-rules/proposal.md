---
version: 4
---

Roadmap: `docs/roadmap.md:188` (Bundle and rule API with audit ships first.).

## Why

**Persona: namespace administrator.** As a namespace administrator I share an installation with CI pipelines and I need workload identity for machines so each pipeline gets only the access its principal holds, with every bundle and rule change on record.

The user path this change must make work:

1. **API.** Administrator saves the namespace trust bundle
2. **API.** Administrator rotates the bundle and reads the version list and the overlap end
3. **API.** Administrator creates a trust rule that names the bundle, the SPIFFE ID and the principal
4. **API.** Administrator updates a rule with PATCH and If-Match, then deletes a rule the team no longer needs
5. **API.** Administrator reads the audit entries for each change
6. **Docs page.** Administrator follows the same steps on the docs page

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Caller without grant changes a bundle or a rule | Server answers 403 access.denied. | `GET /api/dustpan/v1/namespaces/{namespace}/trust-rules` |
| Bundle or rule change with a stale session or with an access token only | Server answers 403 and states that a fresh authentication is needed. | `GET /api/dustpan/v1/me/preferences` |
| Bundle save with malformed PEM, expired CA, oversize bundle or too many CAs | Server answers 400 with the CA index and the count, and stores nothing. | `GET /api/dustpan/v1/namespaces/{namespace}/trust-rules` |

Evidence for the claims above:

- Trust rule routes exist on main with weak docs, console and audit cells Proof: Coverage matrix: GET, POST, DELETE /api/dustpan/v1/namespaces/{namespace}/trust-rules and /{rule_id}, and POST .../trust-rules/exchange.
- The roadmap puts the bundle store and rule API in part 1 and leaves the exchange and the proof to later parts Proof: Roadmap text: This part specifies the namespace trust bundle store and the trust rule REST API and actions with audit entries. It leaves the mTLS exchange and trust token and the E2E proof and docs to the later parts.

## What Changes

- Versioned namespace trust bundle in pkg/trustrule with save checks, bounds, rotation and retired keeps
- SPIFFE and mTLS fields on the existing trust rule in pkg/trustrule
- Bundle save, rotate, rule update with If-Match and the fresh-authentication check in internal/trustrulesvc
- Bundle routes and rule update route in internal/api/trustrules
- Schema change in the baseline of each dialect, in name order

Out of scope:

- mTLS exchange endpoint and trust token issue, with the per-exchange re-check of own-installation issuers
- E2E proof with real CLIs and user docs for mTLS
- Token life cycle expiry email and warning display

## Usability

Where the path still costs the person time or surprises them:

- Bundle errors name the CA index, not a TLS trace.
- A rule read shows the bundle version it was saved with and the active version.
- A write with an access token only says that a fresh session step is needed.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has a workload token exchange test that this part does not replace (`e2e/workload_test.go:38 (func TestWorkloadTokenExchange)`)
- Main has a test that the trust token lives the exchange lifetime (`pkg/accesstoken/trust_token_test.go:59 (func TestTrustTokenLivesTheExchangeLifetime)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 54: Workload identity: OIDC and JWT token exchange, and mTLS** (not owner-tagged).

- Decisions that apply: 4 (current), 8 (changed).
- Changed by: ADR 69 extends; ADR 70 changes decisions 5 and 8; ADR 204 changes decision 3; ADR 222 extends decision 5; ADR 223 extends decision 5; ADR 284 changes decision 3; ADR 369 changes decision 6. Chain to the end: ADR 69, ADR 70, ADR 204, ADR 222, ADR 223, ADR 284, ADR 369, ADR 297, ADR 142, ADR 210, ADR 216, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 248, ADR 286, ADR 335, ADR 351, ADR 240, ADR 313, ADR 362, ADR 370, ADR 301, ADR 333, ADR 149, ADR 247, ADR 273, ADR 339, ADR 295, ADR 363.
- Current rule: this ADR, with ADR 69, 70, 204, 222, 223, 284, 369.
- Quote: "mTLS client certificates. A workload can prove its identity with a TLS client certificate. The server checks the certificate against a trust bundle that an administrator configures."
- Quote: "Audit and API. Each exchange and each trust rule change writes an audit entry. Each trust rule operation has a REST API and an action (ADR 17)."
- Effect: This part ships the bundle store and the rule fields, with a REST API, actions and audit. It does no exchange.
- Conflict: none.

**ADR 284: The token exchange issues a stored access token** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The trust rules refuse the issuers of this installation. A rule cannot name an issuer at or under a URL of an issuer of this installation (ADR 204 decision 7)."
- Effect: The save check refuses a JWT issuer URL of this installation. The exchange-time re-check is out of scope.
- Conflict: none.

**ADR 216: Namespace administration and operator scope: where each installation action goes (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: ADR 273 changes decision 8. Chain to the end: ADR 273, ADR 286.
- Current rule: this ADR, with ADR 273.
- Quote: "The map of each action. Operator actions keep the area `installation:`. A namespace action takes the area `namespace:` and has the namespace as its object."
- Effect: Bundle and rule objects live under the namespace path and use namespace actions.
- Conflict: none.

**ADR 227: One fresh-authentication window** (owner-tagged: decision 2).

- Decisions that apply: 2 (current).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends. Chain to the end: ADR 301, ADR 320, ADR 339, ADR 354, ADR 332.
- Current rule: this ADR, with ADR 301, 320, 339, 354.
- Quote: "One window everywhere (owner). The installation has one setting, `freshAuthSeconds`, with the default 900 (15 minutes)."
- Effect: Bundle and rule writes need a fresh authentication inside the one window.
- Conflict: none.

**ADR 70: Act-as reach, identity provider groups and break-glass accounts** (not owner-tagged).

- Decisions that apply: 3 (current).
- Changed by: ADR 142 changes decision 26; ADR 142 extends decision 13; ADR 210 changes; ADR 216 changes decisions 11, 23, 24 and 28; ADR 222 extends decisions 16, 17 and 23; ADR 227 extends decisions 16, 17 and 23; ADR 245 changes decisions 26 and 27; ADR 306 changes decisions 24, 25 and 26; ADR 306 extends decision 21; ADR 320 extends decision 12; ADR 321 extends decision 19; ADR 332 extends decisions 12, 14, 16 and 22; ADR 354 changes decision 13; ADR 369 changes decision 9. Chain to the end: ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 369, ADR 149, ADR 297, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 295, ADR 370, ADR 333.
- Current rule: this ADR, with ADR 142, 210, 216, 222, 227, 245, 306, 320, 321, 332, 354, 369.
- Quote: "A grant of an act-as right needs the reach of its target."
- Effect: A rule that names a principal with token rights needs the granter reach rule.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Where do the bundle and each rule live? In one namespace (ADR 54 decisions 4 and 5, ADR 216 decision 3).
- Does each operation need a REST API, an action and an audit entry? Yes (ADR 54 decision 8).
- Does a change need fresh authentication? Yes (ADR 227 decision 2, ADR 297 decision 18).
- Do we add a parallel package? No. The PM decision extends the existing trust rule domain so that one writer serves each route.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
