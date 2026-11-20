---
version: 9
---

Issue: #3373.

Roadmap: `docs/roadmap.md:183` (IP allowlists).

## Why

**Persona: site administrator.** A site administrator shares one installation between people and CI pipelines. The administrator needs to limit console sign-in and CLI access by network address. The installation and each namespace opt in on their own. The own list of an access token is the change `ip-allowlists-token-lists`.

The user path this change must make work:

1. **API.** Turn the feature on and set the installation list with `PATCH /api/dustpan/v1/settings`.
2. **API.** Opt in a namespace with `PATCH /api/dustpan/v1/namespaces/{namespace}/settings` (view: `GET`, `PATCH` on `/api/dustpan/v1/namespaces/{namespace}/settings/ip-allowlist`).
3. **Console.** Sign in from an allowed address and from a denied address. Read the 403 reason.
4. **CLI.** Run the Pulumi CLI and OpenTofu with an access token from an allowed address and from a denied address.
5. **Docs page.** Follow docs/guides/ip-allowlists.md to enable, opt in, deny and allow.
6. **E2e.** Run the allowlist e2e suite over TLS against the real binary.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Console sign-in or token request from an address outside an opted-in list. | 403 with error code access.ip-not-allowed that names the scope that refused. | `GET /api/dustpan/v1/settings` |
| Save of a list with an entry that is not a valid CIDR or address. | 400 with the NEW error code ip-allowlist.invalid-cidr that names the bad entry. | `GET /api/dustpan/v1/settings` |
| Save of a list that is longer than the stored value can hold. | 400 that names the limit that failed: 64 entries or 4096 bytes. | `GET /api/dustpan/v1/settings` |
| Refresh of a console session from an address outside an opted-in list. | 403 with error code access.ip-not-allowed. The refresh cookie does not renew the session. | `GET /api/dustpan/v1/settings` |

Evidence for the claims above:

- An e2e file for IP allowlists exists. Proof: e2e/ipallow_test.go
- An e2e file for principals names IP allowlists. Proof: e2e/principals_test.go
- A guide for IP allowlists exists. Proof: docs/guides/ip-allowlists.md

## What Changes

- Done on main (see "Done on main"): the installation flag and list keys in the generic settings API with CIDR checks, the error code, the installation check before the credential, the denial audit entry, the docs page and the e2e step.
- Open in this change: the namespace list as a key of the namespace settings resource, with its NEW view `/api/dustpan/v1/namespaces/{namespace}/settings/ip-allowlist` (a new route). ADR 55 decision 8 says each namespace opts in on its own, and main has no such key (see "What main lacks").
- Open in this change: one switch. The setting `ipAllowlist.enabled` alone turns the namespace and token lists on. The option `--ip-allowlists` stays as a static lock (owner decision 9).
- Open in this change: the actions of the namespace list. The settings actions plus `namespace:manage-ip-allowlist` govern it (owner decision 10).
- Open in this change: a byte limit of 4096 for the list keys (owner decision 11).
- Open in this change: a scenario and a test that a refresh of a console session passes the list check.
- Enforcement on console sign-in and on requests with an access token gives 403 and an audit entry. A denial names the scope (installation or namespace) and never the name of a namespace.
- The health probe paths `/health/live`, `/health/ready` and `/health/ready/<component>` stay outside the installation list and do not authenticate a credential.
- The docs page covers enable, opt in, deny and allow.

Out of scope:

- New sign-in methods
- The own list of an access token: who sets it, its route, its store and its enforcement. The change `ip-allowlists-token-lists` holds them. It depends on this change.
- Token lifetime and expiry email
- mTLS client certificate handling

Depends on:

- add-one-generic-settings-api

## Usability

Where the path still costs the person time or surprises them:

- An administrator finds the refusing scope from the 403 body without reading logs.
- The docs page tells an administrator to set a namespace list in the console, and says that a token never gives a fresh sign-in. The page says that the setting alone turns the lists on.


## Done on main

