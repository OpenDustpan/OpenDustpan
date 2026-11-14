---
version: 1
---

Issue: #2473, #2582.

## Problem

Site administrator manages grants and tokens and must rely on each layer to stop an attack alone; today a bypass of the route gate leaves the grant and token services with no allowlist or namespace-service check.

## Evidence

- Access and access-token services are named exceptions that hold the bare authorizer with no allowlist gate and no namespace service rule. Proof: Issues #2473 and #2582 state the exceptions; path pkg/dustpan/authchain_internal_test.go
- NewServer chain test names the two exceptions and must drop them after the fix. Proof: Issue #2473 body names TestNewServerAuthenticatorChain; path pkg/dustpan/authchain_internal_test.go
- Refusal codes access.ip-not-allowed, access.denied and token.invalid exist on main. Proof: Pack main lists error code access.ip-not-allowed, error code access.denied and error code token.invalid

## Fix boundary

- Grant and token request checks go through the wrapped authorization chain at the service layer, including allowlistGate and NamespaceServiceRule.
- Concrete authorizer stays only as a separate escalation comparator.
- Two exception entries removed from TestNewServerAuthenticatorChain.

Out of scope:

- Other named exceptions of the chain test, if any.
- Console or docs redesign of grants and tokens.

## Check

- Add hostile NewServer test in pkg/dustpan/authchain_internal_test.go:129 for grant change via ANY /api/server/grants with access token through allowlistGate refused from outside allowlist with allow-all embedder authorizer. Check: TestNewServerGrantRefusesOutsideAllowlist fails on main, passes after fix.
- Add hostile NewServer test in pkg/dustpan/authchain_internal_test.go:129 for namespace-service grant via ANY /api/server/namespaces/{}/grants with access token through NamespaceServiceRule refused in another namespace. Check: TestNewServerNamespaceServiceCannotGrantElsewhere fails on main, passes after fix.
- Add hostile NewServer test in pkg/dustpan/authchain_internal_test.go:129 for token issue via ANY /api/server/tokens with session credential through allowlistGate refused from outside allowlist. Check: TestNewServerTokenRefusesOutsideAllowlist fails on main, passes after fix.
- Add service-level escalation test in pkg/access/access_test.go:394 for Grant above caller reach refused with allow-all embedder authorizer. Check: TestGrantEscalationRefusedWithAllowAll fails on main, passes after fix.
- Wire grant service in pkg/access and token service in pkg/accesstoken request checks through the chain via newAccessService in pkg/dustpan; keep concrete authorizer only for escalation comparison. Check: go test ./pkg/access/... ./pkg/accesstoken/... ./pkg/dustpan/... passes.
- Remove the two entries from the named exception list of TestNewServerAuthenticatorChain in pkg/dustpan/authchain_internal_test.go:129. Check: go test ./pkg/dustpan/ -run TestNewServerAuthenticatorChain passes with exceptions removed.
- Add e2e refusal steps for grant and token paths through the real server routes with a session credential. Check: E2E grant and token refusal steps against ANY /api/server/grants and ANY /api/server/tokens pass.
- Update the user docs page for grants and tokens in the same pull request. Check: Docs page records the service-layer refusal, the SDK embedder fail-closed on missing address, and GET /api/server/caller/access help.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Provides the server constructor that wires services and the authorization chain. (`pkg/dustpan/dustpan.go:774 (func NewServer)`)
- Checks the authenticator and authorization chain of NewServer, with named exceptions. (`pkg/dustpan/authchain_internal_test.go:129 (func TestNewServerAuthenticatorChain)`)
- Implements the grant service domain type. (`pkg/access/service.go:114 (func NewService)`)
- Builds the access service for the server. (`pkg/dustpan/dustpan.go:3176 (func newAccessService)`)
- Applies the IP allowlist gate. (`pkg/dustpan/area_ipallow.go:84 (func allowlistGate)`)
- Defines the namespace-service principal check. (`pkg/identity/namespace_service.go:84 (func Principal.IsNamespaceService)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Do grant and token services need the authorization chain at the service layer? Yes. Issue 2473 states the two services use the concrete authorizer with no allowlist gate and no namespace-service rule at the service layer, and issue 2582 confirms the split fix. The pack adrs list is empty so no ADR text can be quoted.
- How to keep the escalation check that needs the concrete grant model? Split them: the request check uses the chain, the escalation comparison keeps the concrete grant view as a separate read-only argument. Issues 2473 and 2582 state this split.
- Is any ADR touched by this change? No ADR text is in the pack. The pack adrs list is empty, so no ADR conflict claim can be quoted and adr_check stays empty.
- Do the refusal error codes exist on main? Error codes access.ip-not-allowed, access.denied and token.invalid exist on main per the pack main list, so none is NEW.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
