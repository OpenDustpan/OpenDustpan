Roadmap: `docs/roadmap.md:188` (Exchange issues a trust token for a matched workload.).

## Why

**Persona: runner operator.** A Runner operator runs CI jobs that need short-lived access without static tokens. The jobs present a client certificate, exchange it for a trust token, and run the Pulumi CLI and OpenTofu with that token.

The user path this change must make work:

1. **API.** Make an enabled bundle and one enabled rule that names the workload SPIFFE ID and its principal.
2. **API.** Call the exchange over TLS with the client certificate and read the short-lived trust token.
3. **CLI.** Run the tool CLI with the trust token until revoke or expiry; a revoked token gives 401.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| No token when the chain, time, revocation, or SPIFFE ID check fails. | 401 with `mtls.no-client-certificate`, then `mtls.unknown-issuer`, `mtls.expired`, `mtls.revoked`, `mtls.revocation-unknown`, or `mtls.no-matching-rule`; nothing is issued. | `curl --cert leaf.pem --key leaf.key https://server/namespaces/ns-xxx/trust-rules/exchange -v` |

## What Changes

- mTLS exchange route verifies chain, time with no skew, revocation, and exact SPIFFE ID against one enabled rule over direct TLS.
- Issue of a stored trust token with 1-hour lifetime and exchange audit in one transaction, with existing last-use record, inactivity disable, shared revoke, and grant plus limit enforcement.

Out of scope:

- Trust bundle and trust rule create, read, update, and delete API.
- E2E proof with real Pulumi CLI and OpenTofu runs.
- User docs page and console views.
- JWT and OIDC exchange path.
- New token life-cycle rules; this change reuses the existing expiry, maximum lifetime, and inactivity disable.

Depends on:

- mtls-trust-bundle-rules
- `mtls-trust-bundle-rules` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- A runner operator behind a TLS proxy gets a plain refusal in this change; the error names direct TLS termination and the one curl command that shows the chain.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 54: Workload identity: OIDC and JWT token exchange, and mTLS** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current), 4 (current), 6 (current).
- Changed by: ADR 69 extends; ADR 70 changes decisions 5 and 8; ADR 204 changes decision 3; ADR 222 extends decision 5; ADR 223 extends decision 5; ADR 284 changes decision 3. Chain to the end: ADR 69, ADR 70, ADR 204, ADR 222, ADR 223, ADR 284, ADR 297, ADR 142, ADR 210, ADR 216, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 248, ADR 286, ADR 335, ADR 351, ADR 240, ADR 313, ADR 362, ADR 301, ADR 333, ADR 149, ADR 247, ADR 273, ADR 339, ADR 295, ADR 363.
- Current rule: this ADR, with ADR 69, 70, 204, 222, 223, 284.
- Quote: "Workload identity is in milestone 4. Two kinds are in scope: token exchange with OIDC and JWT issuers, and mTLS client certificates."
- Quote: "mTLS client certificates. A workload can prove its identity with a TLS client certificate. The server checks the certificate against a trust bundle that an administrator configures. The identity can be a SPIFFE ID in the URI SAN, or another field of the certificate that a trust rule names."
- Quote: "The principal is not a person. A matched workload acts as a principal that is not a person, like the owner of a service token (ADR 53)."
- Effect: The exchange follows the mTLS decisions: mTLS is in scope, a workload proves identity with a client certificate checked against the bundle with SPIFFE ID in URI SAN, and a match acts as a principal that is not a person.
- Conflict: none.

**ADR 284: The token exchange issues a stored access token** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The exchange issues a stored access token. The exchange endpoint checks the JWT of a workload once, against the trust rules of the namespace. When one rule accepts it, the server issues a stored access token of ADR 6 of its own kind, `trust`, for the principal of the rule. The token has an ID, an audit entry for its issue, a lifetime from the token policy (ADR 69 decision 6) and a revoke through the routes of the service tokens."
- Effect: The mTLS exchange reuses the stored trust token kind by extension of this JWT decision: stored token with ID, audit entry, lifetime from token policy, and revoke through service-token routes.
- Conflict: none.

