## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. failing tests first

- [ ] 1.1 Write failing tests of the intake scenarios. Check: TestForgeWebhookDedupe fails on main
- [ ] 1.2 Write failing forgetest cases for the file and review methods. Check: TestGitHubFilesReviewsCapabilities fails on main
- [ ] 1.3 Write a failing test for the unscoped token refusal. Check: TestRepoTokenScopedRefused fails on main

## 2. implementation

- [ ] 2.1 Implement ParseEvent and the verifier. Check: go test ./pkg/forge/... -run TestParseEvent
- [ ] 2.2 Implement the adapter methods and RepoToken. Check: go test ./pkg/forge/github/...
- [ ] 2.3 Wire POST /webhooks/{hook ID}. Check: The test server answers 204 for good, bad and replayed deliveries

## 3. definition of done

- [ ] 3.1 List the intake route on the public list with no authorization action. Check: The route test passes
- [ ] 3.2 Add an e2e step on route POST /webhooks/{hook ID} with a signed push delivery to the real binary over TLS, and a repo token read through the existing git-host-connections check route. Check: make e2e step passes: 204, hint recorded, bad signature counted
- [ ] 3.3 Write the docs page and the federation guide section for the stable hook hostname. Check: make docs-lint
- [ ] 3.4 Run the usability check and the full check. Check: make check reports zero findings
