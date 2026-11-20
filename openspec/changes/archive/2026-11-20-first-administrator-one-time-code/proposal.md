---
version: 1
---

Issue: #3574. This change builds ADR 339 (decisions 1 to 8, owner, 2026-11-07).

## Why

**Persona: operator of a new production installation.** The operator runs the container or the Helm chart. There is no shell on the server. The operator holds the bootstrap admin token and signs in to the console as a local user, or as a person of an OpenID or OAuth provider. The operator tries to make the first person who holds a role. Today this cannot be done. `POST /api/server/first-administrator` does not exist, and only `opendustpan dev` gives an administrator session.

The path after this change:

1. The operator makes the person: a local user (`POST /api/dustpan/v1/local-users`, bootstrap admin token) or a person of a provider who signs in once. The person has no role.
2. The operator reads `GET /api/server/first-administrator` with the bootstrap admin token. It answers `{"made": false, "offer": null}`.
3. The operator sends `POST /api/server/first-administrator` with `{"principal": "<principal ID>", "reason": "<text>"}` and the bootstrap admin token. The answer is `201` with the one-time code, its expiry (10 minutes) and the principal. The answer is the only place that holds the code.
4. The person signs in to the console (password sign-in, or the provider) and sends `POST /api/server/first-administrator/redeem` with `{"code": "<code>"}` in that console session. The sign-in must be fresh.
5. One transaction makes the `site-administrator` grant on the installation, spends the code and stores the marker. The answer is `200` with the grant ID. The person can now do every action that needs a fresh step.
6. `GET /api/server/first-administrator` now answers `{"made": true, ...}` with the person, the grant, the sign-in ID and the time. A new `POST` answers `409` NEW error code `first-administrator.already-made`.

Errors the person may hit, and the one command that helps:

- The code is wrong, spent, expired or for another person: `400` NEW error code `first-administrator.code-invalid`. One text covers the four cases. The text says to ask the operator for a new code with `POST /api/server/first-administrator`.
- The sign-in is too old, or the request has an access token: `403` `mfa.fresh-step-required` with the path of a new sign-in (the existing code and text of the console).
- The marker is set: `409` NEW error code `first-administrator.already-made`. The text says that recovery is `opendustpan host create-temp-admin` (host recovery guide).
- The person holds the `site-administrator` role already: `409` NEW error code `first-administrator.grant-exists`. The code is not spent and no marker is stored. The text says that the person holds the role already.
- The principal is not a live, enabled person, or is a break-glass person, a temporary administrator, a service or a `dev:` subject: `400` NEW error code `first-administrator.principal-invalid`. The text names the rule that failed.
- The reason is empty, longer than 500 characters or holds a control or format character: `400` NEW error code `first-administrator.reason-invalid`.

The change also gives the three guides of ADR 339 decision 8 the true text: console sign-in (the sentence "Give the first roles with the bootstrap admin token, through the grants API" is wrong on main), local users (a section "The first administrator") and workload identity (step 1 needs a console session).

## Evidence

- The route, the code, the marker and the change do not exist on main. Proof, run on main (8c76edc53, 19 Nov 2026):
  `rg -n -i "first-administrator|first_administrator|FirstAdministrator" --glob '!openspec/**' --glob '!docs/adr/**' --glob '!docs/rfc/**' .` prints nothing.
  a search of the guides for the words "first administrator" prints one line, one line in a guide about the Helm chart, which names the bootstrap admin tokens only.
