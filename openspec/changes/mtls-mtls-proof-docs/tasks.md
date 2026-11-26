## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing e2e first

- [ ] 1.1 Add failing e2e step in e2e/mtls_proof_test.go (NEW): valid SPIFFE ID exchange on POST /api/dustpan/v1/namespaces/{namespace}/trust-rules/exchange with a client certificate, then pulumi up and tofu apply with the exchanged token (kind `mtls-exchange`, credential `trust-token`). BLOCKED by mtls-mtls-exchange-token until it merges. Check: e2e TestMtlsProofPulumiTofu fails on main (the NEW test `TestMtlsProofPulumiTofu` is not on main), passes with the change.
- [ ] 1.2 Add failing e2e refusals in e2e/mtls_proof_test.go (NEW) on POST /api/dustpan/v1/namespaces/{namespace}/trust-rules/exchange (token kind `mtls-exchange`, credential `trust-token`): hostile certificates and a forwarded header with no handshake certificate, no token issued. BLOCKED by mtls-mtls-exchange-token until it merges. Check: e2e TestMtlsProofRefusals lists each 401 case with audit entry and body check.
- [ ] 1.3 Add failing e2e grant and lifetime checks in e2e/mtls_proof_test.go (NEW) with the exchanged token (kind `mtls-exchange`, credential `trust-token`) from POST /api/dustpan/v1/namespaces/{namespace}/trust-rules/exchange: 403 within grants, 401 after expiry or revoke. BLOCKED by mtls-mtls-exchange-token until it merges. Check: e2e TestMtlsProofGrants shows 403 and 401 cases.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Wire the e2e step with test CA, SPIFFE ID certificate, trust bundle, and trust rule fixtures against POST /api/dustpan/v1/namespaces/{namespace}/trust-rules/exchange and the exchanged token (kind `mtls-exchange`, credential `trust-token`). BLOCKED by mtls-trust-bundle-rules and mtls-mtls-exchange-token until they merge. Check: make e2e MTLS=1 runs the new steps against the real binary over TLS.
- [ ] 3.2 Write the mTLS docs page in docs/guides/mtls.md (NEW) with bundle, rule, exchange, CLI use, safe transport, and TLS proxy rule. Check: Each shell block of the docs page runs as written on a new install.go runs each command of the docs page against the e2e test server). `make docs-lint` passes on each edited guide.
- [ ] 3.3 Agent pre-run (human run-through in person-steps (mtls-mtls-proof-docs 3.3)): Run a usability check and record where the path costs time. Check: The persona reaches both CLI runs from the docs page alone. Record the transcript in the pull request body.
- [ ] 3.4 Pass the definition of done for POST /api/dustpan/v1/namespaces/{namespace}/trust-rules/exchange with the exchanged token (kind `mtls-exchange`, credential `trust-token`): wiring, route actions unchanged, e2e step, docs page, usability check. BLOCKED by mtls-mtls-exchange-token until it merges. Check: make check reports zero findings and make e2e passes. Confirmed by: CI (the agent runs the fast checks only).
