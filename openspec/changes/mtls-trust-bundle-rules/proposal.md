---
version: 2
---

Roadmap: `docs/roadmap.md:188` (Bundle and rule API with audit ships first.).

## Why

**Persona: namespace administrator.** As a namespace administrator I share an installation with CI pipelines and I need workload identity for machines so each pipeline gets only the access its principal holds, with every bundle and rule change on record.

The user path this change must make work:

1. **API.** Administrator saves the namespace trust bundle from the docs page
2. **API.** Administrator rotates the bundle and checks the version list and overlap end
3. **API.** Administrator creates a trust rule that names the bundle, the SPIFFE ID, and the principal
4. **API.** Administrator updates a rule with PATCH and If-Match, then deletes a rule the team no longer needs
5. **API.** Administrator reads the audit entries for the change
6. **Docs page.** Administrator follows the same steps on the docs page

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Caller without grant creates a rule | Server answers 403 access.denied and names the access check call. | `GET /api/dustpan/v1/namespaces/{namespace}/trust-rules` |
| Rule or bundle change with stale session or with access token only | Server answers 403 fresh-authentication-required and names the session check call. | `GET /api/dustpan/v1/me/preferences` |
| Bundle save with malformed PEM, expired CA, oversize bundle, or too many CAs | Server answers 400 trust-bundle.invalid with the CA index and the count, and stores nothing. | `GET /api/dustpan/v1/namespaces/{namespace}/trust-rules/{rule_id}` |

Evidence for the claims above:

- Trust-rule routes exist in the coverage matrix with weak docs, console, refusal, and audit cells. Proof: GET /api/dustpan/v1/namespaces/{namespace}/trust-rules, GET /api/dustpan/v1/namespaces/{namespace}/trust-rules/{rule_id}, POST /api/dustpan/v1/namespaces/{namespace}/trust-rules, DELETE /api/dustpan/v1/namespaces/{namespace}/trust-rules/{rule_id}
- Roadmap splits mTLS into 3 parts and this part covers bundle store plus rule API with audit, not the exchange. Proof: This part specifies the namespace trust bundle store and the trust rule REST API and actions with audit entries. It leaves the mTLS exchange and trust token and the E2E proof and docs to the later parts.

## What Changes

- Versioned namespace trust bundle store with save checks, bounds, rotation, and retired keeps
- Trust rule REST API with namespace actions, fence, ETag update, fresh session auth, reach check, and audit entries

Out of scope:

- mTLS exchange endpoint and trust token issue, with the per-exchange re-check of own-installation issuers
- E2E proof with real CLIs and user docs for mTLS
- Token life cycle expiry email and warning display

## Usability

Where the path still costs the person time or surprises them:

- Bundle errors name the CA index and the list call, not a TLS trace.
- Rule read shows version seen at save plus active version.
- Access-token-only writes state plainly that a fresh session step is needed.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has a workload token exchange test that this part does not replace (`e2e/workload_test.go:38`)
- Main has a dev TLS bundle test for server certs, not the namespace trust bundle (`cmd/opendustpan/devtls_test.go:270`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 54: Workload identity: OIDC and JWT token exchange, and mTLS** (not owner-tagged).

- Decisions that apply: 4 (current), 5 (changed), 8 (changed).
- Changed by: ADR 69 extends; ADR 70 changes decisions 5 and 8; ADR 204 changes decision 3; ADR 222 extends decision 5; ADR 223 extends decision 5; ADR 284 changes decision 3. Chain to the end: ADR 69, ADR 70, ADR 204, ADR 222, ADR 223, ADR 284, ADR 297, ADR 142, ADR 210, ADR 216, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 248, ADR 286, ADR 335, ADR 351, ADR 240, ADR 313, ADR 362, ADR 301, ADR 333, ADR 149, ADR 247, ADR 273, ADR 339, ADR 295, ADR 363.
- Current rule: this ADR, with ADR 69, 70, 204, 222, 223, 284.
- Quote: "mTLS client certificates. A workload can prove its identity with a TLS client certificate. The server checks the certificate against a trust bundle that an administrator configures."
- Quote: "Trust rules per namespace. A trust rule belongs to one namespace. It names the issuer or the trust bundle, conditions on the claims or on the certificate fields, and the principal that a match gets."
- Quote: "Audit and API. Each exchange and each trust rule change writes an audit entry. Each trust rule operation has a REST API and an action (ADR 17)."
- Effect: This part ships the namespace bundle store and the rule API. It does no exchange.
- Conflict: none.

**ADR 284: The token exchange issues a stored access token** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The exchange issues a stored access token. The exchange endpoint checks the JWT of a workload once, against the trust rules of the namespace."
- Quote: "The trust rules refuse the issuers of this installation. A rule cannot name an issuer at or under a URL of an issuer of this installation (ADR 204 decision 7)."
- Effect: The save check refuses a JWT issuer URL of this installation; the exchange-time re-check is out of scope.
- Conflict: none.

**ADR 216: Namespace administration and operator scope: where each installation action goes (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current), 14 (current).
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
- Effect: Bundle and rule writes require a fresh authentication inside the one window.
- Conflict: none.

**ADR 70: Act-as reach, identity provider groups and break-glass accounts** (not owner-tagged).

- Decisions that apply: 3 (current).
- Changed by: ADR 142 changes decision 26; ADR 142 extends decision 13; ADR 210 changes; ADR 216 changes decisions 11, 23, 24 and 28; ADR 222 extends decisions 16, 17 and 23; ADR 227 extends decisions 16, 17 and 23; ADR 245 changes decisions 26 and 27; ADR 306 changes decisions 24, 25 and 26; ADR 306 extends decision 21; ADR 320 extends decision 12; ADR 321 extends decision 19; ADR 332 extends decisions 12, 14, 16 and 22; ADR 354 changes decision 13. Chain to the end: ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 149, ADR 297, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 295, ADR 333.
- Current rule: this ADR, with ADR 142, 210, 216, 222, 227, 245, 306, 320, 321, 332, 354.
- Quote: "A grant of an act-as right needs the reach of its target."
- Effect: A rule that names a principal with token rights needs the granter reach rule.
- Conflict: none.

**ADR 297: Console sessions are sealed cookies, not stored sessions** (owner-tagged (all decisions)).

- Decisions that apply: 18 (current).
- Changed by: ADR 301 extends decision 1; ADR 333 extends decision 4. Chain to the end: ADR 301, ADR 333.
- Current rule: this ADR, with ADR 301, 333.
- Quote: "An access token never gives a fresh authentication (ADR 227 decision 6)."
- Effect: Writes need a session with a fresh step.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Where does the bundle and each rule live? In one namespace (ADR 54 decisions 4 and 5, ADR 216).
- Does each rule operation need a REST API, an action and an audit entry? Yes (ADR 54 decision 8).
- Does a change need fresh authentication? Yes (ADR 227 decision 2, ADR 297 decision 18).

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
