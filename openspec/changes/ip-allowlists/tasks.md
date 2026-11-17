## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. failing tests

- [ ] 1.1 Write failing tests in NEW file pkg/dustpan/ipallow_deny_test.go for a denied console sign-in at POST /api/dustpan/v1/sign-in/providers/{provider}/password and a denied access token request to GET /api/dustpan/v1/namespaces/{namespace}/units. Check: TestDenyConsoleSignInOutsideList and TestDenyTokenOutsideList fail on main.
- [ ] 1.4 SECURITY. Write a hostile test in NEW file pkg/dustpan/ipallow_deny_test.go, test name TestDenyAnyListDenies. It sends an address that is in the namespace list but outside the installation list straight to the checker newAllowChecker (NEW). It checks that the answer is denial with the installation scope and that the denial audit entry holds the scope and the address. Check: TestDenyAnyListDenies passes and fails when the checker ignores the installation list.
- [ ] 1.2 Write a failing test in NEW file pkg/dustpan/ipallow_cidr_test.go for an invalid CIDR on PATCH /api/dustpan/v1/settings. Check: TestInvalidCidrRefused fails on main.
- [ ] 1.3 Write a failing test in NEW file pkg/dustpan/ipallow_proxy_test.go for a spoofed proxy header. Check: TestRecordedAddressIgnoresUntrustedHeader fails on main.

## 2. implementation

- [ ] 2.0 Add the NEW error code ip-allowlist.invalid-cidr in internal/errcode/catalog.go and map `ipallow.ErrInvalidList` to it in internal/errcode/errcode.go and internal/api/server/ip_allowlist.go. Put the code in the NEW file internal/errcode/catalog_ipallowlist.go (one catalog_<area>.go for each area, internal/errcode/catalog.go:74-76) with a NEW line for the area in `areas()` of internal/errcode/catalog.go, and a NEW golden file internal/errcode/testdata/catalog/ip-allowlist.txt. Map `ipallow.ErrInvalidList` to the NEW code with status 400 in `settingsError` of internal/api/server/settings.go (the settings PATCH path), so that a bad list does not answer settings.value-invalid. Keep `ipallow.MaxEntries` of pkg/ipallow/list.go at 64 and add a test for the 65th entry in pkg/ipallow/list_test.go. Check: the error code catalog test passes and the 65th entry answers 400 with ip-allowlist.invalid-cidr. Confirmed by: CI (the golden files of internal/errcode/testdata/catalog are regenerated with their generator).
- [ ] 2.1 Add flag and list keys with CIDR parsing and audit entries in NEW file pkg/dustpan/ipallow.go, with the setting types in NEW file pkg/ipallow/settings.go, and register them in pkg/dustpan/area_ipallow.go and pkg/dustpan/settings_resource.go. Check: PATCH settings returns 200 and GET shows the effective value.
- [ ] 2.2 Wire the checker into sign-in and token authentication. Call the NEW checker newAllowChecker from NEW file pkg/dustpan/ipallow.go. Check: A denied address gets 403 with access.ip-not-allowed.
- [ ] 2.3 Update docs/guides/ip-allowlists.md. Check: Each command on the page runs as written. Confirmed by: CI (an e2e step in the NEW file e2e/ip_allowlists_docs_test.go runs each command of the docs page against the e2e test server). `make docs-lint` passes on each edited guide.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. definition of done

- [ ] 4.1 Check the routes and their authorization actions. Check: The route test passes.
- [ ] 4.2 Run an e2e step with the real Pulumi CLI and OpenTofu using an access token against GET /api/dustpan/v1/namespaces/{namespace}/units from allowed and denied addresses. Check: e2e/ipallow_test.go passes. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.3 Do the usability check from the docs page alone. Check: The site administrator completes the path. A person's step.
- [ ] 4.4 Run the full check. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
