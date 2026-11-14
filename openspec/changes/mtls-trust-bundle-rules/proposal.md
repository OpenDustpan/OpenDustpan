Roadmap: `docs/roadmap.md:188` (Bundle and rule API with audit ships first.).

## Why

**Persona: namespace administrator.** As a namespace administrator I share an installation with CI pipelines and I need workload identity for machines so each pipeline gets only the access its principal holds, with every bundle and rule change on record.

The user path this change must make work:

1. **API.** Administrator saves the namespace trust bundle from the docs page
2. **API.** Administrator creates a trust rule that names the bundle, the SPIFFE ID, and the principal
3. **API.** Administrator deletes a rule that the team no longer needs
4. **API.** Administrator reads the audit entries for the change
5. **Console.** Follow the same steps in the console when the console ships them

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Caller without grant creates a rule | Server answers 403 with the action and the one command to check grants. | `opendustpan namespace grants list --namespace <namespace>` |
| Rule change with stale authentication | Server answers 401 fresh-authentication-required and names the command that shows the session state. | `opendustpan status` |
| Bundle save with bad PEM or expired CA | Server answers 400 with the CA index and the one command that validates the bundle. | `opendustpan namespace trust-bundle check --namespace <namespace>` |

Evidence for the claims above:

- Trust-rule routes exist in the coverage matrix with weak docs, console, refusal, and audit cells. Proof: GET /api/dustpan/v1/namespaces/{namespace}/trust-rules, GET /api/dustpan/v1/namespaces/{namespace}/trust-rules/{rule_id}, POST /api/dustpan/v1/namespaces/{namespace}/trust-rules, DELETE /api/dustpan/v1/namespaces/{namespace}/trust-rules/{rule_id}
- Roadmap splits mTLS into 3 parts and this part covers bundle store plus rule API with audit, not the exchange. Proof: This part specifies the namespace trust bundle store and the trust rule REST API and actions with audit entries. It leaves the mTLS exchange and trust token and the E2E proof and docs to the later parts.

## What Changes

- Versioned namespace trust bundle store with save checks and rotation
- Trust rule REST API with namespace actions, fence, fresh authentication, and audit entries

Out of scope:

- mTLS exchange endpoint and trust token issue
- E2E proof with real CLIs and user docs for mTLS
- Token life cycle expiry email and warning display

## Usability

Where the path still costs the person time or surprises them:

- Bundle errors name the bad CA index and the check command, not a TLS trace.
- Rule list shows effective bundle version and principal so the administrator sees what a match gets.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 54: Workload identity: OIDC and JWT token exchange, and mTLS** (not owner-tagged).

- Decisions that apply: 4 (current).
- Changed by: ADR 69 extends; ADR 70 changes decisions 5 and 8; ADR 204 changes decision 3; ADR 222 extends decision 5; ADR 223 extends decision 5; ADR 284 changes decision 3. Chain to the end: ADR 69, ADR 70, ADR 204, ADR 222, ADR 223, ADR 284, ADR 297, ADR 142, ADR 210, ADR 216, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 248, ADR 286, ADR 335, ADR 351, ADR 240, ADR 313, ADR 362, ADR 301, ADR 333, ADR 149, ADR 247, ADR 273, ADR 339, ADR 295, ADR 363.
- Current rule: this ADR, with ADR 69, 70, 204, 222, 223, 284.
- Quote: "mTLS client certificates. A workload can prove its identity with a TLS client certificate. The server checks the certificate against a trust bundle that an administrator configures. The identity can be a SPIFFE ID in the URI SAN, or another field of the certificate that a trust rule names."
- Effect: This part ships the mTLS bundle store that decision 4 requires, with no exchange.
- Conflict: none.

**ADR 70: Act-as reach, identity provider groups and break-glass accounts** (not owner-tagged).