Tasks 1.1 to 4.2 of version 7 are on main. The builder lane that checked them reported 8 guard mutants killed: the installation step dropped, a bad stored list opens, a failed read opens, the limit 64 changed to 65, `Contains` always true, an IPv4-mapped address not unmapped, an unavailable decision opens, and a probe check that uses the decoded path only. The lane report is not in the repository, so task 0.1 runs each mutant again. The tests below are on main at the head of this branch.

| Done task | Evidence on main |
| --- | --- |
| 1.1 denied sign-in and denied token | `TestDenyConsoleSignInOutsideList` (`pkg/dustpan/ipallow_deny_test.go:19`), `TestDenyTokenOutsideList` (`pkg/dustpan/ipallow_deny_test.go:32`) |
| 1.2 invalid CIDR on `PATCH` | `TestInvalidCidrRefused` (`pkg/dustpan/ipallow_cidr_test.go:16`), `TestTooManyCidrsRefused` (`pkg/dustpan/ipallow_cidr_test.go:65`) |
| 1.3 spoofed proxy header | `TestRecordedAddressIgnoresUntrustedHeader` (`pkg/dustpan/ipallow_proxy_test.go:18`) |
| 1.4 any list denies | `TestDenyAnyListDenies` (`pkg/dustpan/ipallow_deny_internal_test.go:44`). It drives the checker with a namespace list that no production caller passes (see "What main lacks") |
| 2.0 error code | `internal/errcode/catalog_ipallowlist.go`, `internal/errcode/testdata/catalog/ip-allowlist.txt`, `TestParseListMaxEntries` (`pkg/ipallow/list_test.go:117`) |
| 2.1 flag and list keys | `pkg/ipallow/settings.go:13,16`, `TestInstallationListReducesSecurity` (`pkg/ipallow/settings_test.go:13`) |
| 2.2 checker on the request path | `admit` (`pkg/dustpan/ipallow.go:470`), `TestAllowlistGateFailsClosed` (`pkg/dustpan/ipallow_deny_internal_test.go:223`), `TestAdmitAnswersUnavailableForAStoppedDatabase` (`pkg/dustpan/ipallow_deny_internal_test.go:375`), `TestAllowlistRequestSkipsProbes` (`pkg/dustpan/ipallow_deny_internal_test.go:98`), `TestAllowlistRefusalWritesAuditEntry` (`pkg/dustpan/ipallow_deny_internal_test.go:415`), `TestContains` (`pkg/ipallow/list_test.go:137`, row "mapped ipv4 in a v4 list") |
| 2.3 docs page | `docs/guides/ip-allowlists.md`, `TestE2EIPAllowlistsDocsPage` (`e2e/ip_allowlists_docs_test.go:23`) |
| 3.1 generated files | `internal/api/apidesc/openapi.json` and the clients on main |
| 4.2 e2e with the real CLIs | `TestIPAllowlists` (`e2e/ipallow_test.go:26`) |

The three open tasks of version 7 (4.1, 4.3 and 4.4) are tasks 5.1, 5.2 and 5.3 in `tasks.md`. That file keeps only open tasks.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- A test covers the installation grant for allowlists. (`pkg/dustpan/ipallow_access_test.go:245 (TestIPAllowlistNeedsInstallationGrant)`)
- A list holds at most 64 distinct prefixes, and an entry that does not parse gives an error that wraps `ErrInvalidList`. (`pkg/ipallow/list.go:47 (ParseList)`, `pkg/ipallow/list.go:18 (MaxEntries)`)
- A refused address maps to the error code access.ip-not-allowed. (`internal/errcode/errcode.go:81 (the ErrAddressNotAllowed case of Forbidden)`)
- A write of a list with an invalid entry is classified by the handler of the namespace list route. (`internal/api/server/ip_allowlist.go:230 (classifyIPAllowlist)`)
- The namespace list is read and changed with `GET`, `PUT` and `DELETE` on one route. Each route needs `namespace:manage-ip-allowlist`, and `PUT` and `DELETE` need a fresh sign-in through `h.fresh`. (`internal/api/server/ip_allowlist.go:27 (ipAllowlistPath)`, `internal/api/server/ip_allowlist.go:105 (ipAllowlistRoutes)`)
- The namespace list is enforced by an authorizer decorator, after authentication, and only when the server option `--ip-allowlists` is on. (`pkg/ipallow/gate.go:39 (Gate.Authorize)`, `pkg/dustpan/area_ipallow.go:30 (WithIPAllowlists)`)
- The installation list is enforced before the credential is read, when the setting `ipAllowlist.enabled` is `true`. (`pkg/dustpan/ipallow.go:470 (admit)`, `pkg/dustpan/ipallow.go:160 (checker)`)
- A request is checked when it has an `Authorization` header, a path under a sign-in prefix, or the session, access or refresh cookie. (`pkg/dustpan/ipallow.go:230 (allowlistRequest)`, cookies at `pkg/dustpan/ipallow.go:247-253`)
- The feature is off until the option turns it on. (`pkg/dustpan/area_ipallow.go:23 (defaultIPAllowlistSettings)`)

