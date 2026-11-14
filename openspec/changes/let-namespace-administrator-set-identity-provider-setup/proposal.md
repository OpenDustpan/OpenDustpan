---
version: 1
---

Issue: #953.

## Problem

A namespace administrator runs sign-in for one tenant and must connect the OIDC or SAML provider of that namespace, keep its members separate from other namespaces, and prove each change with fresh authentication and an audit entry.

## Evidence

- Issue asks for namespace OIDC and SAML settings, namespace-only principals, fresh authentication, audit entries, and docs page. Proof: Issue #953 body: In scope identity provider configuration at namespace scope (OIDC and SAML as on installation scope); principals created in the namespace; audit entries and alerts; acceptance sign-in through namespace A never makes or finds principal of B; change needs fresh authentication and writes audit entry; docs page.
- No account object exists above namespace; tenant is the namespace. Proof: ADR 215 decision 2 text: The namespace is the tenant (owner). The server has no object above the namespace other than the installation.
- Existing namespace settings route exists on main. Proof: Path exactly as pack lists it: GET /api/dustpan/v1/namespaces/{}/settings and ANY/GET/PATCH /api/dustpan/v1/namespaces/{}/settings
- Sign-in isolation must hold per home. Proof: ADR 216 decision 9 text: A sign-in authenticates only principals of its own home.

## Fix boundary

- NEW namespace OIDC and SAML settings at .../namespaces/{namespace}/sign-in managed by namespace administrator with `namespace:manage-sign-in`.
- Sign-in through a namespace provider finds or makes a principal of that namespace only.
- Provider change needs fresh authentication and writes a namespace audit entry.
- Setup docs page for namespace OIDC and SAML.

Out of scope:

- Path routing of hosted accounts by namespace path
- Refusal text for tokens that name the wrong namespace
- Removal of the account object if one exists
- Installation-scope providers (exist and stay)
- LDAP, CAS, OAuth groups, trusted header at namespace scope

## Check

- Add failing SaveProviderConfig and FindOrMakeSignInPrincipal isolation tests in `NEW pkg/namespace/signinsettings_test.go` for OIDC and SAML per namespace home. Check: TestNamespaceSignInFindsOnlyHomePrincipal in `NEW pkg/namespace/signinsettings_test.go` fails on main, passes after.
- Add failing SaveProviderConfig tests in `NEW pkg/namespace/signinsettings_test.go` for fresh-auth refusal and namespace audit entry. Check: TestNamespaceProviderSaveNeedsFreshAuth in `NEW pkg/namespace/signinsettings_test.go` fails on main, passes after.
- Add the NEW sign-in route in `NEW internal/namespacesvc/signin.go` with `namespace:manage-sign-in` check and fresh-auth check. Check: PUT the NEW route `PUT /api/dustpan/v1/namespaces/{namespace}/sign-in` with a session of a namespace admin passes the manage gate and fresh check; stale proof gets 401.
- Store provider config in `NEW pkg/namespace/signinsettings.go` keyed by stable namespace ID and show effective values without secrets. Check: `GET /api/dustpan/v1/namespaces/{namespace}/settings` with a session of a namespace admin shows effective provider state without secrets.
- Add e2e step in `NEW e2e/namespacesignin_test.go` against the real server with hosted namespace principals. Check: `NEW e2e/namespacesignin_test.go` signs in through namespace A provider and asserts no principal of namespace B appears; BLOCKED by NEW sign-in route until it lands.
- Add user docs page in `NEW docs/guides/namespace-sign-in.md` for namespace provider setup. Check: Docs page for namespace OIDC and SAML setup in `NEW docs/guides/namespace-sign-in.md` runs as written.
- Run usability check of the user path from `NEW docs/guides/namespace-sign-in.md`. Check: Namespace administrator completes setup from `NEW docs/guides/namespace-sign-in.md` alone; each error names the one command that helps.
- Run wiring and route authorization on `GET /api/dustpan/v1/namespaces/{namespace}/settings` with a session, plus e2e, docs, and `make check`. Check: `GET /api/dustpan/v1/namespaces/{namespace}/settings` with a session of a namespace admin passes; `make check` reports zero findings.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main lists installation identity providers helper for local users. (`pkg/dustpan/localusers.go:305 (func identityProviders)`)
- Main has e2e helper that configures an identity provider for sign-in tests. (`e2e/signin.go:360 (func Env.IdentityProvider)`)
- Main has e2e helper that configures a plain identity provider for sign-in tests. (`e2e/signin.go:373 (func Env.PlainIdentityProvider)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 215: The namespace is the tenant, and OpenDustpan Cloud is multitenant (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 9 (current), 12 (current).
- Changed by: ADR 248 changes decision 21. Chain to the end: ADR 248, ADR 286.
- Current rule: this ADR, with ADR 248.
- Quote: "Each principal and each group has a home (owner). The home of a principal or a group is the installation or one namespace. This extends ADR 67 decision 7 from service principals to persons, workloads and groups: - A principal of a namespa"
- Quote: "One person in two namespaces has two principals (owner). Each principal is in one home. A person who works in two namespaces of OpenDustpan Cloud has two principals, two sign-ins and two sets of tokens. The server never links two principa"
- Effect: Change keeps each principal in one home and keeps one person in two namespaces as two principals with two sign-ins.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Can a sign-in through namespace A find or make a principal of namespace B? ADR 215 decisions 9 and 12 and ADR 216 decision 9 answer it: each principal has one home and a sign-in through a namespace provider finds or makes a principal of that namespace only.
- Where do provider config changes write audit entries? ADR 216 decision 16 answers it: each change of a sign-in setting of the namespace writes an entry in the audit scope of the namespace, in the same transaction as the write.
- What counts as fresh authentication for a provider change? ADR 227 decision 2 with ADR 332 decisions 5 and 8 answers it: 900 seconds default, SP-started fresh sign-in for SAML with ForceAuthn, OIDC fresh prompt; IdP-started SAML never counts as fresh.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
