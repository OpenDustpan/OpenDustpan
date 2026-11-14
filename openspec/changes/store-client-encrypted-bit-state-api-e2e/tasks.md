## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write a test for the API field of a state version in the NEW file internal/api/tfe/state_version_client_encrypted_test.go. Check: go test ./internal/api/tfe/... -run StateVersionClientEncrypted fails on main
- [ ] 1.2 Write the e2e step with the real OpenTofu CLI in the NEW file e2e/state_client_encrypted_test.go. It uses the helper OpenTofu (e2e/tool.go:110). It calls GET /api/v2/workspaces/{}/state-versions with the access token of a principal that holds the read action. Check: make e2e runs the step in CI (the agent does not run it). Confirmed by: CI (the agent writes the e2e test).

## 2. Implementation

- [ ] 2.1 Add the field to the response in the state version handler of internal/api/tfe (the handler of the route GET /api/v2/workspaces/{}/state-versions). Add the field to the API description in internal/api/apidesc/openapi.json with `make generate`. The handler calls Service.StateClientEncrypted (pkg/workspace/state_encrypted.go:14). Check: make generate gives no diff. Confirmed by: the driver's gates.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Wire the field in the route, with the existing read action. Update the docs page of the API. Check that the page runs as written. Check: route test passes and make docs-lint passes. Confirmed by: CI (an e2e step in the NEW file e2e/store_client_encrypted_bit_state_api_e2e_docs_test.go runs each command of the docs page against the e2e test server).
- [ ] 4.2 Run all checks. Check: make check reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
