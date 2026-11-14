## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Tests first

- [ ] 1.1 In NEW file internal/api/server/capabilities_versions_test.go add failing server test that calls GET /api/dustpan/v1/capabilities with access token holding caller:read and only collection actions, with no credential, and with a revoked credential. Check: `go test ./internal/api/server/ -run TestCapabilitiesVersions -count=1` fails on main
- [ ] 1.2 In NEW file e2e/capabilities_versions_test.go add e2e step that reads GET /api/dustpan/v1/capabilities with access token against real binary for release build with ldflags version, dev build default, and describe form. Check: `go test ./e2e/ -run TestCapabilitiesVersions -count=1` fails on main

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 2.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Extend capabilities handler at internal/api/pulumi/pulumi.go:317 (func Handler.capabilities) to serve GET /api/dustpan/v1/capabilities with access token and add NEW fields serverVersion and descriptionVersion to the openapi.json capabilities entry after let-lock-in-static-settings lands. Check: `go test ./internal/api/... -run TestCapabilities -count=1`
- [ ] 3.2 Declare authorization action caller:read on installation for GET /api/dustpan/v1/capabilities and keep 401 for anonymous with access token gate; prove collection-only token passes and token without caller:read gets 403. Check: Route test passes with declared caller:read on installation for GET /api/dustpan/v1/capabilities
- [ ] 3.3 Regenerate API clients from updated description and keep Pulumi CLI contract safe. Check: `make clients-check` passes after regeneration
- [ ] 3.4 Update user docs page for version check in same pull request. Check: Docs page renders and `make docs-lint` passes for changed files
- [ ] 3.5 Run full checks with GET /api/dustpan/v1/capabilities using access token, e2e step against real CLI, docs page, usability check, and latency check of 95th percentile under 200 ms in-process. Check: `make check` with zero findings