- The bootstrap admin token cannot make a grant. `requireFresh` answers `errFreshStepRequired` to each request that has no console session ((read only) `internal/api/server/freshness.go`, line 112, `:67`). An access token never gives a fresh authentication.
- A person who signs in has no role. The sign-in guide says "Give the first roles with the bootstrap admin token, through the grants API" ((read only) `docs/guides/console-sign-in.md`, line 206). The workload identity guide shows the same `403` in its step 1 ((read only) `docs/guides/workload-identity.md`, line 90).
- Only `dev` has the path. `devCodeSignIn` signs in the person `dev:admin`, and `grantDevAdministrator` gives the role with the bootstrap administrator as the granter ((read only) `internal/api/server/devsignin.go`, line 72, `:156`). `devSignInRoutes` mounts the route only for the kind dev.
- The pieces for a safe build exist. The store of a grant writes the grant and its audit entry in one transaction ((read only) `pkg/store/sqlite/access.go`, line 28, `inGrantTx` at `:1145`; (read only) `pkg/store/postgres/access.go`, line 30). A one-use code with a stored digest exists for the set-up code (`local_user_resets`, `digest` with `CHECK (length(digest) = 64 ...)` in (read only) `pkg/store/sqlite/migrations/0168_baseline.sql`, line 1483). The bootstrap administrator is `identity.StaticToken` ((read only) `pkg/identity/identity.go`, line 1304), and an action that no family classifies is allowed to `Admin` only ((read only) `pkg/access/authorizer.go`, line 123, (read only) `pkg/access/access.go`, line 112).

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- `FamilyOf` answers false for an action that no family holds, and the authorizer then allows that action to `Admin` only (`pkg/access/access.go:238 (func FamilyOf)`)
- `StaticToken` authenticates the one bootstrap administrator with no kind, no token ID and `Admin` set (`pkg/identity/identity.go:1304 (type StaticToken)`)
- `requireFresh` answers `errFreshStepRequired` to a request with no console session, and `checkTokenFreshness` compares the sign-in time with the window of the session policy and fails closed: (read only) `internal/api/server/freshness.go`, lines 112 and 146 (func requireFresh, func checkTokenFreshness)
- `Fresh` compares the sign-in time with a window: (read only) `internal/sessionauth/sessionauth.go`, line 313 (func Fresh)
- `devCodeSignIn` redeems a one-time code, spends the failure budget of the address on a wrong code and answers one text for a wrong, spent or old code: (read only) `internal/api/server/devsignin.go`, line 72 (func devCodeSignIn)
- `Service.Grant` gives a role to a subject, and the store checks the role and the actions of the granter in the transaction of the write: (read only) `pkg/access/service.go`, line 354 (func Grant)
- The authorizer allows `Admin` every action: (read only) `pkg/access/authorizer.go`, line 123 (func Authorize)
- `ValidateReason` of the audit package refuses a reason that is too long, not UTF-8 or hidden-character text: (read only) `pkg/audit/audit.go`, line 193 (func ValidateReason)
- `ValidateReason` of the temporary administrator also refuses a blank reason: (read only) `pkg/tempadmin/tempadmin.go`, line 130 (func ValidateReason)
- `inAuditedTx` writes a change and its audit entry in one transaction in the scope of the installation: (read only) `pkg/store/sqlite/audit.go`, line 85 (func inAuditedTx)

## Fix boundary

Decisions 1 to 8 of ADR 339 are the requirements. This change adds:

- A NEW domain package, add the new file pkg/firstadmin/firstadmin.go: the port `Store`, the code, its digest, the pure checks of the redeem and of the principal, the errors, and a NEW conformance suite, add the new file pkg/firstadmin/firstadmintest/suite.go.
- NEW tables `first_administrator` (the marker, one row, in a backup) and `first_administrator_codes` (the one code, one row, not in a backup) in the baseline of each dialect. The NEW queries are in pkg/store/sqlite/queries.sql and pkg/store/postgres/queries.sql. The NEW adapters are add the new file pkg/store/sqlite/firstadmin.go and add the new file pkg/store/postgres/firstadmin.go.
- A NEW service, add the new file internal/firstadminsvc/service.go, with `Offer`, `Redeem` and `State`.
- Three NEW routes in add the new file internal/api/server/firstadmin.go: NEW route `GET /api/server/first-administrator`, NEW route `POST /api/server/first-administrator` and NEW route `POST /api/server/first-administrator/redeem`. The API description and the generated clients are regenerated.
- Three new actions in `pkg/identity/identity.go`: `server:offer-first-administrator` (no family: the bootstrap administrator only), `server:read-first-administrator` (operator family) and `caller:redeem-first-administrator` (self family).
- NEW error codes in add the new file internal/errcode/catalog_firstadmin.go, and the page of the error codes.
- The backup: the marker table travels with the data, the code table is emptied by a restore.
- The code rule in `docs/reference/code-rules.md`, the page `docs/guides/first-administrator.md` and the three guides of decision 8.
- An e2e step against the real binary.