- Decisions that apply: 3 (current).
- Changed by: ADR 142 changes decision 26; ADR 142 extends decision 13; ADR 210 changes; ADR 216 changes decisions 11, 23, 24 and 28; ADR 222 extends decisions 16, 17 and 23; ADR 227 extends decisions 16, 17 and 23; ADR 245 changes decisions 26 and 27; ADR 306 changes decisions 24, 25 and 26; ADR 306 extends decision 21; ADR 320 extends decision 12; ADR 321 extends decision 19; ADR 332 extends decisions 12, 14, 16 and 22; ADR 354 changes decision 13. Chain to the end: ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 149, ADR 297, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 295, ADR 333.
- Current rule: this ADR, with ADR 142, 210, 216, 222, 227, 245, 306, 320, 321, 332, 354.
- Quote: "A grant of an act-as right needs the reach of its target. These changes need a granter whose reach covers the reach of the target principal: - a grant of a role with `principal:manage-tokens` on that principal, to a principal or to a group, - a new member of a group that holds such a grant.  This is the rule that the owner set for principals of a namespace. It now applies to the whole installation. It comes in addition to the action that ADR 67 decision 4 asks for."
- Effect: A grant that gives token rights on the rule principal needs reach of that principal in addition to the namespace action.
- Conflict: none.

**ADR 284: The token exchange issues a stored access token** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The exchange issues a stored access token. The exchange endpoint checks the JWT of a workload once, against the trust rules of the namespace. When one rule accepts it, the server issues a stored access token of ADR 6 of its own kind, `trust`, for the principal of the rule."
- Quote: "The trust rules refuse the issuers of this installation. A rule cannot name an issuer at or under a URL of an issuer of this installation (ADR 204 decision 7)."
- Effect: Rules name principals that later get stored trust tokens, and rules refuse the issuers of this installation at save.
- Conflict: none.

**ADR 216: Namespace administration and operator scope: where each installation action goes (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current), 14 (current).
- Changed by: ADR 273 changes decision 8. Chain to the end: ADR 273, ADR 286.
- Current rule: this ADR, with ADR 273.
- Quote: "The map of each action. Operator actions keep the area `installation:`. A namespace action takes the area `namespace:` and has the namespace as its object."
- Quote: "Routes. The paths of ADR 210 decision 3 do not change. A namespace object is under `/api/dustpan/v1/namespaces/{namespace}/...`, for example `.../namespaces/{namespace}/groups/{id}` and `.../namespaces/{namespace}/sign-i"
- Effect: Bundle and rule objects live under the namespace path and use namespace actions.
- Conflict: none.

**ADR 227: One fresh-authentication window** (owner-tagged: decision 2).

- Decisions that apply: 2 (current).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends. Chain to the end: ADR 301, ADR 320, ADR 339, ADR 354, ADR 332.
- Current rule: this ADR, with ADR 301, 320, 339, 354.
- Quote: "One window everywhere (owner). The installation has one setting, `freshAuthSeconds`, with the default 900 (15 minutes)."
- Effect: Rule and bundle writes need a fresh authentication with the one window.
- Conflict: none.

**ADR 223: Access limits: Cedar forbid policies for each scope** (not owner-tagged).

