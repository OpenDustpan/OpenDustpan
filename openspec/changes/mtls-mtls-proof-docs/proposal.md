---
version: 2
---

Roadmap: `docs/roadmap.md:188` (E2E step and docs prove mTLS with real CLIs.).

## Why

**Persona: operator.** An operator sets up machine identity and must show that a workload with a client certificate can run stacks and workspaces. The operator needs one proven path from certificate to token to CLI, plus plain refusals for bad certificates.

The user path this change must make work:

1. **Server start.** Start the real binary over TLS with a trust bundle set on the namespace.
2. **API.** Send POST /api/dustpan/v1/namespaces/{namespace}/trust-rules/exchange with the client certificate. The server issues a stored trust token.
3. **CLI.** Run pulumi up and tofu apply with the exchanged trust token as a bearer token.
4. **Docs page.** Follow the mTLS docs page for bundle, rule, exchange, CLI use, and TLS proxy.
5. **E2e.** Run the e2e step with real CLIs over TLS and check the audit entry.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A certificate from another CA, an expired certificate, or an ID that no rule names gives 401. | The server answers 401, issues no token, and the body names the failed match and the rule to check. Check the certificate dates, issuer, and SAN, then check the rule. | `openssl x509 -noout -dates -issuer -ext subjectAltName -in ./workload.pem` |
| No client certificate at the exchange behind a TLS proxy gives 401. | The server answers 401 and the body says no client certificate was seen and names the proxy rule: terminate mTLS at the server. Check that the proxy passes TLS through to the server. | `openssl s_client -connect dustpan.example.com:443 -showcerts` |
| The exchanged token runs a CLI write without a grant gives 403. | The server answers 403 and the body names the missing grant on the unit or collection. Read the grants of the principal, give it a grant and run the CLI again. | `GET /api/server/principals/{}/grants` |

Evidence for the claims above:

- Milestone 4 step 9 asks for an mTLS workload that gets a token and runs the CLIs, with refusals for a foreign CA, an expired certificate, and an unmatched ID. Proof: Context pack roadmap step: A workload with a client certificate from the trust bundle, with a SPIFFE ID in the URI SAN that a trust rule names, gets an access token, and the CLIs run with it.
- A workload token exchange e2e test exists in main. Proof: e2e/workload_test.go:38
- A trust token lifetime test exists in main. Proof: pkg/accesstoken/trust_token_test.go:59
- The trust rule exchange route is in the coverage matrix. Proof: POST /api/dustpan/v1/namespaces/{namespace}/trust-rules/exchange

## What Changes

- E2e step with the real binary over TLS: valid SPIFFE ID runs the Pulumi CLI and OpenTofu with the trust token; foreign CA, expired certificate, unmatched ID, hostile SANs, and forwarded header alone get no token.
- Grant and lifetime proof: 403 without a grant and across namespaces; 401 after expiry or revoke; rule removal stops new exchanges only.
- Docs page with the CLI certificate answer, safe token transport, and the TLS proxy rule, in the same pull request.
- Audit check of the exchange entry in the e2e step.

Out of scope:

- Trust bundle and trust rule create, read, update, and delete
- Exchange request shape, matching logic, and trust token fields
- Token expiry enforcement, warnings, and email

Depends on:

- mtls-trust-bundle-rules
- mtls-mtls-exchange-token
- `mtls-mtls-exchange-token` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- An operator copies four blocks: make the bundle, add the rule, exchange with the certificate, pass the token to the CLIs.
- The proxy rule is one line: terminate mTLS at the server, else the exchange says no certificate was seen.
- The token block keeps the token out of shell history and logs.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Workload token exchange e2e test exists. (`e2e/workload_test.go:38`)
- Trust token lifetime test exists. (`pkg/accesstoken/trust_token_test.go:59`)
- Dev cert bundle helper test exists. (`cmd/opendustpan/devtls_test.go:270`)
- Request log client IP test exists. (`pkg/dustpan/client_address_test.go:188`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 54: Workload identity: OIDC and JWT token exchange, and mTLS** (not owner-tagged).

- Decisions that apply: 4 (current), 5 (changed).
- Changed by: ADR 69 extends; ADR 70 changes decisions 5 and 8; ADR 204 changes decision 3; ADR 222 extends decision 5; ADR 223 extends decision 5; ADR 284 changes decision 3. Chain to the end: ADR 69, ADR 70, ADR 204, ADR 222, ADR 223, ADR 284, ADR 297, ADR 142, ADR 210, ADR 216, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 248, ADR 286, ADR 335, ADR 351, ADR 240, ADR 313, ADR 362, ADR 301, ADR 333, ADR 149, ADR 247, ADR 273, ADR 339, ADR 295, ADR 363.
- Current rule: this ADR, with ADR 69, 70, 204, 222, 223, 284.
- Quote: "A workload can prove its identity with a TLS client certificate."
- Quote: "A trust rule belongs to one namespace."
- Effect: This part adds no bundle or rule code. It proves the mTLS identity and the namespace rule with real CLIs.
- Conflict: none.

**ADR 69: Token lifetime and session defaults** (not owner-tagged).

- Decisions that apply: 6 (current).
- Changed by: ADR 297 changes decision 4. Chain to the end: ADR 297, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 297.
- Quote: "The token that a workload gets in an exchange (ADR 54) expires after 1 hour."
- Effect: The e2e step uses a 1-hour exchanged token and the docs page states the 1-hour life.
- Conflict: none.

**ADR 284: The token exchange issues a stored access token** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "When one rule accepts it, the server issues a stored access token of ADR 6 of its own kind, `trust`, for the principal of the rule."
- Effect: The proof exchanges the certificate for the stored trust token and runs the CLIs with that token.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: task "Add failing e2e refusals on the exchange route: hostile certificates a" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part; a code check fails: task "Add failing e2e grant and lifetime checks with the trust token: 403 wi" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part; a code check fails: task "Wire the e2e step with test CA, SPIFFE ID certificate, trust bundle, a" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part; a code check fails: and 1 more.

Questions considered and answered:

- Which identity and scope does the proof cover? ADR 54 decision 4 allows a SPIFFE ID in the URI SAN and decision 5 keeps the rule and its grants in one namespace. The proof covers a SPIFFE ID match, the refusal in another namespace, and the 403 when the principal lacks a grant.
- How do the CLIs send a client certificate, and how does mTLS work behind a TLS proxy? ADR 284 decision 1 issues a stored trust token and the CLIs use that bearer token, so the CLIs send no client certificate. The server reads the certificate only from the TLS handshake on the exchange route and ignores forwarded headers. Behind a proxy the install must terminate mTLS at the server.
- What happens to live trust tokens at rule removal, expiry, or revoke? ADR 69 decision 6 bounds the token to 1 hour and ADR 284 stores it as a normal access token. Rule or bundle removal stops new exchanges only; a live token runs until expiry or revoke.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
