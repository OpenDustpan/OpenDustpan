## 1. Failing tests first

- [ ] 1.1 Add failing port tests for bundle save checks, own-issuer refusal, and SPIFFE ID shape Check: go test ./pkg/trust/... -run TestBundleSaveRefusesBadPEM fails on main
- [ ] 1.2 Add failing service tests for namespace fence, rule auth, fresh authentication, concurrency, and delete Check: go test ./pkg/trust/... -run TestRuleRefusesOtherNamespace fails on main

## 2. Build

- [ ] 2.1 Build bundle store with versioned CAs, rotation, overlap end, and fence checks Check: go test ./... trust bundle and rule suites pass on SQLite and PostgreSQL
- [ ] 2.2 Wire rule CRUD routes with actions, stable references, and audit entries Check: `GET /api/dustpan/v1/namespaces/{namespace}/trust-rules` answers only that namespace with declared actions

## 3. Definition of done

- [ ] 3.1 Run full check with wiring, route action test, docs page, and usability check from the docs page Check: `make check` reports zero findings