## What main lacks

Each claim has its proof.

- No setting key for a namespace list. Proof: `rg -n 'ipAllowlist\.' pkg internal --glob '*.go' --glob '!*_test.go'` prints only `pkg/ipallow/settings.go:13` and `pkg/ipallow/settings.go:16`.
- The namespace list lives in the column `namespaces.ip_allowlist` (`pkg/store/sqlite/migrations/0168_baseline.sql:23`, `pkg/store/postgres/migrations/0168_baseline.sql:844`), not in the settings table (`pkg/store/sqlite/migrations/0168_baseline.sql:1432`).
- No view of the settings resource. Proof: `rg -n 'settings/(session-policy|token-policy|ip-allowlist)' pkg internal --glob '*.go' --glob '!*_test.go'` prints nothing (exit 1). Only a test table names the planned paths of the other two policies (`pkg/dustpan/apidesc_test.go:605`).
- The checker holds a namespace slot, but no production call fills it. Proof: the four calls of `newAllowChecker` outside tests pass `nil` for the namespace list (`pkg/dustpan/ipallow.go:178,200,221,224`). So "deny when any list denies" holds today across the checker (installation) and the authorizer decorator (namespace), not inside one checker.
- Two switches. The installation list follows the setting `ipAllowlist.enabled` (`pkg/dustpan/ipallow.go:169`). The namespace list follows the option `--ip-allowlists` (`pkg/dustpan/area_ipallow.go:31`). ADR 55 decision 8 names one feature switch. Owner decision 9 makes the setting that switch (task 2.1).
- No test sends a request with the refresh cookie only. Proof: `rg -n 'RefreshCookie' pkg/dustpan --glob '*_test.go'` prints nothing.
- The settings value limit is below the list limit. A string value holds at most 1024 bytes (`pkg/setting/setting.go:85`, `pkg/store/sqlite/migrations/0168_baseline.sql:1435`). A list of 64 IPv6 entries in the longest form is 64 x 43 bytes plus 63 commas, about 2800 bytes. Computed, not run. Owner decision 11 sets 4096 bytes (task 2.3).

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 55: Sign-in and provisioning methods in milestone 4** (not owner-tagged).

- Decisions that apply: 8 (current), 11 (changed).
- Changed by: ADR 70 changes decisions 4, 9 and 11; ADR 285 changes decision 5; ADR 297 changes decision 9; ADR 301 extends decisions 6 and 7; ADR 301 extends decision 10; ADR 320 changes decisions 1 and 2; ADR 321 extends decision 1; ADR 321 extends decision 6; ADR 321 extends decision 7; ADR 321 extends decision 9. Chain to the end: ADR 70, ADR 285, ADR 297, ADR 301, ADR 320, ADR 321, ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 332, ADR 354, ADR 369, ADR 312, ADR 333, ADR 149, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 339, ADR 295, ADR 370.
- Current rule: this ADR, with ADR 70, 285, 297, 301, 320, 321.
- Quote (decision 8, `docs/adr/0055-sign-in-methods-in-milestone-4.md:89`): "IP allowlists are off by default, and each scope opts in."
- Quote: "An administrator of the installation turns the feature on first."
- Quote: "Then each scope opts in on its own: the installation, each namespace and each access token."
- Quote: "A scope that did not opt in has no list."
- Quote: "When the feature is on, each list applies to console sign-in and to requests with an access token."
- Quote (decision 11, line 104): "Each change writes an audit entry."
- Effect: The change follows decision 8 for off-by-default opt-in lists and decision 11 for an audit entry on each change. Decision 8 puts a list on each namespace and each token. Main has the namespace list on a legacy route. The new tasks of this change move it into the settings resource. The token list is in `ip-allowlists-token-lists`. Together the two changes do not narrow the spec.
- Conflict: none.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (not owner-tagged as a whole). Decision 7 carries "(owner decision 2026-10-18)".

