## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. failing tests

- [ ] 1.1 Write failing tests in NEW file pkg/dustpan/ipallow_deny_test.go for a denied console sign-in at POST /api/dustpan/v1/sign-in/providers/{provider}/password and a denied access token request to GET /api/dustpan/v1/namespaces/{namespace}/units. Check: TestDenyConsoleSignInOutsideList and TestDenyTokenOutsideList fail on main.
- [ ] 1.2 Write a failing test in NEW file pkg/dustpan/ipallow_cidr_test.go for an invalid CIDR on PATCH /api/dustpan/v1/settings. Check: TestInvalidCidrRefused fails on main.
- [ ] 1.3 Write a failing test in NEW file pkg/dustpan/ipallow_proxy_test.go for a spoofed proxy header. Check: TestRecordedAddressIgnoresUntrustedHeader fails on main.

## 2. implementation

- [ ] 2.1 Add flag and list keys with CIDR parsing and audit entries in NEW file pkg/dustpan/ipallow.go. Check: PATCH settings returns 200 and GET shows the effective value.
- [ ] 2.2 Wire the checker into sign-in and token authentication. Call the NEW checker newAllowChecker from NEW file pkg/dustpan/ipallow.go. Check: A denied address gets 403 with ip-allowlist.denied.
- [ ] 2.3 Update docs/guides/ip-allowlists.md. Check: Each command on the page runs as written. Confirmed by: CI (an e2e step in the NEW file e2e/ip_allowlists_docs_test.go runs each command of the docs page against the e2e test server).

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. definition of done

- [ ] 4.1 Check the routes and their authorization actions. Check: The route test passes.
- [ ] 4.2 Run an e2e step with the real Pulumi CLI and OpenTofu using an access token against GET /api/dustpan/v1/namespaces/{namespace}/units from allowed and denied addresses. Check: e2e/ipallow_test.go passes. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.3 Do the usability check from the docs page alone. Check: The site administrator completes the path. A person's step.
- [ ] 4.4 Run the full check. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
