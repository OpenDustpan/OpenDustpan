## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add refusal test for token of A for readable B in pkg/access/access_test.go:1473. Check: TestCrossNamespaceTokenRefusal fails on main, passes after fix.
- [ ] 1.2 Add unknown-path 404 test for token of A for unknown B in pkg/access/access_test.go:1473. Check: TestUnknownNamespacePath404 fails on main, passes after fix.
- [ ] 1.3 Add no-account-object test on the API description in NEW internal/api/route/account_test.go. Check: TestApiHasNoAccountObject fails when an account object exists, passes after fix.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Set refusal text at point of use for ANY /api/server/namespaces/{}/principals with credential kind access token using fixed allowlist pulumi login and tofu login. Check: Call ANY /api/server/namespaces/{}/principals with credential kind access token returns 403 token.outside-reach naming A; `go test ./pkg/access/...` passes.
- [ ] 3.2 Wire refusal through ANY /api/server/namespaces/{}/principals with credential kind access token and check e2e with real Pulumi CLI and OpenTofu binaries. Check: Call ANY /api/server/namespaces/{}/principals with credential kind access token and ANY /api/dustpan/v1/me/linked-accounts with credential kind session; e2e with real Pulumi CLI and OpenTofu binaries passes for the refusal path.
- [ ] 3.3 Write account switching docs page for re-login and per-namespace credential in NEW docs page. Check: Docs page renders and `make docs-lint` passes.
- [ ] 3.4 Run usability check from the docs page alone and full definition of done. Check: `make check` reports zero findings.