- Decisions that apply: 3 (changed), 7 (current, owner), 8 (current).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5; ADR 373 extends decision 1; ADR 385 changes decision 3. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 373, ADR 385. ADR 385 is owner-tagged (owner, 2026-11-15).
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365, 373, 385.
- Quote (decision 7, `docs/adr/0212-planner-role-smtp-settings-fresh-mfa-proxies-restore-audit-and-branding.md:256`): "`/api/dustpan/v1/namespaces/{namespace}/settings/ip-allowlist`. A view answers the keys of its group, with the same revision, `ETag`, `PATCH` and lock rules. A view adds no setting and no rule of its own. A change through a view is a change of the resource."
- Quote (decision 7): "The actions are `installation:read-settings` and `installation:manage-settings` on `@server`, and `namespace:read-settings` and `namespace:manage-settings` on the namespace." Main names the installation actions `server:read-settings` and `server:manage-settings` (`pkg/identity/identity.go:257,271`).
- Quote (decision 3): "A change of an installation setting or of a namespace setting through the API needs a fresh MFA step when it makes the installation or the namespace less secure. When one key of a `PATCH` reduces security, the whole `PATCH` needs the step."
- Quote (decision 3 list): "IP allowlists from on to off,".
- Quote (ADR 385 decision 3): "An access token never gives one. The refusal is `403` with `mfa.fresh-step-required`." Quote (ADR 385 decision 2): "When the setting is off, the server asks a person for no fresh authentication." Main has no `freshAuthEnabled` yet (`rg -l 'freshAuthEnabled' --glob '*.go'` finds only `internal/api/server/firstadmin_test.go`), so the fresh step on main is always on.
- Effect: The namespace list becomes a key of the namespace settings resource, and the view only groups it. The key declares `ReducesSecurity` (like `pkg/ipallow/settings.go:47 (listReducesSecurity)`), so only a change that widens or clears the list needs a fresh step. Main today needs a step for every `PUT` and `DELETE`. The ADR settles this for settings, so it is not an open question.
- Conflict: none. Owner-tagged decision 7 is followed by its words.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide** (not owner-tagged).

- Decisions that apply: the path table at `docs/adr/0210-the-tool-neutral-api-prefix-paths-actions-versions-and-style.md:292`.
- Changed by and current rule: the row of ADR 210 in `docs/adr/INDEX.md`.
- Quote: "`/api/server/session-policy`, `/api/server/token-policy`, `/api/server/namespaces/{namespace}/ip-allowlist` | views of the settings resource (ADR 212 decision 7)".
- Effect: The legacy route `/api/server/namespaces/{namespace}/ip-allowlist` is replaced by the view. Task 4.3 removes it and names the console follow-up.
- Conflict: none.

