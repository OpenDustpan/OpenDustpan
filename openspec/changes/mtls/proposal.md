Roadmap: `docs/roadmap.md:188` (mTLS).

## Why

**Persona: developer.** A developer runs the Pulumi CLI and OpenTofu from a CI workload that cannot hold a long-lived token. The workload presents its mTLS client certificate, gets a short-lived token, and runs its change with only the access its principal needs.

The user path this change must make work:

1. **API.** Make a trust bundle and a trust rule for the namespace with the SPIFFE ID of the workload.
2. **API.** Exchange the client certificate for a short-lived access token at POST /trust/v1/namespaces/{namespace}/exchange-mtls with a generic HTTPS client.
3. **CLI.** Run the Pulumi CLI and OpenTofu with the issued token against a unit of the namespace.
4. **Docs page.** Read the audit export for the exchange entries and the docs page for the setup steps.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Certificate from another CA or expired certificate at exchange | 401 with code workload.no-match and the body names the trust rule check. Run the trust rule read to check the SPIFFE ID and bundle. | `opendustpan trust-rule get --namespace {namespace} {rule}` |
| SPIFFE ID that no trust rule names | 401 with code workload.no-match and the body names the trust rule check. Run the trust rule list to check which ID the rule names. | `opendustpan trust-rule list --namespace {namespace}` |
| Workload token used on a namespace or collection its principal cannot use | 403 with code namespace.denied and the body names the object. Run the grant list to check the reach of the workload principal. | `opendustpan grant list --namespace {namespace}` |

Evidence for the claims above:

- Trust rules have e2e, guide and code today. Proof: e2e/trust_rules_test.go, e2e/workload_test.go, docs/guides/workload-identity.md, pkg/trustrule
- No shipped e2e, guide or code names mTLS or SPIFFE. Proof: Keyword evidence for mTLS: e2e [], guides [], code [], e2e_count 0. Keyword evidence for SPIFFE: e2e [], guides [], code [], e2e_count 0.
- Bearer tokens from workload exchange already run the tool CLIs in the workload e2e. Proof: e2e/workload_test.go

## What Changes

- Trust bundle per namespace and mTLS exchange that checks the client chain and matches a trust rule on the SPIFFE ID in the URI SAN.
- Stored trust token for a matched mTLS workload with token policy lifetime and service token revoke path.
- Audit entries for each mTLS exchange and each trust rule change, plus REST API and actions for trust bundle and trust rule operations.
- E2E step with the real binary over TLS and the real CLIs, and a docs page.

Out of scope:

- JWT and OIDC token exchange
- Token life cycle, expiry email and inactivity disable
- Groups, roles, grants and access review
- mTLS behind a TLS proxy

Depends on:

