Roadmap: `docs/roadmap.md:188` (E2E step and docs prove mTLS with real CLIs.).

## Why

**Persona: operator.** An operator sets up machine identity and must show that a workload with a client certificate can run stacks and workspaces. The operator needs one proven path from certificate to token to CLI, plus plain refusals for bad certificates.

The user path this change must make work:

1. **Server start.** Start the real binary over TLS with a trust bundle set on the namespace.
2. **API.** Exchange the client certificate with SPIFFE ID for a stored trust token.
3. **CLI.** Run pulumi up and tofu apply with the exchanged token.
4. **Docs page.** Follow the mTLS docs page for bundle, rule, exchange, CLI use and TLS proxy.
5. **E2e.** Run the e2e step with real CLIs over TLS and check the audit entry.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A certificate from another CA, an expired certificate or an ID that no rule names gives 401. | The server answers 401 with a body that names the failed match and the rule to check. No token is issued. | `curl --cert ./workload.pem --key ./workload-key.pem https://dustpan.example.com/api/dustpan/v1/namespaces/ns-01/trust-rules/exchange` |
| No client certificate at the exchange behind a TLS proxy gives 401. | The server answers 401 with a body that says no client certificate was seen and names the proxy rule in the docs page. | `opendustpan doctor` |

Evidence for the claims above:

- Milestone 4 step 9 asks for an mTLS workload that gets a token and runs the CLIs, with refusals for a foreign CA, an expired certificate and an unmatched ID. Proof: docs/roadmap.md:188 and roadmap step in the pack
- ADR 54 puts mTLS client certificates with a SPIFFE ID in the URI SAN and per-namespace trust rules in scope. Proof: docs/adr/0054-workload-identity-oidc-jwt-and-mtls.md decisions 4 and 5
- The exchange issues a stored access token of kind trust with the token policy lifetime. Proof: docs/adr/0284-the-token-exchange-issues-a-stored-access-token.md decision 1

## What Changes

- E2e step with the real binary over TLS: valid SPIFFE ID runs the Pulumi CLI and OpenTofu with the trust token; foreign CA, expired certificate and unmatched ID get no token.
- Docs page with the CLI certificate answer and the TLS proxy rule, in the same pull request.
- Audit check of the exchange entry in the e2e step.

Out of scope:

- Trust bundle and trust rule create, read, update and delete
- Exchange request shape, matching logic and trust token fields
- Token expiry enforcement, warnings and email

Depends on:

- mtls-trust-bundle-rules
- mtls-mtls-exchange-token
- `mtls-mtls-exchange-token` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- An operator copies four blocks: make the bundle, add the rule, exchange with the certificate, export the token for the CLIs.
- The proxy rule is one line: terminate mTLS at the server, else the exchange says no certificate was seen.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 54: Workload identity: OIDC and JWT token exchange, and mTLS** (not owner-tagged).

- Decisions that apply: 4 (current), 5 (changed).
- Changed by: ADR 69 extends; ADR 70 changes decisions 5 and 8; ADR 204 changes decision 3; ADR 222 extends decision 5; ADR 223 extends decision 5; ADR 284 changes decision 3. Chain to the end: ADR 69, ADR 70, ADR 204, ADR 222, ADR 223, ADR 284, ADR 297, ADR 142, ADR 210, ADR 216, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 248, ADR 286, ADR 335, ADR 351, ADR 240, ADR 313, ADR 362, ADR 301, ADR 333, ADR 149, ADR 247, ADR 273, ADR 339, ADR 295, ADR 363.
- Current rule: this ADR, with ADR 69, 70, 204, 222, 223, 284.
- Quote: "mTLS client certificates."
- Quote: "Trust rules per namespace."
- Effect: This part uses the mTLS identity and the namespace trust rule to prove the full path with real CLIs. It adds no new bundle or rule behavior.
- Conflict: none.

**ADR 284: The token exchange issues a stored access token** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The exchange issues a stored access token."
- Effect: The proof step exchanges a client certificate for the stored trust token of this decision and runs the CLIs with that token.
- Conflict: none.

**ADR 69: Token lifetime and session defaults** (not owner-tagged).

