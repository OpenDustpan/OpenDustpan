---
version: 5
---

Issue: #3566.

Roadmap: `docs/roadmap.md:188` (Exchange issues a trust token for a matched workload.).

## Why

**Persona: runner operator.** A Runner operator runs CI jobs that need short-lived access with no static token. The jobs present a client certificate, exchange it for a trust token, and run the Pulumi CLI and OpenTofu with that token.

The user path this change must make work:

1. **API.** Make a bundle and one rule that names the workload SPIFFE ID and its principal (part 1 API).
2. **API.** Call POST /api/dustpan/v1/namespaces/{namespace}/trust-rules/exchange over direct TLS with the client certificate and read the trust token.
3. **CLI.** Run the Pulumi CLI or OpenTofu with the trust token until it expires or is revoked. A revoked token gives 401.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| No token when the chain, time, revocation or SPIFFE ID check fails. | 401 with NEW error codes mtls.no-client-certificate, mtls.unknown-issuer, mtls.expired, mtls.not-yet-valid, mtls.revoked, mtls.revocation-unknown, mtls.no-matching-rule, mtls.ambiguous-rule, mtls.bundle-disabled and mtls.principal-disabled; 429 with NEW error code mtls.rate-limited; 500 with NEW error code token.issue-failed and no token. A trust rule has no disabled state on main, so there is no code mtls.rule-disabled. | `curl --cert leaf.pem --key leaf.key https://server/api/dustpan/v1/namespaces/ns-xxx/trust-rules/exchange -v` |
| A rule write with a `*` in `spiffePattern`. | 400 with NEW error code trust-rule.spiffe-wildcard. The message names the field `spiffePattern` and says that the rule must name an exact SPIFFE ID. The server stores nothing. | `curl -X POST https://server/api/dustpan/v1/namespaces/ns-xxx/trust-rules -d '{"spiffePattern":"spiffe://example.org/ci/runner-1"}'` with the exact SPIFFE ID and the other fields of the rule |
| A revoked or disabled trust token gives no access. | 401 token.ended when the token was revoked or its principal was disabled; 401 token.invalid after expiry. | `curl -X POST https://server/api/dustpan/v1/namespaces/ns-xxx/trust-rules/exchange --cert leaf.pem --key leaf.key` to get a new token |

Evidence for the claims above:

- A workload exchange e2e test exists Proof: e2e/workload_test.go:38 TestWorkloadTokenExchange
- A trust token lifetime test exists Proof: pkg/accesstoken/trust_token_test.go:59 TestTrustTokenLivesTheExchangeLifetime
- The error code token.invalid exists Proof: internal/errcode/catalog_token.go:11

## What Changes