Not decided here, behavior stays as today. RFC 117 and ADR 339 name four open questions. This change does not decide them. Each one keeps today's behavior, and none is a question of this change:

1. **A second factor beyond the fresh authentication.** The redeem needs the fresh authentication of ADR 339 decision 6 and nothing more. No second factor is asked.
2. **The two break-glass accounts of the first setup (ADR 70 decision 24).** The redeem does not ask for them and does not check for them. No prompt is added.
3. **Whether the redeem ends the bootstrap admin token (ADR 53 decision 9).** The bootstrap admin token stays valid after the redeem. No refusal after the marker is added.
4. **A marker for an installation that has a site administrator before this change.** No tag shipped, so no installation of a shipped tag exists. No upgrade code and no marker migration is written. An installation on a development build that already has an administrator has no marker. Its first call of the route is the same as for a new installation.

Out of scope:

- The console screen for the redeem. It is a change of the repository `OpenDustpan/console`. This change gives the API, and the guide shows the redeem with the console session of the password sign-in. The console already holds the session and the CSRF value.
- A CLI command. Every client uses the REST API (the rule of the repository). A command can come later with no change of the API.
- The setting `freshAuthEnabled`. ADR 385 decision 5 keeps the code of the first administrator out of the setting: the freshness check of the redeem does not ask the setting (see design, decision 6).
- A change of an `opendustpan dev` installation. `dev` keeps its own grant path and sets no marker.
- A change of the grant API, the roles or the recovery commands. A loss of every administrator after the marker goes through recovery (ADR 57, ADR 245).

## Files

New (each path below is NEW on main):

- add the new file pkg/firstadmin/firstadmin.go, the new file pkg/firstadmin/firstadmin_test.go and the new file pkg/firstadmin/leak_test.go: the port, the code, the pure checks.
- add the new file pkg/firstadmin/firstadmintest/suite.go: the conformance suite of the port.
- add the new file internal/firstadminsvc/service.go, the new file internal/firstadminsvc/service_test.go and the new file internal/firstadminsvc/leak_test.go: the service.
- add the new file pkg/store/sqlite/firstadmin.go, the new file pkg/store/sqlite/firstadmin_test.go, the new file pkg/store/postgres/firstadmin.go and the new file pkg/store/postgres/firstadmin_test.go: the adapters and their tests.
- add the new file internal/api/server/firstadmin.go and the new file internal/api/server/firstadmin_test.go: the three routes.
- add the new file internal/errcode/catalog_firstadmin.go: the error codes.
- add the new file pkg/backup/portable_first_administrator.go: the portable description of the marker table.
- add the new file pkg/dustpan/firstadmin.go: the wiring.
- add the new file e2e/first_administrator_test.go: the e2e step.
- add the new file docs/guides/first-administrator.md: the docs page.

Edited:

- pkg/store/sqlite/migrations/0168_baseline.sql, pkg/store/postgres/migrations/0168_baseline.sql: the two tables.
- pkg/store/sqlite/queries.sql, pkg/store/postgres/queries.sql: the queries; the generated query code of each dialect is regenerated.
- pkg/identity/identity.go, pkg/access/access.go, pkg/access/access_test.go: the three actions, the family of two of them and their test.
- internal/api/server/server.go, internal/api/server/routes_test.go, internal/api/server/operations.go: the Config field, the route table, the route tests and the join point of the labels.
- pkg/backup/backup.go, pkg/backup/coverage.go, pkg/backup/devstrip_coverage_test.go: the table lists.
- e2e/coverage_server.go: the route coverage of the e2e suite.
- .golangci.yml: the new packages join the domain rule and the test helper paths. No rule is weakened.
- internal/api/apidesc/openapi.json, clients/typescript: regenerated by their generators.
- docs/guides/console-sign-in.md, docs/guides/local-users.md, docs/guides/workload-identity.md: decision 8.
- docs/reference/code-rules.md, docs/reference/tool-neutral-error-codes.md: the code rule and the error codes.

## Check

