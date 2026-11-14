## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add hostile NewServer test in pkg/dustpan/authchain_internal_test.go:129 for grant change via ANY /api/server/grants with access token through allowlistGate refused from outside allowlist with allow-all embedder authorizer. Check: TestNewServerGrantRefusesOutsideAllowlist fails on main, passes after fix.
- [ ] 1.2 Add hostile NewServer test in pkg/dustpan/authchain_internal_test.go:129 for namespace-service grant via ANY /api/server/namespaces/{}/grants with access token through NamespaceServiceRule refused in another namespace. Check: TestNewServerNamespaceServiceCannotGrantElsewhere fails on main, passes after fix.
- [ ] 1.3 Add hostile NewServer test in pkg/dustpan/authchain_internal_test.go:129 for token issue via ANY /api/server/tokens with session credential through allowlistGate refused from outside allowlist. Check: TestNewServerTokenRefusesOutsideAllowlist fails on main, passes after fix.
- [ ] 1.4 Add service-level escalation test in pkg/access/access_test.go:394 for Grant above caller reach refused with allow-all embedder authorizer. Check: TestGrantEscalationRefusedWithAllowAll fails on main, passes after fix.

## 2. Fix

- [ ] 2.1 Wire grant service in pkg/access and token service in pkg/accesstoken request checks through the chain via newAccessService in pkg/dustpan; keep concrete authorizer only for escalation comparison. Check: go test ./pkg/access/... ./pkg/accesstoken/... ./pkg/dustpan/... passes.
- [ ] 2.2 Remove the two entries from the named exception list of TestNewServerAuthenticatorChain in pkg/dustpan/authchain_internal_test.go:129. Check: go test ./pkg/dustpan/ -run TestNewServerAuthenticatorChain passes with exceptions removed.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Add e2e refusal steps for grant and token paths through the real server routes with a session credential. Check: E2E grant and token refusal steps against ANY /api/server/grants and ANY /api/server/tokens pass.
- [ ] 4.2 Update the user docs page for grants and tokens in the same pull request. Check: Docs page records the service-layer refusal, the SDK embedder fail-closed on missing address, and GET /api/server/caller/access help.
- [ ] 4.3 Run full checks and walk the usability path from the docs page alone. A person's step. Check: `make check` reports zero findings.