- token-life-cycle-expiry-email
- groups-roles-grants
- fresh-authentication-window-0-turns
- `token-life-cycle-expiry-email` (order of the milestone review)
- `groups-roles-grants` (order of the milestone review)
- `fresh-authentication-window-0-turns` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A developer must make a trust bundle and a trust rule before the first exchange, which costs time on the first setup.
- A certificate failure gives 401 with no hint of bundle, ID or expiry, so the docs page must show the one check command.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 54: Workload identity: OIDC and JWT token exchange, and mTLS** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current), 4 (current), 5 (changed), 6 (current), 8 (changed).
- Changed by: ADR 69 extends; ADR 70 changes decisions 5 and 8; ADR 204 changes decision 3; ADR 222 extends decision 5; ADR 223 extends decision 5; ADR 284 changes decision 3. Chain to the end: ADR 69, ADR 70, ADR 204, ADR 222, ADR 223, ADR 284, ADR 297, ADR 142, ADR 210, ADR 216, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 248, ADR 286, ADR 335, ADR 351, ADR 240, ADR 313, ADR 362, ADR 301, ADR 333, ADR 149, ADR 247, ADR 273, ADR 339, ADR 295, ADR 363.
- Current rule: this ADR, with ADR 69, 70, 204, 222, 223, 284.
- Quote: "Workload identity is in milestone 4. Two kinds are in scope: token exchange with OIDC and JWT issuers, and mTLS client certificates."
- Quote: "mTLS client certificates. A workload can prove its identity with a TLS client certificate. The server checks the certificate against a trust bundle that an administrator configures. The identity can be a SPIFFE ID in the URI SAN, or another field of the certificate that a trust rule names."
- Quote: "Trust rules per namespace. A trust rule belongs to one namespace. It names the issuer or the trust bundle, conditions on the claims or on the certificate fields, and the principal that a match gets. The grants of that principal decide what it can do (ADR 52). A rule never gives access to another namespace."
- Quote: "The principal is not a person. A matched workload acts as a principal that is not a person, like the owner of a service token (ADR 53)."
- Effect: This change carries out the mTLS half of the workload identity decision. Trust rules, principals that are not persons, audit entries and REST actions follow the same rules as token exchange.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The proposal section Open questions reads none, but lists two open questions (TLS proxy path, CLI certificate support) with provisional answers. Per the rule the provisional mechanism is accepted, but the change still has no decided design for the proxy path (R1.14) and no proof that Pulumi and OpenTofu send client certificates, so the structure and e2e task cannot be built right yet. Resolution: answered: Direct TLS only is now the decided design; proxy path is out of scope, and the CLIs use the issued bearer token so no CLI needs to send a certificate. Covered in design decision 2, out_of_scope, considered, and the e2e task.
- R1.2 (open question): No authorization action is named for creating a trust rule or trust bundle, and no rule says who may bind a rule to a principal. A caller who writes a rule can mint a token for any principal of the namespace. ADR 54 decision 8 requires an action for each trust rule operation, and ADR 67 decision 4 limits who may give the right to make tokens. Resolution: answered: Added action namespace:manage-trust-rules on the namespace for trust rule and trust bundle operations, plus the manage-access reach rule and fresh authentication. Covered by new auth requirements.
- R1.3 (adr conflict): ADR 54 decision 8 says each trust rule operation has a REST API and an action; the spec defines no action or route for trust bundle or trust rule operations, and the proposal's error table names trust-rule commands that are not specified. Resolution: answered: Added REST routes and actions for trust bundle and trust rule operations in the auth requirements and tasks; error commands now map to specified routes.
- R1.4 (missing scenario): The proposal error table promises 403 namespace.denied for a workload token used outside its reach, but the spec has no requirement or scenario for that refusal. Resolution: answered: Added requirement use-inside-grants with 403 scenario for a token used outside its reach.
- R1.5 (missing scenario): No scenario covers certificate revocation or the wrong purpose chain (no client authentication EKU), although the design layer table requires both checks and lists hostile tests for them. Resolution: answered: Added chain-checks requirement with revoked and wrong-purpose scenarios and hostile tests.
- R1.6 (failure mode): The spec does not define the result when two trust rules of one namespace name the same SPIFFE ID, or when a certificate has several or no URI SANs. The requirement says one rule matches, but the ambiguous case is not stated. Resolution: answered: Added ambiguity requirement: duplicate SPIFFE ID rules, several URI SANs, and zero URI SANs each give 401.
- R1.7 (missing scenario): The spec does not say what happens to an issued trust token when its trust rule or trust bundle is removed or changed while the token is still valid, and does not say that access limits (ADR 223 decision 6) apply to the mTLS trust token. Resolution: answered: Added requirements token-after-rule-change and limits-hold: issued token stays valid to expiry or revoke, and access limits hold for it.
- R1.8 (failure mode): Trust bundle rotation (CA overlap), a certificate with a future not-before time, and clock skew are not covered by any requirement or scenario. Resolution: answered: Added time-checks requirement for future not-before and clock behavior, and bundle rotation by overlapping bundles.
- R1.9 (missing scenario): Refused exchanges (401) and trust bundle changes have no audit entry in the spec, and the fingerprint named in the design audit list is not required. ADR 54 decision 8 requires an audit entry for each trust rule change, which includes the bundle. Resolution: answered: Added audit requirement covering issued, refused, rule change and bundle change entries with certificate fingerprint.
- R1.10 (open question): The change does not say whether a trust rule or trust bundle change needs a fresh authentication. ADR 227 decision 2 (owner) lists token creation and weaker settings as steps that need one, and this change adds a path that issues tokens. Resolution: answered: Added fresh-auth requirement for trust rule and trust bundle changes per ADR 227.
- R1.11 (scope): The valid-certificate scenario mixes the exchange with running pulumi up and tofu apply. The CLI run belongs to the e2e task, not to the exchange requirement. Resolution: answered: Split the valid-certificate scenario to issuance only; CLI runs moved to the e2e task and the docs check.
- R2.1 (open question): The roadmap step requires a design answer for which tool CLIs can send a client certificate and how mTLS works behind a TLS proxy. The change answers this in the proposal's Open questions only by declaring direct TLS and a separate exchange client, but no ADR or owner decision answers the proxy question, and design.md still lists proxy hiding the certificate as an unresolved risk. Resolution: open question: Moved to open_questions: direct TLS only in this change with a specified exchange path; proxy header path deferred to a later design. Covered in design decision 2, out_of_scope and open_questions.
- R2.2 (wrong claim): The proposal says the tool CLIs use the issued bearer token and a separate exchange client presents the certificate, but no requirement, task or e2e step specifies that exchange client; tasks 2.3 uses curl, which contradicts the stated CLI path. Resolution: answered: Stated that the exchange client is any HTTPS client such as curl and the tool CLIs never send the certificate. Covered in design decision 2, considered, spec exchange-client requirement and e2e task.
- R2.3 (adr conflict): ADR 54 decision 8 ("Each exchange and each trust rule change writes an audit entry") is met, but ADR 54 decision 2 and ADR 284 decision 1 require a token lifetime from the token policy; the spec's lifetime requirement names the exchanged workload token lifetime setting (ADR 69 decision 6) without stating the default or how a namespace shorter maximum (ADR 69 decision 2) applies, so the lifetime rule is not fully specified. Resolution: answered: Specified lifetime as the exchanged workload token setting with 1 hour default capped by the shorter namespace maximum. Covered by ADR 69 decisions 2 and 6 and the token-lifetime requirement.
- R2.4 (missing scenario): The spec has no scenario for a disabled trust rule or a disabled trust bundle, although the duplicate-rule requirement refers to enabled rules only. Resolution: answered: Added disabled-rule and disabled-bundle scenarios that give 401. Covered by the enabled-rule requirement.
- R2.5 (failure mode): Trust bundle rotation and revocation state are named in the chain checks, but no requirement says how the revocation state is obtained (CRL, OCSP or stored list), how it is refreshed, or what happens when it is unreachable; a fail-open default would accept revoked certificates. Resolution: answered: Added revocation requirement: stored serial deny list on the bundle, checked at each exchange, fail closed on missing state. Covered by the revocation requirement and hostile tests.
- R2.6 (failure mode): Concurrent exchanges and concurrent rule changes are not covered: a rule removed during an exchange can still issue a token, and no requirement orders the check against the write. Resolution: answered: Added atomic-check requirement: match and issue read the rule set in one transaction snapshot so a rule removed before issue gives no token. Covered by the atomic-check requirement and design decision 3.
- R2.7 (missing scenario): The spec does not cover the case where the trust bundle contains a CA that is not a configured trust anchor for the namespace, or a bundle that is itself malformed or empty at save time. Resolution: answered: Added bundle-save requirement that refuses a malformed, empty or non-anchor bundle with 400. Covered by the bundle-save requirement.
- R2.8 (scope): The mTLS exchange endpoint and its route are not given a path, and ADR 210 decision 2 requires that protocol endpoints stored by an external system stay outside /api/dustpan/v1; the change does not say which path applies. Resolution: answered: Named the exchange path POST /trust/v1/namespaces/{namespace}/exchange-mtls outside /api/dustpan/v1 per ADR 210 decision 2. Covered in design decision 2 and the exchange-path requirement.
- R2.9 (adr conflict): ADR 227 decision 2 (owner) requires a fresh authentication for token creation; the exchange itself is machine-issued, and the spec does not state whether the exchange needs fresh authentication or how the ADR 53 decision 3 action per token kind applies to the trust token. Resolution: answered: Stated the machine exchange needs no fresh authentication and no grant; the rule match is the right, and human rule changes need fresh authentication. Covered by ADR 53 decision 3, ADR 227 and the no-fresh-auth requirement.
- R3.1 (open question): The roadmap step requires a design answer for how mTLS works behind an operator TLS proxy, and the design only defers it. The proposal's Open questions section reads none, yet design.md Risks still names the proxy path as an unresolved open question and the provisional answer is not an ADR or owner decision. The change cannot be built right until the proxy path is decided or explicitly excluded from the roadmap step. File: design.md and proposal.md. Resolution: open question: Proxy path stays an open question with direct TLS only in this change and proxy deferred; out_of_scope and design risks state the fallback to JWT exchange or service tokens.
- R3.2 (open question): No requirement or evidence shows that the Pulumi CLI and OpenTofu CLI can use an issued bearer token from a generic exchange client as the roadmap states, nor which tool CLIs can send a client certificate. The e2e task 2.3 depends on this and the claim is not proved. File: tasks.md. Resolution: answered: Exchange returns a stored bearer trust token that the CLIs use; e2e/workload_test.go shows bearer tokens run the CLIs and the e2e task exchanges with a generic HTTPS client then runs both CLIs with the issued token.
- R3.3 (adr conflict): ADR 54 decision 4 says the identity can be a SPIFFE ID or another certificate field that a trust rule names, while the spec and design allow only exact SPIFFE ID plus one optional field; the wording of the optional field and its ADR basis is not stated. File: design.md. Resolution: answered: Design decision 1 now names the optional named field as the other field of ADR 54 decision 4, exact match only with no substring and no regular expression.
- R3.4 (wrong claim): The ADR check says ADR 54 decision 8 ties audit to each exchange, but it also lists ADR 54 decision 5 as changed while the ADR check treats the mTLS half as current; the 'Changed by' and 'Current rule' lines are computed, so only the stated effect for the rule namespace fence needs proof. File: proposal.md. Resolution: answered: Namespace fence effect follows ADR 54 decision 5 quote that a rule never gives access to another namespace, covered by the namespace-fence requirement.
- R3.5 (missing scenario): No scenario covers a trust bundle rotation with overlapping CAs, a bundle removed while exchanges run, or a revoked deny list entry that is later removed. The design names rotation but no requirement covers it. File: specs/mtls-exchange/spec.md. Resolution: answered: Added bundle-rotation requirement with overlapping bundles and removal behavior, covered by new tasks and docs check.
- R3.6 (failure mode): Hostile inputs such as an oversized certificate chain, a chain with a self-signed root that is also in the bundle, or a certificate with a SAN that is a URI with a different scheme are not covered by any requirement or hostile test. File: specs/mtls-exchange/spec.md. Resolution: answered: Added hostile-chain requirement for oversized chain, self-signed root and non-SPIFFE URI scheme with 401 scenarios and hostile tests.
- R3.7 (open question): The provisional answer on the proxy path is only a benchmark; it leaves the exchange path behind a proxy undefined for operators who already run one, with no decided fallback in the change. File: proposal.md. Resolution: open question: Open question keeps the benchmark and names the decided fallback in this change: JWT exchange or service tokens for proxy operators.
- R3.8 (scope): The change adds trust bundle and trust rule REST API, the manage-trust-rules action and the fresh-auth rule, which belong to the ADR 54 and ADR 67 items of the roadmap; the change also claims service token revoke semantics that belong to token-life-cycle-expiry-email. File: proposal.md. Resolution: answered: Trust bundle and rule management reuses the existing trust-rules routes named in the pack; revoke path reuses the token-life-cycle-expiry-email change listed in depends_on.
- R3.9 (failure mode): The stored revocation state is a deny list on the bundle, but no requirement says how a serial is added, how it is replicated in a federated installation, or how a revoked serial is reported in the audit export. File: design.md. Resolution: answered: Added revocation-manage requirement: serials added through the bundle route with manage-trust-rules action and fresh auth, read at each exchange fail closed, and reported in the audit export.
- M.20 (gap): Step 9 needs the design answer for client certificates from the tool CLIs and for a TLS proxy of the operator. The change puts the proxy out of scope and has no scenario that proves the answer. Trust rules are shared with the JWT exchange of ADR 54, which no change covers. Resolution: answered: Direct TLS only with proxied request refused is now in design decision 2, proved by the new proxied-no-certificate 401 scenario, the TLS layer hostile test, and the e2e task. Neither CLI sends a certificate is stated in design and considered. The one trust rule model of ADR 54 is reused per design decision 1. fresh-authentication-window-0-turns added to depends_on and considered.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. How does mTLS work behind an operator TLS proxy that ends TLS before the server?
   - Options: Direct TLS only in this change, proxy path later; Accept a proxy header for the client identity in this change.
   - Provisional decision (owner review 14 Nov): Direct TLS only in this change, proxy path later
   - Reason: A proxy header that carries identity without attestation lets any caller claim any workload, so direct TLS only is the safe default until a proxy attestation design ships. Benchmark: AWS expects direct TLS to the service for mTLS identity and documents header pass-through only with a trusted proxy that signs the attested identity.
   - Persona impact: A developer behind an operator TLS proxy cannot use mTLS in this change and uses the JWT exchange instead, so no persona is blocked. Escape hatch: Operators who end TLS at a proxy use JWT exchange or service tokens until the proxy design ships.

