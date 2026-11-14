## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write a test for the API field of a state version. Check: go test ./internal/api/tfe/... -run StateVersionClientEncrypted fails on main
- [ ] 1.2 Write the e2e step with the real OpenTofu CLI. It calls GET /api/v2/workspaces/{}/state-versions with the access token of a principal that holds the read action. Check: make e2e runs the step in CI (the agent does not run it)

## 2. Implementation

- [ ] 2.1 Add the field to the response and the API description. Check: make generate gives no diff

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire the field in the route, with the existing read action. Update the docs page of the API. Check that the page runs as written. Check: route test passes and make docs-lint passes
- [ ] 4.2 Run all checks. Check: make check reports zero findings