**ADR 53: Access token life cycle, inactivity disable and expiry warnings** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (extended), 4 (current), 7 (current), 13 (current).
- Changed by: ADR 69 extends decisions 2 and 11; ADR 70 changes decisions 5, 6 and 10; ADR 70 extends; ADR 212 extends decision 11; ADR 237 changes decision 9; ADR 354 extends. Chain to the end: ADR 69, ADR 70, ADR 212, ADR 237, ADR 354, ADR 297, ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 232, ADR 235, ADR 301, ADR 365, ADR 252, ADR 285, ADR 324, ADR 333, ADR 149, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 339, ADR 295, ADR 288, ADR 253, ADR 312.
- Current rule: this ADR, with ADR 69, 70, 212, 237, 354.
- Quote: "Last-used time and expiry. Each access token records the time of its last use. Each access token has an expiry."
- Quote: "Service tokens. A service token serves a machine that cannot sign in to the console. It belongs to a principal that is not a person. Workload identity comes first for machines (ADR 54)."
- Quote: "API and audit. Every token operation has a REST API and an action (ADR 17). Each operation writes an audit entry (ADR 41)."
- Effect: The trust token reuses the existing token life cycle: expiry, maximum lifetime, inactivity disable, non-person owner, API with action and audit. This change adds no new life-cycle rule.
- Conflict: none.

**ADR 69: Token lifetime and session defaults** (not owner-tagged).

- Decisions that apply: 6 (current).
- Changed by: ADR 297 changes decision 4. Chain to the end: ADR 297, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 297.
- Quote: "An exchanged workload token lives 1 hour. The token that a workload gets in an exchange (ADR 54) expires after 1 hour. It is a setting."
- Effect: The trust token from mTLS exchange expires after the 1-hour exchanged-token lifetime setting.
- Conflict: none.

**ADR 223: Access limits: Cedar forbid policies for each scope** (not owner-tagged).