- Test the redeem makes the grant, spends the code and stores the marker in one transaction, and a failed audit write leaves the three unchanged: TestRedeemOneTransaction in pkg/firstadmin/firstadmintest fails on main (the package does not exist)
- Test the marker closes the request route: TestOfferRefusedAfterMarker fails on main
- Test each hostile row of the three layers: the tables TestStoreHostileRows, TestServiceHostileRows and TestHandlerHostileRows fail on main
- Test the code is never logged, never in an audit entry and never stored: TestCodeNeverLeaks fails on main
- Test the route table has the three routes with their actions: TestRoutesDeclareActions and TestRoutesAuthorizeDeclaredObject fail on main for the new routes
- Test the backup holds the marker and not the code: TestBackupMarkerNotCode in pkg/backup fails on main
- Docs: the three guides and the new page run as written: make docs-lint and the docs test of the e2e step
- E2E step against the real binary: request with the bootstrap admin token, redeem in a console session with a fresh sign-in, `409` after the marker, the grant listed, the audit entries: make e2e

## ADR check

Each quote is copied from the file in `docs/adr/`. The "Changed by" chains come from `docs/adr/INDEX.md`.

**ADR 339: The first administrator gets a one-time code from the bootstrap token** (owner-tagged: decisions 1 to 8).

- Decisions that apply: 1 to 8. This change builds them.
- Changed by: none yet (INDEX row 307: "extends ADR 6; extends ADR 227 decision 6", no later ADR). Current rule: this ADR.
- Quote, decision 2: "The route works only while the marker "first administrator made" is not stored. After the marker, it answers `409` with its own error code."
- Quote, decision 3: "One transaction makes the `site-administrator` grant on the installation, spends the code and stores the marker. It writes an audit entry. The entry names the bootstrap administrator as the creator, and the person and the sign-in ID as the one who took the role."
- Quote, decision 4: "The code is 32 bytes from the system random source. It goes in a `POST` body only. The server never logs it, stores a hash only and compares in constant time. One code is valid at a time, for 10 minutes, for one use, for one role (`site-administrator`) on the installation only."
- Quote, decision 5: "It refuses a break-glass person, a temporary administrator, a service and a `dev:` subject."
- Quote, decision 6: "The redeem checks three facts in the store transaction. The marker is not set. The code names the principal of the session. The authentication of the session is fresh. An unknown state, a set marker or a failed audit write refuses the request. The refusal writes an audit entry."
- Quote, decision 7: "The marker is an installation fact with a read in the API. A later loss of every administrator goes through recovery (ADR 57, ADR 245), not through this route."
- Quote, decision 8: "Three guides change in the pull request that builds the route. They are console sign-in, local users and workload identity."
- Open items of the ADR: "This ADR does not decide the open questions 2 to 5 of RFC 117 beyond decision 3." They are the four non-goals of "Fix boundary". Conflict: none.

**ADR 385: A fresh step is opt-in** (owner-tagged: decisions 1, 2, 3, 5, 8).

- Decision that applies: 5. Changed by: none yet. Current rule: this ADR.
- Quote: "the one-time code of the first administrator (ADR 339 decision 6). It is not MFA and not a step-up. It is out of the scope of the setting (owner, 2026-11-15)".
- Effect: the freshness check of the redeem never asks `freshAuthEnabled`. With the setting off, the redeem still needs a fresh authentication of the session. Conflict: none.

**ADR 227: One fresh-authentication window** (owner-tagged: decisions 2, 3).

- Decisions that apply: 1 ("A fresh authentication is a built-in MFA step (ADR 46) or a new authentication at the SSO identity provider (ADR 71). It is fresh when its age is not above the window."), 3 ("A window of 0 asks for a fresh authentication at each step.") and 6 ("A step with no fresh authentication answers `403` with the error code `mfa.fresh-step-required` and changes nothing (ADR 212 decision 3). [...] An access token never gives a fresh authentication.").
- Changed by: ADR 301, ADR 320, ADR 339, ADR 354, ADR 385. Current rule: ADR 227 with ADR 301, 320, 339, 354, 385.
- Effect: the redeem uses the same check as the token changes of main (`checkTokenFreshness`). A request with an access token gets `403` `mfa.fresh-step-required`. Conflict: none.

**ADR 6: Identity: OpenDustpan issues tokens; SSO only signs humans in.**

