## 1. Failing tests first

- [ ] 1.1 Add failing exchange test for valid SPIFFE ID, unknown ID, wrong CA and expired certificate. Check: Test TestMtlsExchangeIssuesTrustToken fails on main.
- [ ] 1.2 Add failing test that a rule of another namespace gives no token. Check: Test TestMtlsRuleIsPerNamespace fails on main.
- [ ] 1.3 Add failing test for duplicate SPIFFE ID rules and for several or zero URI SANs. Check: Test TestMtlsAmbiguousIdentityRefused fails on main.
- [ ] 1.4 Add failing test for revoked chain, wrong purpose chain, oversized chain, wrong URI scheme and future not-before time. Check: Test TestMtlsChainChecksRefused fails on main.
- [ ] 1.5 Add failing test for a disabled rule and a disabled bundle that each give 401. Check: Test TestMtlsDisabledRuleRefused fails on main.
- [ ] 1.6 Add failing test for a malformed, empty or anchor-less bundle save with 400. Check: Test TestMtlsBundleSaveRefused fails on main.

## 2. Build

- [ ] 2.1 Add trust bundle store, exact SPIFFE ID match, stored serial deny list and namespace fence. Check: `go test ./pkg/trustrule/...` passes.
- [ ] 2.2 Add mTLS exchange service that reads rule set and issues the stored trust token in one transaction snapshot. Check: `go test ./internal/trustrulesvc/...` passes.
- [ ] 2.3 Wire the exchange route POST /trust/v1/namespaces/{namespace}/exchange-mtls with action namespace:manage-trust-rules on management routes, trust rule API and audit entries, with an e2e step that exchanges a certificate with a generic HTTPS client, proves a proxied request with no certificate gets no token, and runs the CLIs with the issued token. Check: `make e2e E2E_RUN=mtls` passes against the real binary over TLS with the real Pulumi CLI and the real OpenTofu CLI.

## 3. Definition of done

- [ ] 3.1 Write the mTLS docs page with trust bundle, trust rule, rotation, revocation and exchange steps. Check: Docs page commands run as written and `make docs-lint` passes.
- [ ] 3.2 Run the usability check for a developer with a test SPIFFE ID. Check: Persona completes the path from the docs page alone and each error names the one command that helps.
- [ ] 3.3 Run wiring, route with authorization actions, e2e step, docs page, usability check and full checks. Check: `make check` passes with zero findings.