- Decisions that apply: 6 (current).
- Changed by: ADR 240 changes decision 4; ADR 313 extends; ADR 362 changes decisions 4 and 11. Chain to the end: ADR 240, ADR 313, ADR 362, ADR 297, ADR 354, ADR 363, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 240, 313, 362.
- Quote: "Every credential. A limit holds for a session, an access token, a job token and a token that a trust rule issued (ADR 54). A trust rule still decides whether the server issues a token. It never passes a limit."
- Effect: Access limits hold for the trust token; the rule decides issue only and never passes a limit.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The open question on how the client chain reaches the exchange behind an operator TLS proxy is still open, so the 'Open questions' section does not read none and the spec-ready gate fails. The roadmap also says design is needed for this. Decide before build (mtls-mtls-exchange-token/proposal.md, Open questions item 1). Resolution: answered: Decided direct TLS termination only; proxy chain stays refused. Covered in considered, design structure, and verify requirement.
- R1.2 (open question): Revocation is required before issue, but no source is named (CRL, OCSP or a store) and no behavior is set when the source is unreachable. The fail-closed rule needs this answer (mtls-mtls-exchange-token/specs/mtls-exchange/spec.md, Requirement 'Verify chain and SPIFFE ID'; design.md, Layers, port adapter). Resolution: answered: Revocation comes from the bundle revocation source via the TLS verifier; unknown or unreachable refuses. Covered in considered and verify requirement scenarios.
- R1.3 (missing scenario): The spec has no scenarios for a revoked leaf, a leaf with no URI SAN, a request with no client certificate, more than one matching rule, or an over-long lifetime. The proposal error table and the design hostile tests name these cases, so the spec and its tests do not match (mtls-mtls-exchange-token/specs/mtls-exchange/spec.md). Resolution: answered: Added scenarios for revoked leaf, no client certificate, leaf with no URI SAN, more than one matching rule, and over-long lifetime.
- R1.4 (missing scenario): The design requires a forced store error to give a 500 with no token and a refusal audit entry. The spec has no scenario for an issue transaction that fails, so a partial write (token without audit, or audit without token) is not specified (mtls-mtls-exchange-token/design.md, Layers, HTTP handler; mtls-mtls-exchange-token/specs/mtls-exchange/spec.md). Resolution: answered: Added atomic-issue requirement with store-failure scenario: no token without audit and no audit without token.
- R1.5 (failure mode): The spec does not say what happens to live trust tokens when their matched rule, bundle or principal is disabled or deleted after issue. Requirement 'Enforce token limits and revoke' covers only grant reach and revoke, so a disabled rule may leave a working token. Resolution: answered: Added live-token requirement: revoke and principal disable end use; rule or bundle disable after issue leaves live token until expiry or revoke and refuses new exchanges.
- R1.6 (failure mode): Concurrency is not covered. A rule, bundle or principal can be disabled between the match check and the commit of the issue transaction. The spec does not say that the check and the write must share one transaction or one version check. Resolution: answered: Added one-transaction match and issue with version check and concurrent-disable scenario.
- R1.7 (failure mode): Hostile input is not covered. A leaf with several URI SANs, several SPIFFE IDs, or a SPIFFE ID that differs only in case or in trailing text has no rule. There is also no limit on exchange rate or chain size, and no statement of what a refusal audit entry records about the leaf. Resolution: answered: Added exact case-sensitive match rule, multiple-SAN handling, refusal audit content, and rate and size limit scenarios.
- R1.8 (open question): Clock skew is named as a risk in design.md, but no tolerance is set. The spec says 'not yet valid at exchange time' without a bound, so the accepted skew is left to the implementer. Resolution: answered: Set zero skew with server clock; spec states not valid at exchange time with no allowance.
- R1.9 (wrong claim): The ADR check lists ADR 54 decisions 5 and 8 as applicable, but both are changed by ADR 70. Their current text is not in the input, so the current rule for trust-rule naming and audit is not verified. The 'Questions considered' reasoning cites ADR 54 decision 5 as current, which the ADR index does not support. Resolution: answered: Removed ADR 54 decisions 5 and 8 from adr_check and considered; current-rule text for those decisions is not cited.
- R1.10 (wrong claim): The 'Questions considered' section cites ADR 284 decision 1 to support the mTLS exchange. That decision describes a JWT exchange, so the citation does not support the mTLS issue path without an explicit extension. Resolution: answered: Marked ADR 284 decision 1 as applied to mTLS by extension with ADR 54 decision 4 as the mTLS basis.
- R1.11 (scope): The change adds last-use recording and inactivity disable. These belong to the general token life cycle of ADR 53, not to the mTLS exchange item in the roadmap, so the owner of that work is not named. Resolution: answered: Clarified that last use and inactivity disable are reused existing rules; this change adds none, named in out_of_scope and adr_check effect.

## Open questions

none

Questions considered and answered:

- Does the exchange issue a stored token or a stateless token? ADR 54 decision 4 names mTLS with a bundle and SPIFFE ID in URI SAN; ADR 284 decision 1 names the stored kind trust with ID, audit, policy lifetime, and service-token revoke, applied here to mTLS by extension.
- What lifetime does the mTLS trust token have? ADR 69 decision 6: 1 hour, as a setting; the existing ADR 53 life cycle of last use, maximum lifetime, and inactivity disable holds with no new rule in this change.
- What can the issued token do? ADR 54 decisions 4 and 6 plus ADR 223 decision 6: one namespace only, principal that is not a person, grants decide reach, limits still hold.
- How does the exchange see the client chain behind a TLS proxy? Direct TLS termination at the server for this change; the server reads the chain from the TLS session only and refuses the exchange when no verified chain is present; a proxy-passed chain stays refused with a docs note.
- What revocation source applies and what happens when it is unreachable? The TLS verifier supplies chain validity, time, and revocation from the enabled bundle revocation source; any error or unknown revocation status refuses with no token.
- What clock skew tolerance applies? Zero seconds: the server uses its own clock at exchange time with no skew allowance; a leaf not valid at that time refuses.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
