---
version: 3
---

Roadmap: `docs/roadmap.md:188` (Exchange issues a trust token for a matched workload.).

## Why

**Persona: runner operator.** A Runner operator runs CI jobs that need short-lived access with no static token. The jobs present a client certificate, exchange it for a trust token, and run the Pulumi CLI and OpenTofu with that token.

The user path this change must make work:

1. **API.** Make an enabled bundle and one enabled rule that names the workload SPIFFE ID and its principal (part 1 API).
2. **API.** Call POST /api/dustpan/v1/namespaces/{namespace}/trust-rules/exchange over direct TLS with the client certificate and read the trust token.
3. **CLI.** Run the Pulumi CLI or OpenTofu with the trust token until it expires or is revoked. A revoked token gives 401.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| No token when the chain, time, revocation or SPIFFE ID check fails. | 401 with NEW error codes mtls.no-client-certificate, mtls.unknown-issuer, mtls.expired, mtls.not-yet-valid, mtls.revoked, mtls.revocation-unknown, mtls.no-matching-rule, mtls.ambiguous-rule, mtls.rule-disabled, mtls.bundle-disabled and mtls.principal-disabled; 500 with NEW error code token.issue-failed and no token. | `curl --cert leaf.pem --key leaf.key https://server/api/dustpan/v1/namespaces/ns-xxx/trust-rules/exchange -v` |
| A revoked or disabled trust token gives no access. | 401 token.ended when the token was revoked or its principal was disabled; 401 token.invalid after expiry. | `curl -X POST https://server/api/dustpan/v1/namespaces/ns-xxx/trust-rules/exchange --cert leaf.pem --key leaf.key` to get a new token |

Evidence for the claims above:

- A workload exchange e2e test exists Proof: e2e/workload_test.go:38 TestWorkloadTokenExchange
- A trust token lifetime test exists Proof: pkg/accesstoken/trust_token_test.go:59 TestTrustTokenLivesTheExchangeLifetime
- The error code token.invalid exists Proof: internal/errcode/catalog_token.go:11

## What Changes

- The mTLS exchange verifies chain, time with no skew, bundle revocation and exact SPIFFE ID against one enabled rule over direct TLS.
- It issues a stored trust token with capped lifetime and an exchange audit entry in one transaction, with shared revoke and grant and limit enforcement.
- NEW error codes mtls.* and token.issue-failed. The new credential use for mTLS waits for the owner (open question 1).

Out of scope:

- Trust bundle and trust rule API (part 1).
- E2E proof with real CLIs and the docs page (part 3).
- JWT and OIDC exchange.
- New token life-cycle rules.

Depends on:

- mtls-trust-bundle-rules
- `mtls-trust-bundle-rules` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- A runner operator behind a TLS proxy gets a plain refusal that names direct TLS.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Trust token lifetime test (`pkg/accesstoken/trust_token_test.go:59`)
- Workload exchange e2e (`e2e/workload_test.go:38`)
- token.invalid code (`internal/errcode/catalog_token.go:11`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 54: Workload identity: OIDC and JWT token exchange, and mTLS** (not owner-tagged).

- Decisions that apply: 1 (current), 4 (current), 6 (current).
- Changed by: ADR 69 extends; ADR 70 changes decisions 5 and 8; ADR 204 changes decision 3; ADR 222 extends decision 5; ADR 223 extends decision 5; ADR 284 changes decision 3. Chain to the end: ADR 69, ADR 70, ADR 204, ADR 222, ADR 223, ADR 284, ADR 297, ADR 142, ADR 210, ADR 216, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 248, ADR 286, ADR 335, ADR 351, ADR 240, ADR 313, ADR 362, ADR 301, ADR 333, ADR 149, ADR 247, ADR 273, ADR 339, ADR 295, ADR 363.
- Current rule: this ADR, with ADR 69, 70, 204, 222, 223, 284.
- Quote: "Workload identity is in milestone 4. Two kinds are in scope: token exchange with OIDC and JWT issuers, and mTLS client certificates."
- Quote: "The principal is not a person. A matched workload acts as a principal that is not a person, like the owner of a service token (ADR 53)."
- Effect: The exchange follows the mTLS scope, bundle check, and non-person principal.
- Conflict: none.

**ADR 284: The token exchange issues a stored access token** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The exchange issues a stored access token. The exchange endpoint checks the JWT of a workload once, against the trust rules of the namespace."
- Effect: Decision 1 names the JWT exchange only. Issue of kind trust for mTLS needs an owner decision. This change does not proceed on it until then.
- Conflict: owner_blocks.

**ADR 69: Token lifetime and session defaults** (not owner-tagged).

- Decisions that apply: 6 (current).
- Changed by: ADR 297 changes decision 4. Chain to the end: ADR 297, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 297.
- Quote: "An exchanged workload token lives 1 hour. The token that a workload gets in an exchange (ADR 54) expires after 1 hour. It is a setting."
- Effect: The trust token uses the exchanged-token setting.
- Conflict: none.

**ADR 223: Access limits: Cedar forbid policies for each scope** (not owner-tagged).

- Decisions that apply: 6 (current).
- Changed by: ADR 240 changes decision 4; ADR 313 extends; ADR 362 changes decisions 4 and 11. Chain to the end: ADR 240, ADR 313, ADR 362, ADR 297, ADR 354, ADR 363, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 240, 313, 362.
- Quote: "A trust rule still decides whether the server issues a token. It never passes a limit."
- Effect: Access limits hold for the trust token.
- Conflict: none.

## Open questions

1. May the mTLS exchange issue stored kind trust, or does it need an owner decision or a new kind? Decides: owner.
   - Options: Extend kind trust to mTLS; Define a new stored kind for mTLS.
   - Recommended: Extend kind trust to mTLS Reason: ADR 284 decision 1 is owner-tagged and names the JWT only. One revoke path is simpler.

Not spec-ready: the change conflicts with an owner-tagged decision of ADR 284.

Questions considered and answered:

- Stored or stateless token? Stored, as ADR 284 decision 1 does for JWT; its use for mTLS is open question 1.
- Lifetime? ADR 69 decision 6: setting, default 1 hour, capped at the leaf not-after time.
- Behind a TLS proxy? Direct TLS only here; a forwarded chain header is ignored. Proxy design is in part 3.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