- Decisions that apply: 6 (current), 9 (current).
- Changed by: ADR 240 changes decision 4; ADR 313 extends; ADR 362 changes decisions 4 and 11. Chain to the end: ADR 240, ADR 313, ADR 362, ADR 297, ADR 354, ADR 363, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 240, 313, 362.
- Quote: "Every credential. A limit holds for a session, an access token, a job token and a token that a trust rule issued (ADR 54)."
- Quote: "Changes. A change of a limit needs the action of its scope (ADR 230 decision 2), a fresh authentication (ADR 227), and writes an audit entry."
- Effect: Tokens later issued from mTLS rules stay inside access limits, and rule changes follow the change rule.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The open questions section reads not spec-ready (a code check fails on command `opendustpan auth`), so the change cannot be implemented until that check is resolved; proposal.md and its gate must be corrected. Resolution: answered: Fixed invalid command; fresh-auth error now names `opendustpan status`.
- R1.2 (adr conflict): ADR 284 decision 3 (owner) requires the trust rules to refuse issuers of this installation, checked at save and again at each exchange; the design's own-issuer refusal is only in the port layer and the spec for rules has no scenario refusing an own-issuer URL, so the requirement is not in the rule specs. Resolution: answered: Added rule requirement that refuses own-issuer URLs with a scenario.
- R1.3 (adr conflict): ADR 54 decision 5 and ADR 284 decision 3 require a rule to name the issuer or the trust bundle and the principal; the rule spec has no requirement or scenario for a rule that names a stable rule ID, a stable principal subject, or a bundle version reference, although design decision 2 requires them. Resolution: answered: Added rule requirement that stores stable rule ID, principal subject, and bundle version reference.
- R1.4 (missing scenario): The spec has no scenario for a rule that references a bundle version that is later rotated out or whose CA expires; the design says expiry is checked at use, but no requirement states what happens to an existing rule at exchange time or on list. Resolution: answered: Added rule requirement for rotated or expired bundle versions with list and read behavior.
- R1.5 (missing scenario): No scenario covers deleting or removing the active bundle version, although design decision 1 says no delete of the active version; the spec is silent on the refusal and its status code. Resolution: answered: Added bundle requirement that refuses delete of the active version with 409.
- R1.6 (failure mode): Concurrent saves and rotation: two administrators saving a bundle at once could both append a version or move the active pointer; no requirement covers the version ordering, conflict answer or If-Match style concurrency that ADR 210 style requires. Resolution: answered: Added bundle requirement for ETag and If-Match conflict handling on concurrent saves.
- R1.7 (missing scenario): The spec for refusal gives only a 400 for expired CA or malformed PEM; the own-issuer refusal, the SPIFFE ID shape refusal and the cross-namespace refusal (403 in design) have different codes that are not written in the spec, and the error naming the bad CA index required by Usability is not a requirement. Resolution: answered: Added status codes per refusal and a requirement that the bad CA index appears in the error.
- R1.8 (failure mode): Rotation overlap is accepted by design (two CAs at once) but no requirement bounds the overlap window or its end; a stale CA can stay trusted indefinitely unless the docs or a setting state the end. Resolution: answered: Added requirement that bounds the rotation overlap window and states its end in the read.
- R1.9 (open question): ADR 227 decision 2 (owner) defines fresh authentication with freshAuthSeconds; the rule spec does not say which step counts as fresh for a bundle save, and the bundle spec has no fresh-authentication requirement even though design layer 2 requires it for bundle writes. Resolution: answered: Added bundle fresh-authentication requirement with the one window.
- R1.10 (wrong claim): The ADR check quotes ADR 54 decisions 5 and 8 as current rules, but the ADR index marks them changed by ADR 70; the change does not record the ADR 70 text it relies on, so the claim that no conflict exists is unsupported. Resolution: answered: ADR check no longer quotes changed ADR 54 decisions 5 and 8 as current; it quotes ADR 54 decision 4 and ADR 284 plus ADR 70 decision 3.
- R1.11 (scope): The tasks and the coverage evidence list rule API endpoints including DELETE, while the proposal's user path has no delete step and the spec has no delete scenario, so the rule delete path is unspecified in the spec yet named in tasks. Resolution: answered: Added delete step to the user path and a delete scenario to the rule spec.

## Open questions

none

Questions considered and answered:

- Where does the bundle and each rule live? Yes through ADR 54 decision 4 plus ADR 216: bundle belongs to one namespace, fence carries namespace and failover version. Rule namespace scope follows ADR 284 decisions 1 and 3; the changed wording of ADR 54 decisions 5 and 8 is not quoted because the pack gives no replacement text.
- Does each rule operation need a REST API, an action, and an audit entry? Yes through ADR 284 decisions 1 and 3 plus ADR 216: REST API under the namespace path with a namespace action per operation and an audit entry per change; endpoint list is in the coverage note.
- Does a rule or bundle change need fresh authentication? Yes through ADR 227 decision 2: one freshAuthSeconds window, default 900, for bundle saves and rule creates, updates, and deletes; no new window.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