- Quote: "The static token is for first boot." and "The bootstrap adapter is a single static admin token from `DUSTPAN_ADMIN_TOKEN` (environment only, never a flag, at least 32 bytes, compared in constant time)."
- Changed by: ADR 17, ADR 52, ADR 53, ADR 237, ADR 339. Current rule: this ADR, with ADR 17, 52, 53, 237, 339.
- Effect: the bootstrap admin token makes the offer, and no other credential does. Conflict: none.

**ADR 250: Defense in depth for security code** (owner-tagged).

- Quote: "Never only one layer. Every security-relevant path has at least two independent layers. Each layer must stop the attack alone, without help from the others."
- Effect: the layers and tests are in "Layers" of the design and in the tasks. Conflict: none.

**ADR 362: Owner decisions of 8 November 2026** (owner-tagged).

- Quote: "Every stored reference to an entity uses the stable ID of the entity when a user can rename the entity, or delete and recreate it. A name is a label for people only."
- Effect: the code row, the marker and the grant name the principal by its ID (`prn-...`), never by its subject. The same rule covers the store order and the baseline edit in place of the same ADR. Conflict: none.

**ADR 70 decision 24** (owner-tagged in its text: "A guard keeps two working paths. The first setup cannot finish without two break-glass accounts."). The change does not decide how the first setup asks for them (non-goal 2). It adds no check that depends on them. Conflict: none.

**ADR 53 decision 9** ("Retire the bootstrap admin token. On the first sign-in with the bootstrap admin token (ADR 6), the console and the API ask the operator to make a personal admin token and to retire the bootstrap token."). The change does not end the token (non-goal 3). Conflict: none.

**ADR 57** and **ADR 245** (recovery): the route is closed for good after the marker, and recovery stays the path for a later loss of every administrator (ADR 339 decision 7). The change adds no recovery path. Conflict: none.

## Open questions

none

Questions considered and answered:

- Which route path does the redeem use? ADR 339 decision 3 says only "a `POST` in a console session". The change takes `POST /api/server/first-administrator/redeem`: the same resource, the same prefix as the request, so a person finds both next to each other in the API description. A path is a name, not a rule of security.
- What happens to an older code when the operator asks for a new one? "One code is valid at a time" (decision 4) is built as one row in `first_administrator_codes`. A new offer replaces the row and writes an audit entry that says it replaced a code. The replaced code no longer works. The reason: an operator who lost the code must not wait 10 minutes, and the holder of the bootstrap admin token can make a new offer anyway. The replace is not a new power.
- Why is the marker in its own table, and in a backup? The marker is a fact about the data (the grants). A restore into another installation (backups are portable only, ADR 362) must carry it with the grants. If the marker stayed outside the backup, the restored installation would have the grant and no marker, and the route would open again. The table `installation` is a control table that a restore keeps live, so it cannot hold the marker. The code table holds a secret digest for a short time, so a backup leaves it out and a restore empties it.
- Does the route need the idempotency key? No. A stored replay record would hold the answer with the code. The three routes do not use `Idempotency-Key`, and a test checks it.
- What if the person already holds the `site-administrator` role when the redeem runs? The transaction answers `already exists` from the grant store and changes nothing. The route answers `409` `first-administrator.grant-exists` with the text "the person holds the role already". The marker stays unset and the code stays until it expires. The person still has no marker-backed record, so the operator sends a new offer for another person, or the person makes the next grants alone.
- Does a dev installation get the route? The route table mounts the three routes for each installation kind. On a dev installation the person is `dev:admin`, which decision 5 refuses, so no offer can name it. `dev` sets no marker.
- What if the window of the session policy is 0? The check refuses each sign-in, as for every step (ADR 227 decision 3). The window can be set to 0 only after the first administrator exists, because a change of the window needs a fresh step. A restore of a backup that holds window 0 on an installation with no marker is the one way to reach it, and the answer is then the same `403` with the path of a new sign-in.
- Does the answer with the code need a header against caching? Yes: `Cache-Control: no-store` (decision 4: the code goes in a `POST` body only, and the answer is not a stored resource). A test checks it.
- Is a failed offer or redeem recorded? Yes. Each refusal writes an audit entry with the outcome `failure` and the reason (decision 6). The entry never holds the code or its digest.