- The mTLS exchange verifies chain, time with no skew, bundle revocation and exact SPIFFE ID against one enabled rule over direct TLS.
- It issues a stored access token of the kind `mtls-exchange` (ADR 371 decision 2) with capped lifetime and an exchange audit entry in one transaction, with shared revoke and grant and limit enforcement.
- The request credential of the token stays `trust-token` (ADR 284 decision 2; ADR 371 changes no credential).
- NEW error codes mtls.* and token.issue-failed (see "Contract" in design.md).
- The store port `RecordExchange` gets the verified SPIFFE ID: the store matches the rule again in the exchange transaction, with a conformance suite before any adapter changes.
- The store port `RecordExchange` also gets the stable ID of the bundle and the number of the bundle version that the service verified the chain against. The store reads the bundle of the namespace in the same transaction and refuses unless that version is still the active version of the bundle of the rule (issue #3584).
- A trust rule write refuses a SPIFFE ID pattern with `*`, with the NEW error code trust-rule.spiffe-wildcard: a rule for the mTLS exchange names an exact SPIFFE ID (issue #3585).
- The exchange route answers an mTLS call when the request has an empty body.

Out of scope:

- Trust bundle and trust rule API (part 1).
- E2E proof with real CLIs and the docs page (part 3).
- JWT and OIDC exchange.
- New token life-cycle rules.
- The rename of the kind `trust` to `jwt-exchange` (change `token-kind-jwt-exchange`).

Depends on:

- mtls-trust-bundle-rules
- `mtls-trust-bundle-rules` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- A runner operator behind a TLS proxy gets a plain refusal that names direct TLS.
- An administrator who writes a rule with a `spiffePattern` that ends in `/*` gets a 400 at the write. The answer names the field `spiffePattern` and says that the rule must name an exact SPIFFE ID. The rule is not a silent rule that never matches.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The exchange store port has one method, RecordExchange, which only accepts the kind `jwt-exchange` (`pkg/trustrule/exchange.go:50`, `pkg/store/internal/trustrulestore/trustrulestore.go:233` func CheckExchange)
- The two adapters call CheckExchange inside the fenced transaction (`pkg/store/postgres/trustrules.go:299`, `pkg/store/sqlite/trustrules.go:313`, func RecordExchange)
- A trust bundle has an ID and one active version number, and no other state (`pkg/trustrule/trustrule.go:258`, type Bundle). Both adapters read the bundle row of a namespace with the query GetTrustBundleOfNamespace
- The SPIFFE ID pattern check allows a trailing `/*` (`pkg/trustrule/trustrule.go:410`, func ValidateSpiffePattern), and Rule.Validate runs it for the create, the update, the store and the restore (`pkg/trustrule/trustrule.go:581`, func Validate)
- The route maps an invalid rule to trust-rule.invalid (`internal/api/trustrules/handlers.go:570`, func classifyRefusal)
- The JWT exchange builds the Exchange value with named fields (`internal/trustrulesvc/exchange.go:218`, func Exchange)
- Trust token lifetime test (`pkg/accesstoken/trust_token_test.go:59`)
- Workload exchange e2e (`e2e/workload_test.go:38`)
- token.invalid code (`internal/errcode/catalog_token.go:11`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 54: Workload identity: OIDC and JWT token exchange, and mTLS** (not owner-tagged).

- Decisions that apply: 1 (current), 4 (current), 6 (changed).
- Changed by: ADR 69 extends; ADR 70 changes decisions 5 and 8; ADR 204 changes decision 3; ADR 222 extends decision 5; ADR 223 extends decision 5; ADR 284 changes decision 3; ADR 369 changes decision 6. Chain to the end: ADR 69, ADR 70, ADR 204, ADR 222, ADR 223, ADR 284, ADR 369, ADR 297, ADR 142, ADR 210, ADR 216, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 248, ADR 286, ADR 335, ADR 351, ADR 240, ADR 313, ADR 362, ADR 370, ADR 301, ADR 333, ADR 149, ADR 247, ADR 273, ADR 339, ADR 295, ADR 363.
- Current rule: this ADR, with ADR 69, 70, 204, 222, 223, 284, 369.
- Quote: "Workload identity is in milestone 4. Two kinds are in scope: token exchange with OIDC and JWT issuers, and mTLS client certificates."
- Quote: "The principal is not a person. A matched workload acts as a principal that is not a person, like the owner of a service token (ADR 53)."
- Effect: The exchange follows the mTLS scope, bundle check, and non-person principal.
- Conflict: none.

**ADR 284: The token exchange issues a stored access token** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current).
- Changed by: ADR 371 changes decision 1. Chain to the end: ADR 371.
- Current rule: this ADR, with ADR 371.
- Quote: "The exchange issues a stored access token. The exchange endpoint checks the JWT of a workload once, against the trust rules of the namespace."
- Effect: Decision 1 names the JWT exchange only. ADR 371 decision 2 decides the mTLS exchange: it issues a stored token of the kind `mtls-exchange` with the same life cycle.
- Conflict: none.

**ADR 371: Each way to prove identity in the token exchange has its own token kind** (owner-tagged (decisions 1, 2 and 3)).

- Decisions that apply: 2 (current), 3 (current), 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "An exchange that checks a TLS client certificate against the trust rules issues a stored access token of the kind `mtls-exchange` for the principal of the rule."
- Quote: "This ADR changes no credential: a token of either kind of decision 1 or 2 gives the credential `trust-token`."
- Effect: The mTLS exchange issues kind `mtls-exchange`. The token list and the issue audit entry show the kind. The credential stays `trust-token`.
- Conflict: none.

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

**ADR 250: Defense in depth for security code** (owner-tagged (all decisions): "The project owner decided it.").

- Decisions that apply: 1 (current), 3 (current), 4 (current).
- Changed by: ADR 382 extends decision 3. Chain to the end: ADR 382.
- Current rule: this ADR, with ADR 382.
- Quote: "Authorize at the point of use, for the object, not only the action and not only at the route."
- Quote: "The test sends hostile input directly to that layer, as if every other layer had failed."
- Effect: The store reads the bundle again in the transaction of the record, as a layer that does not rely on the service. The refusal of a wildcard is a check at the boundary of a rule write, and the store and the restore run it too. Each layer has its own hostile test.
- Conflict: none.

**ADR 362: Owner decisions of 8 November 2026: stable IDs, store order, MySQL, baseline hashes and portable backups** (owner-tagged (decision 1)).

- Decisions that apply: 1 (current).
- Changed by: ADR 363 extends decision 2. Chain to the end: ADR 363.
- Current rule: this ADR, with ADR 363.
- Quote: "Every stored reference to an entity uses the stable ID of the entity when a user can rename the entity, or delete and recreate it."
- Effect: The exchange value names the bundle by its stable ID. The version is a number in that bundle. The active version cannot be deleted, and a new version gets a number above every stored number, so the pair of the bundle ID and the number names one version for as long as it is active.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- May the mTLS exchange issue a stored token, and of which kind? ADR 371 decision 2 (owner, 2026-11-14): a stored access token of the kind `mtls-exchange`, with the life cycle of ADR 284 decision 1. The open question of the earlier draft is answered.
- Does the credential change? No. ADR 371 "Not decided here" keeps the credential `trust-token` for both kinds.

- Where does the revocation answer come from? Not decided here: the exchange takes a revocation check as an injected function, and no check means mtls.revocation-unknown with no token (fail closed). The source of the answer is outside this change.
- Stored or stateless token? Stored, as ADR 284 decision 1 does for JWT; its use for mTLS is open question 1.
- Lifetime? ADR 69 decision 6: setting, default 1 hour, capped at the leaf not-after time.
- Behind a TLS proxy? Direct TLS only here; a forwarded chain header is ignored. Proxy design is in part 3.
- Does the store check that the bundle version of the verify step is still active? Yes, in the same transaction as the token write (issue #3584; decision of the lead and the PM, provisional until the owner reviews it). The bundle row holds an ID and an active version number only. So the check is: the bundle exists in the namespace of the fence, it is the bundle of the rule, and its active version is the verified version.
- May a rule for the mTLS exchange hold a `*` in `spiffePattern`? No. The exchange needs an exact SPIFFE ID, and a pattern with `*` never matches it. The write refuses it (issue #3585; decision of the lead, provisional until the owner reviews it). A rule has no field for its kind: it is a rule for the mTLS exchange when `spiffePattern` is not empty. A rule with an empty `spiffePattern`, as the JWT rules have, is not changed.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
