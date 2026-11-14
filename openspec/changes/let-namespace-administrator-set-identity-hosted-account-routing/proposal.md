---
version: 1
---

Issue: #963.

## Problem

A developer works with stacks and workspaces in one hosted account at a time and needs one clear credential per namespace. The server refuses a cross-namespace token with the token namespace and the login command, keeps no account object, and documents switching.

## Evidence

- The server has per-namespace principal and token helpers on main. Proof: pkg/access/reach.go:886 (func reachWalker.deriveNamespaceTokens) and pkg/access/access_test.go:1473 (TestNamespacePrincipalTokens)
- Token error codes exist on main. Proof: internal/errcode/catalog_token.go:19 (token.outside-reach) and internal/errcode/catalog_token.go:11 (token.invalid)
- The issue scope says the server API has no account object and no CLI changes. Proof: #963 Scope and Acceptance criteria: In: console path prefix, account label, token refusal text, switching docs. Out: changes to the CLIs.

## Fix boundary

- Console path prefix on one host with account as hosted-only console word for namespace
- Token refusal for readable namespace B with token of namespace A names A and the login command
- 404 with no data for unknown or unreadable namespace paths
- No account object or account ID in the server API with a guarding test
- Docs page for switching accounts with re-login and per-namespace credential

Out of scope:

- Provider management and per-namespace OIDC and SAML setup
- Changes to the Pulumi CLI or OpenTofu
- Billing objects, usage counters, and namespace move between principals

## Check

- Add refusal test for token of A for readable B in pkg/access/access_test.go:1473. Check: TestCrossNamespaceTokenRefusal fails on main, passes after fix.
- Add unknown-path 404 test for token of A for unknown B in pkg/access/access_test.go:1473. Check: TestUnknownNamespacePath404 fails on main, passes after fix.
- Add no-account-object test on the API description in NEW internal/api/route/account_test.go. Check: TestApiHasNoAccountObject fails when an account object exists, passes after fix.
- Set refusal text at point of use for ANY /api/server/namespaces/{}/principals with credential kind access token using fixed allowlist pulumi login and tofu login. Check: Call ANY /api/server/namespaces/{}/principals with credential kind access token returns 403 token.outside-reach naming A; `go test ./pkg/access/...` passes.
- Wire refusal through ANY /api/server/namespaces/{}/principals with credential kind access token and check e2e with real Pulumi CLI and OpenTofu binaries. Check: Call ANY /api/server/namespaces/{}/principals with credential kind access token and ANY /api/dustpan/v1/me/linked-accounts with credential kind session; e2e with real Pulumi CLI and OpenTofu binaries passes for the refusal path.
- Write account switching docs page for re-login and per-namespace credential in NEW docs page. Check: Docs page renders and `make docs-lint` passes.
- Run usability check from the docs page alone and full definition of done. Check: `make check` reports zero findings.

Depends on:

- let-namespace-administrator-set-identity-provider-setup
- `let-namespace-administrator-set-identity-provider-setup` (the part before it in the split)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Reach walker derives per-namespace tokens. (`pkg/access/reach.go:886 (func reachWalker.deriveNamespaceTokens)`)
- Reach walker lists namespace principals. (`pkg/access/reach.go:723 (func reachWalker.namespacePrincipals)`)
- Authorizer adds namespace principals. (`pkg/access/authorizer.go:406 (func Authorizer.addNamespacePrincipals)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current), 3 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request. A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation. A feature that a person or a client can see has a route, a setting, a command, a flag, a console page or an error that a user reads. A pull request that changes only internal code needs no page."
- Effect: The change ships the account switching docs page in the same pull request.
- Conflict: none.

**ADR 215: The namespace is the tenant, and OpenDustpan Cloud is multitenant (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 2 (current), 3 (current), 4 (current), 6 (current), 8 (current), 12 (current), 14 (current), 15 (current).
- Changed by: ADR 248 changes decision 21. Chain to the end: ADR 248, ADR 286.
- Current rule: this ADR, with ADR 248.
- Quote: "The namespace is the tenant (owner). The server has no object above the namespace other than the installation. The containment chain stays installation > namespace > collection > unit. There is no account object, no account ID, no `accoun"
- Quote: "The word "account" (owner). "Account" is the label of the hosted console for a namespace, as ADR 59 decision 6 says. The API, the CLI output and each error text say "namespace". The Pulumi CLI and the TFE-compatible API keep their own wor"
- Quote: "One host, with the namespace in the path (owner). OpenDustpan Cloud answers on one host name, `app.opendustpan.com`. The console page of a namespace is `https://app.opendustpan.com/<namespace name>`. The server finds the namespace of each"
- Quote: "Reserved names. The server refuses a namespace name that is equal to a first path segment that the server or the console serves, for example `api`, `oidc`, `tofu`, `runner`, `sign-in`, `mcp`, `metrics`, `chat`, `.well-known`, `health` (th"
- Quote: "Unknown paths. A path that names a namespace that does not exist, or that the caller cannot read, answers 404. The answer holds no data of a namespace: no name, no ID and no count. ADR 166 decision 14 gives the rule.  ### Principals of a"
- Quote: "One person in two namespaces has two principals (owner). Each principal is in one home. A person who works in two namespaces of OpenDustpan Cloud has two principals, two sign-ins and two sets of tokens. The server never links two principa"
- Quote: "One credential for each host. The Pulumi CLI keeps one access token for each backend URL. OpenTofu keeps one token for each host name. On one host, a person with two principals cannot hold both tokens in one login. The Pulumi CLI and Open"
- Quote: "The smallest fix: a token for each namespace in the environment. The server changes nothing in the tool-compatible APIs. The docs of OpenDustpan Cloud tell a person in two namespaces to do one of these: - Run `pulumi login` or `tofu login"
- Effect: The change follows the decisions: no account object, hosted word account only in console copy, one host with namespace in path with reserved names and 404 for unknown paths, separate principals, refusal only for a known readable namespace with namespace word and per-tool login command, no tool API change.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- How are hosted accounts routed and how do two namespaces work on one host? ADR 215 decisions 2, 3, 4, 6, 8, 12, 14 and 15 answer it: no account object, account only as hosted console word, one host with namespace in path and reserved names, 404 for unknown paths, separate principals, one credential per host, refusal text plus env token docs, no tool API change.
- Are docs needed for switching accounts? ADR 214 decisions 1 and 2 answer it: the docs page ships in the same pull request.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