**ADR 216: Namespace administration and operator scope: where each installation action goes (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: ADR 273 changes decision 8. Chain to the end: ADR 273, ADR 286.
- Current rule: this ADR, with ADR 273.
- Quote: "Operator actions keep the area `installation:`."
- Effect: The action names follow the map of decision 3. Main names the operator settings actions with the area `server:` (`pkg/identity/identity.go:257,271`). This change adds no operator action name.
- Conflict: none.

**ADR 297: Console sessions are sealed cookies, not stored sessions** (the owner set the direction on 31 October 2026).

- Changed by: ADR 301 extends decision 1; ADR 333 extends decision 4. Current rule: this ADR, with ADR 301, 333.
- Quote (`docs/adr/0297*.md:13`): "The server checks them from sealed cookies at each request and at each refresh, not from a stored session."
- Effect: The refresh route is a request with a cookie. The list check runs on it (`pkg/dustpan/ipallow.go:249`).
- Conflict: none.

**ADR 320: One sign-in path for every sign-in method** (not owner-tagged).

- Decisions that apply: 11 (current).
- Changed by: ADR 332 changes decision 12. Chain to the end: ADR 332.
- Current rule: this ADR, with ADR 332.
- Quote: "One answer, and the reason in the audit log (PM)."
- Effect: An allowlist denial is a network check, not a refused sign-in of a method. It has its own 403.
- Conflict: none.

**ADR 219: A lock in the static layer can turn a feature off** (owner-tagged).

- Changed by: none (`docs/adr/INDEX.md`, row 219: "-"). Current rule: this ADR.
- Quote (decision 1): "The static layer can lock each declared switch to on or to off, also a security switch."
- Quote (decision 4): "A security switch is one of: the audit log, the hash chain, signed checkpoints, the MFA requirement, break-glass, revoke on removal, and the IP allowlist feature."
- Effect: The IP allowlist feature is one switch. Owner decision 9: the setting `ipAllowlist.enabled` alone turns the namespace and token lists on, and the option `--ip-allowlists` stays as a static lock.
- Conflict: none.

## Owner decisions that apply

- **Decision 9 (owner)**: "The setting alone turns the namespace and token lists on; the static option stays as a lock." This answers the question about the switch. Task 2.1.
- **Decision 10 (owner)**: "The settings actions plus namespace:manage-ip-allowlist govern the namespace list." This answers the question about the actions. Task 3.2.
- **Decision 11 (owner)**: "These keys have a 4096-byte limit." This answers the question about the stored value. Task 2.3.

## Open questions

Open questions: none. The four questions of version 8 are answered or moved:

- Which switch turns the namespace and token lists on? Answered by owner decision 9.
- Which action reads and changes the namespace list through the view? Answered by owner decision 10.
- How does the list fit the stored value? Answered by owner decision 11.
- Who sets the list of an access token, and does a change need a fresh step? Moved with the whole token list to the change `ip-allowlists-token-lists`, which answers it with owner decision 8 (20 Nov 2026) and says "Open questions: none". Nothing in this change sets, stores or reads the own list of an access token.

Provisional, kept from version 7. It now describes main. It is a PM record under rule 88, not an owner decision:

- Does the server re-check an existing sealed-cookie console session on each request and on refresh? Main does (`pkg/dustpan/ipallow.go:247-253`). The spec locks it with a scenario, and task 1.3 tests it. Reason: Google Cloud evaluates access levels on every request. Persona impact: an allowed person sees no change. Escape hatch: turn the feature off in settings. The owner may reverse it.

Questions considered and answered:

- Which scopes hold a list? ADR 55 decision 8: the installation, each namespace and each access token. This change covers the installation and the namespace.
- What audit entries are written? ADR 55 decision 11: each change writes an entry. A denial also writes one.
- Which actions guard the installation flag and list? ADR 212 decision 7 (owner): the installation settings actions (`server:read-settings` and `server:manage-settings` in code). Version 7 named `installation:manage-sign-in`. That is not a settings action, so it is removed.
- Does the namespace list move to the settings resource? ADR 212 decision 7 and the path table of ADR 210: yes, as a view. The legacy route goes.
- Does narrowing a namespace list need a fresh step? No. ADR 212 decision 3 asks for a step only when a change reduces security.
- May the namespace scope hold an empty list? No, on main. `PUT` with an empty list gives 400 (`internal/api/server/ip_allowlist.go:179`, `pkg/ipallow/ipallow.go:188`). An empty value of a key means "no list". Only the installation scope can deny every address with an empty list.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Revised against main by the PM. Each file and line was read on main at the head of the branch `pm/ip-allowlists-revision`.
