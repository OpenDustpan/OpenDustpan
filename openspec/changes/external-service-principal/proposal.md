Decision: ADR 369 decisions 3 to 5. Milestone 8 follow-up.

## Why

**Persona: site administrator.** An organization runs its CI with non-human accounts of its own identity system: service accounts of GitLab, or system accounts in LDAP. Today OpenDustpan can only make a service principal through its own API, so the administrator copies each account by hand, and a token of such an account can look like a person. The administrator needs each such account to be a service principal of OpenDustpan, keyed by its stable external ID, with grants and an owner, that never gets a console session. The organization may have no LDAP group or OU to key on, so a name pattern such as `system-*` must also work, without granting anything.

The user path this change must make work:

1. **API.** The administrator previews a classification rule: a glob `system-*` on the external username of the identity provider of the installation. `POST /api/dustpan/v1/service-classification-rules/preview` answers the 3 existing principals that the rule would make service principals, their count and the digest of the set. Nothing changes.
2. **API.** The administrator creates the rule with `existingPrincipals: skip`. The 3 existing matches stay as they are and the answer lists them. A new account `system-deploy` that signs in through the CI token exchange becomes a principal of kind `service`, keyed by its external ID.
3. **API.** The administrator creates a second rule with `existingPrincipals: confirm` and the digest of its preview. The server makes the matching principals service principals and writes one audit entry with the set and its count.
4. **CLI and provider.** A team manages the rule as code. `dustpan` and the providers send `existingPrincipals: skip` or `apply` from the configuration. A plan of the provider calls the preview, shows the principals, and sends `confirm` with the digest at the apply.
5. **API.** The administrator grants a role to the service principal and names its owner. The access review lists the principal, its owner, its classification and the rule that made it.
6. **Console.** The rule form shows the preview before the save. A service principal never gets a console session: its sign-in is refused with the reason.
7. **Docs page.** `docs/guides/external-service-principals.md` gives the four values of `existingPrincipals`, the glob rules (exact bytes, `*` alone refused) and one example for each source.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The rule would classify existing principals again and `existingPrincipals` is `refuse` or missing. | 409 `principal.reclassify-refused`, with the count and the preview route. Nothing changes. | `POST /api/dustpan/v1/service-classification-rules/preview` |
| The set changed after the preview. | 409 `principal.reclassify-stale`, with the new count. Nothing changes. Preview again. | `POST /api/dustpan/v1/service-classification-rules/preview` |
| A glob of `*` alone, or a glob that is not valid. | 400 `principal.classification-invalid`, with the field. | `dustpan service-classification-rule create --help` |
| An unknown value of `existingPrincipals`. | 400 `principal.classification-invalid`, with the four values. | `dustpan service-classification-rule create --help` |
| A service principal tries to sign in to the console. | 403 `session.service-principal`. | `GET /api/dustpan/v1/principals/{principal}` |
| The account is no longer active in its source at an exchange. | 401 `trustrule.account-inactive`. The exchange issues no token. | `GET /api/dustpan/v1/principals/{principal}` |

Evidence for the claims above:

- Only the API makes a principal of kind service, with the source `local`. Proof: `pkg/principal/namespace.go:47` sets `Kind: KindService, Source: SourceLocal`; `rg -n 'KindService' pkg/principal --type go` gives no other place that sets the kind from a source.
- No external ID apart from the subject, and no owner, on a principal record. Proof: the fields of `pkg/principal/principal.go:158` (`Principal`).
- No classification rule exists. Proof: `rg -n -i 'classif' pkg internal --type go` gives no type of a rule.

## What Changes

- NEW stored classification rules of the installation and of a namespace. A rule has one kind: a condition on a claim of the token, an LDAP group or OU, or a glob on the external username. Routes: `/api/dustpan/v1/service-classification-rules` for the installation and `/api/dustpan/v1/namespaces/{namespace}/service-classification-rules` for a namespace: list, create, update, delete, and `preview`.
- NEW field `existingPrincipals` on create and update: `refuse` (the default), `skip`, `confirm` with `expectedDigest`, `apply` (ADR 369 decision 5).
- NEW fields on the principal record: the external ID and its source, the classification with the ID of the rule that made it, and the owner. The API and the access review show them.
- A principal of kind `service` from an external source: keyed by its external ID, never by its name. It gets no console session, no MFA and no break-glass.
- Each token exchange checks that the external account is still active in its source.
- A rename into a glob makes the principal a service principal, with an audit entry. A rename out of it changes nothing. Only an administrator makes a service principal a person again, with an audit entry.
- NEW, from the check of main: the trust rule exchange gives the kind of actor of ADR 369 decision 3. A token of an automated source (a schedule, an API trigger, a pipeline that another pipeline started, a token with no user) never acts as a person: a hard check in the exchange.
- A docs page.

Out of scope:

- A trust rule in person mode and its action (ADR 369, "Not decided here").
- The token claims in the access facts (concurrent-applies-merge-core).
- SCIM. The same rule kinds can apply to it later.

Depends on:

- ADR 369 on main (pull request #3056)

## Usability

Where the path still costs the person time or surprises them:

- A glob that matches a person by mistake makes that person a service principal, who then cannot sign in to the console. This is the safe direction. The preview shows the person before the save, and the error of the sign-in names the principal route.
- A rename of an account out of the pattern does not make it a person again. The docs page says so.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The kinds of a principal record, with `service`. (`pkg/principal/principal.go:89 (KindService)`)
- The sources of a principal record: `local`, `ldap`, `scim`, `oidc`, `oauth`. (`pkg/principal/principal.go:106 (Source)`)
- The principal record, with no external ID and no owner. (`pkg/principal/principal.go:158 (Principal)`)
- The API makes a service principal of a namespace with the source `local`. (`pkg/principal/namespace.go:47`)
- A trust rule names one principal. (`pkg/trustrule/trustrule.go:155 (Rule)`)
- A trust rule matches string claims exactly. (`pkg/trustrule/jwt.go:508 (Rule.Matches)`)
- The exchange of a token. (`internal/trustrulesvc/exchange.go:174 (Exchanger.Exchange)`)
- The request checks the kind of the principal and the credential. (`pkg/identity/identity.go:134 (Principal.ActsAsPerson)`)
- LDAP gives `entryUUID` and `objectGUID` as text. (`internal/ldapauth/subject.go:24 (GUIDText)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 369: The actor of a job: trust rules, conditions on token claims, and external service principals** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current), 4 (current), 5 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "It is keyed by its stable external ID (for example the GitLab user ID, or the LDAP `entryUUID` or `objectGUID`), never by its name (ADR 362 decision 1)."
- Quote: "A glob only classifies. It never grants."
- Quote: "A rename that leaves the pattern does not make a service principal a person again."
- Quote: "`refuse`: the default."
- Quote: "A schedule that a person owns never takes the identity or the rights of that person. This is a hard check in code."
- Effect: This change builds decisions 3 (items 2 and 3), 4 and 5.
- Conflict: none.

**ADR 54: Workload identity: OIDC and JWT token exchange, and mTLS** (not owner-tagged; its context quotes the owner on workload identity).

- Decisions that apply: 5 (changed), 6 (changed).
- Changed by: ADR 69 extends; ADR 70 changes decisions 5 and 8; ADR 204 changes decision 3; ADR 222 extends decision 5; ADR 223 extends decision 5; ADR 284 changes decision 3; ADR 369 changes decision 6. Chain to the end: ADR 69, ADR 70, ADR 204, ADR 222, ADR 223, ADR 284, ADR 369.
- Current rule: this ADR, with ADR 69, 70, 204, 222, 223, 284, 369.
- Quote: "A trust rule belongs to one namespace."
- Quote: "The principal is not a person."
- Effect: The exchange keeps the rules of decision 5. It gives the kind of actor of ADR 369 decision 3 in place of decision 6 alone.
- Conflict: none.

**ADR 67: Who creates and administers a service principal** (not owner-tagged).

- Decisions that apply: 1 (extended), 6 (changed), 7 (changed).
- Changed by: ADR 70 changes decisions 2, 3, 4 and 5; ADR 70 extends decision 6; ADR 210 changes; ADR 215 changes decision 7; ADR 369 extends decisions 1 and 6. Chain to the end: ADR 70, ADR 210, ADR 215, ADR 369.
- Current rule: this ADR, with ADR 70, 210, 215, 369.
- Quote: "Making a service principal of the installation is an administration action."
- Quote: "The access to a service principal is visible."
- Effect: A classification rule of the installation needs `server:manage-principals`. A rule of a namespace needs `namespace:manage-principals`. The access review lists each external service principal with its owner and its rule.
- Conflict: none.

**ADR 362: Owner decisions of 8 November 2026** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current).
- Changed by: ADR 363 extends decision 2. Chain to the end: ADR 363.
- Current rule: this ADR, with ADR 363.
- Quote: "Every stored reference to an entity uses the stable ID of the entity when a user can rename the entity, or delete and recreate it."
- Effect: The principal is keyed by the external ID of its source. The name is only matched by the glob and shown to people.
- Conflict: none.

**ADR 270: Manage OpenDustpan as code with a CLI and two providers** (owner-tagged: decisions 1 to 3).

- Decisions that apply: 6 (current), 8 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A create never adopts an existing object."
- Quote: "Every update and delete sends `If-Match`. The server does not reconcile."
- Effect: The rule routes take `If-Match`. The value of `existingPrincipals` stays the same between two runs, so a provider stays idempotent.
- Conflict: none.

**ADR 250: Defense in depth for security code** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Each layer must stop the attack alone, without help from the others."
- Effect: The rule store, the classification service, the exchange and the sign-in each refuse alone, each with its own hostile test.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Which action creates a classification rule? The owner named `server:manage`, which the action list does not have. ADR 67 decisions 1 and 7 give the actions for principals: `server:manage-principals` for a rule of the installation and `namespace:manage-principals` for a rule of a namespace. Decided by the PM under the decision rule on 14 November 2026, recorded in ADR 369 "Not decided here" for the owner's review.
- Is a glob of `*` alone refused or warned? Refused, as ADR 369 proposes. A refusal fails toward restriction. The owner can turn it into a warning.
- How does a glob compare names? Exact bytes: no case folding and no Unicode normalization (ADR 369 decision 4). `System-x` does not match `system-*`. A lookalike character does not match.
- How is the digest of a preview made? SHA-256 over the sorted principal IDs of the set, one per line. The IDs are stable, so the digest does not change with a rename.
- Does `apply` need its own permission, as the override of ADR 309 decision 3 does? No. `apply` is a value of the rule write, and the rule write already needs the administration action. The audit entry records the set.
- How does an exchange check that an account is active? For GitLab, through the user API of the instance with the connection of the installation; for LDAP, through a read of the entry by its `entryUUID` or `objectGUID`. An answer that the server cannot read is inactive (fail closed).
- Does a service principal from an external source get a token with no exchange? No. It acts only through a token exchange or a job token.
- Who is the owner of an external service principal? A person principal that the administrator names at the grant or later. The access review shows a principal with no owner.

Drafted by the PM from ADR 369.