Questions considered and answered:

- How does the server prove a workload with mTLS? ADR 54 decisions 4 and 5 answer it. The server checks the client certificate against a configured trust bundle and applies per-namespace trust rules on certificate fields. A rule never gives access to another namespace.
- What token does a matched workload get? ADR 284 decision 1 answers it. The exchange issues a stored access token of kind trust with ID, audit entry, token policy lifetime and revoke through service token routes.
- What part of this step already ships? The pack evidence answers it. Trust rules have e2e files e2e/trust_rules_test.go and e2e/workload_test.go, guide docs/guides/workload-identity.md and code pkg/trustrule. The keyword evidence lists for mTLS show e2e [], guides [] and code [], so no shipped mTLS e2e, guide or code is claimed.
- Which tool CLIs can send a client certificate, and how does mTLS work behind a TLS proxy? Direct TLS only in this change. No tool CLI sends the client certificate: neither the Pulumi CLI nor OpenTofu sends one. A generic HTTPS exchange client presents the certificate, gets the stored trust token, and the Pulumi CLI and OpenTofu use that bearer token. A proxied request with no client certificate gets no token. A proxy header path is out of scope and is an open question.
- Who may make a trust rule or trust bundle that mints a token for a principal? ADR 54 decision 8 and ADR 67 decision 4 answer it. Each trust rule and trust bundle operation has a REST API with action namespace:manage-trust-rules on the namespace, and a grant of that role needs namespace:manage-access reach over the target principal.
- Does a trust rule or trust bundle change need a fresh authentication? ADR 227 decision 2 answers it. A trust rule or trust bundle change is a token-issuing path, so it needs a fresh authentication within freshAuthSeconds. The machine exchange itself needs no fresh authentication because no person acts. The window-0 behavior is defined by the fresh-authentication-window-0-turns change.
- Which action gates the mTLS exchange itself? ADR 53 decision 3 answers it. The trust token is its own token kind. The right to get one is the rule match on the presented certificate, not a grant. The grants of the named principal then decide what the token can do.
- Which certificate fields can a trust rule match? ADR 54 decision 4 answers it. The identity is the SPIFFE ID in the URI SAN by default, or one other named certificate field that a trust rule names. This change supports exact SPIFFE ID match, plus one optional exact match on a named field, with no substring and no regular expression.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
