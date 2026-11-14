## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing e2e first

- [ ] 1.1 Add failing e2e step: valid SPIFFE ID exchange on POST /api/dustpan/v1/namespaces/{namespace}/trust-rules/exchange with a client certificate, then pulumi up and tofu apply with the trust token (credential kind trust). BLOCKED by mtls-mtls-exchange-token until it merges. Check: e2e TestMtlsProofPulumiTofu fails on main, passes with the change.
- [ ] 1.2 Add failing e2e refusals on POST /api/dustpan/v1/namespaces/{namespace}/trust-rules/exchange (credential kind trust): hostile certificates and a forwarded header with no handshake certificate, no token issued. BLOCKED by mtls-mtls-exchange-token until it merges. Check: e2e TestMtlsProofRefusals lists each 401 case with audit entry and body check.
- [ ] 1.3 Add failing e2e grant and lifetime checks with the trust token (credential kind trust) from POST /api/dustpan/v1/namespaces/{namespace}/trust-rules/exchange: 403 within grants, 401 after expiry or revoke. BLOCKED by mtls-mtls-exchange-token until it merges. Check: e2e TestMtlsProofGrants shows 403 and 401 cases.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Wire the e2e step with test CA, SPIFFE ID certificate, trust bundle, and trust rule fixtures against POST /api/dustpan/v1/namespaces/{namespace}/trust-rules/exchange and the trust token (credential kind trust). BLOCKED by mtls-trust-bundle-rules and mtls-mtls-exchange-token until they merge. Check: make e2e MTLS=1 runs the new steps against the real binary over TLS.
- [ ] 3.2 Write the mTLS docs page with bundle, rule, exchange, CLI use, safe transport, and TLS proxy rule. Check: Each shell block of the docs page runs as written on a new install.
- [ ] 3.3 Run a usability check and record where the path costs time. Check: The persona reaches both CLI runs from the docs page alone.
- [ ] 3.4 Pass the definition of done for POST /api/dustpan/v1/namespaces/{namespace}/trust-rules/exchange with the trust token (credential kind trust): wiring, route actions unchanged, e2e step, docs page, usability check. BLOCKED by mtls-mtls-exchange-token until it merges. Check: make check reports zero findings and make e2e passes.