- Decisions that apply: 6 (current).
- Changed by: ADR 297 changes decision 4. Chain to the end: ADR 297, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 297.
- Quote: "An exchanged workload token lives 1 hour."
- Effect: The e2e step uses the 1-hour exchanged workload token life and shows its expiry in the docs page.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (wrong claim): proposal.md: the Open questions section says the spec is not ready because the command 'opendustpan docs' does not exist (commands listed: approve, audit-chain, ... no docs). The error table in the same file uses 'opendustpan docs mtls' as the one helping command, so the user-facing error text names a command that is not in the CLI. The gate must not pass until this is fixed. Resolution: answered: Replaced the helping command with `opendustpan doctor`, a listed CLI command; no new command is named.
- R1.2 (missing scenario): proposal.md error table lists a 401 for no client certificate at the exchange behind a TLS proxy, with a body that names the proxy rule. specs/mtls-proof/spec.md has no scenario for it and tasks.md 1.2 tests no such case, so the error has no proof. Resolution: answered: Added a refuse-hostile-certificates scenario for no certificate seen and an e2e refusal task for it.
- R1.3 (failure mode): design.md decision 2 allows a proxy to pass the verified client certificate fields. The design does not say how the server trusts those fields, so a client could send forged forwarded-certificate headers and get a token as another SPIFFE ID. The trusted-proxy list needed for this is not specified (compare ADR 321 decision 35 for header providers). Resolution: answered: Design now reads the certificate only from the TLS handshake and ignores forwarded headers, so no spoofed header gives a token.
- R1.4 (missing scenario): ADR 54 decision 8 says each exchange writes an audit entry. The spec checks one audit entry only for the success case. No scenario says that refused exchanges (foreign CA, expired, unmatched ID, no certificate) write an audit entry. Resolution: answered: Success and each refusal scenario now state the audit entry that the exchange writes.
- R1.5 (missing scenario): Hostile certificate inputs are not covered: a certificate with two URI SANs, a SPIFFE ID from another trust domain, or a SPIFFE ID with a wrong path. No scenario or test names these refusals. Resolution: answered: Added hostile certificate scenarios for two URI SANs, another trust domain and a wrong path.
- R1.6 (failure mode): ADR 69 decision 6 gives the exchanged token a 1-hour life. The proposal says the docs show its expiry, but no scenario covers a token that expires during a long pulumi up or tofu apply, or a token that is revoked through the service token routes (ADR 284 decision 1). Resolution: open question: Moved to open questions: expiry or revoke during a long CLI run.
- R1.7 (open question): No decision says what happens to live trust tokens when the trust rule or the trust bundle is removed, or when a client certificate is revoked before its expiry. Revocation by CRL or OCSP is not mentioned. ADR 223 decision 6 covers limits only, not this case. Resolution: open question: Moved to open questions: fate of live tokens at rule or bundle removal and CRL or OCSP.
- R1.8 (failure mode): The e2e server terminates TLS itself, so the proxy path in design.md decision 2 and the proxy docs rule are never run by a test. The docs scenario only proves the direct TLS path, so a wrong proxy rule would pass. Resolution: answered: Design forbids header pass-through so there is no proxy path to test; e2e proves the direct TLS path plus the no-certificate refusal that names the proxy rule.
- R1.9 (missing scenario): The proposal says 401 bodies name the failed match and the rule to check, but no spec scenario or task asserts the body of any refusal. The body text is therefore unchecked. Resolution: answered: Added a scenario that asserts the 401 body names the failed match and the rule to check.
- R1.10 (open question): Open questions say the CLIs use the exchanged token, but no decision says how the token reaches the CLIs (environment variable, file or flag) or how it is kept out of logs and shell history. The docs blocks depend on this. Resolution: open question: Moved to open questions: safe transport of the trust token to the CLIs.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What happens when a 1-hour trust token expires during a long run or is revoked?
   - Options: The CLI run fails at expiry and the operator repeats the exchange; The server extends the token for a running CLI.
   - Provisional decision (owner review 14 Nov): The run fails at expiry or revoke and the operator repeats the exchange.
   - Reason: Short fixed lifetimes are the normal practice and a repeat exchange is a plain retry. Benchmark: AWS: short-lived exchanged credentials expire in place and the caller repeats the exchange for a new one.
   - Persona impact: Operator repeats one exchange command; no extra work for other personas. Escape hatch: The operator repeats the exchange to get a new 1-hour token after expiry or revoke.
   - Owner-tagged ADRs involved: ADR 284 (their words do not change).
2. What happens to live trust tokens when the trust rule or bundle is removed, and is there CRL or OCSP for client certificates?
   - Options: Live tokens keep their lifetime and rule removal stops new exchanges only; Rule or bundle removal revokes all live tokens at once.
   - Provisional decision (owner review 14 Nov): Rule or bundle removal stops new exchanges; live tokens expire or are revoked explicitly.
   - Reason: A 1-hour expiry bounds the risk and matches the workload token practice. Benchmark: Google: removal of a trust rule stops new grants at once and live tokens expire on their own short lifetime.
   - Persona impact: No surprise for workloads beyond the 1-hour limit; an explicit revoke path stays. Escape hatch: The administrator revokes live trust tokens through the service token routes.
   - Owner-tagged ADRs involved: ADR 284 (their words do not change).
3. How does the exchanged trust token reach the CLIs without leaking into logs or shell history?
   - Options: Environment variable or file with mode 0600, never a flag or log line; Any caller-chosen transport.
   - Provisional decision (owner review 14 Nov): Docs use an environment variable or a file with mode 0600 and never a flag.
   - Reason: Environment or a locked file is the standard safe transport for short tokens. Benchmark: AWS: machine credentials pass through environment variables or files with strict file mode and never appear in logs.
   - Persona impact: Operator copies one safe block; token stays out of shell history and logs. Escape hatch: The docs show a file with mode 0600 and warn against command-line flags.

Questions considered and answered:

- Which identity and scope does the proof cover? ADR 54 decision 4 allows a SPIFFE ID in the URI SAN and ADR 54 decision 5 keeps the rule and its grants in one namespace. The proof covers a SPIFFE ID match and the refusal of another namespace.
- How do the Pulumi CLI and OpenTofu send a client certificate, and how does mTLS work behind a TLS proxy? ADR 284 decision 1 issues a stored trust token. The CLIs use that token, so the CLIs send no client certificate. The server reads the certificate only from the TLS handshake and ignores forwarded headers; behind a proxy the install must terminate mTLS at the server.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
