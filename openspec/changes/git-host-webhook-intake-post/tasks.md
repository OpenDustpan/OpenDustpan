## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. failing tests first

- [ ] 1.1 Write failing tests of the intake scenarios in NEW file internal/api/githubwebhook/intake_test.go. Check: TestForgeWebhookDedupe fails on main
- [ ] 1.2 Write failing forgetest cases for the file and review methods in NEW file pkg/forge/github/capabilities_test.go. Check: TestGitHubFilesReviewsCapabilities fails on main
- [ ] 1.3 Write a failing test for the unscoped token refusal in NEW file pkg/forge/github/repotoken_test.go. Check: TestRepoTokenScopedRefused fails on main

## 2. implementation

- [ ] 2.1 Implement the verifier and ParseEvent in NEW file pkg/forge/github/event.go, against the port at pkg/forge/forge.go:436. Check: go test ./pkg/forge/... -run TestParseEvent
- [ ] 2.2 Implement RepoToken in NEW file pkg/forge/github/repotoken.go and finish the adapter methods in pkg/forge/github/files.go:127 and pkg/forge/github/review.go:62. Check: go test ./pkg/forge/github/...
- [ ] 2.3 Add the NEW store method MarkDelivery in NEW file pkg/forge/delivery.go with its SQL in each dialect queries.sql. Check: go test ./pkg/forge/...
- [ ] 2.4 Wire POST /webhooks/{hook ID}: declare the route in NEW file internal/api/githubwebhook/routes.go and the handler HandleDelivery in NEW file internal/api/githubwebhook/intake.go. Check: The test server answers 204 for good, bad and replayed deliveries

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `internal/db` (`make generate`, sqlc). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. definition of done

- [ ] 4.1 List the intake route on the public list of the route test with no authorization action (internal/api/route). Check: The route test passes
- [ ] 4.2 Add an e2e step in NEW file e2e/githubwebhook_test.go. BLOCKED by the missing real-binary e2e harness for GitHub deliveries (the pack names none): the step sends a signed push delivery to POST /webhooks/{hook ID} over TLS with the event secret of the registration as its credential, then calls POST /api/dustpan/v1/namespaces/{namespace}/git-host-connections/{id}/check with an access token of a principal that holds the check action. Check: make e2e step passes: 204, hint recorded, bad signature counted
- [ ] 4.3 Write the docs page NEW docs/guides/git-host-webhook.md and the federation guide section for the stable hook hostname. Check: make docs-lint
- [ ] 4.4 Run the usability check and the full check. Check: make check reports zero findings
