## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write failing tests in pkg/accesstoken/accesstoken_test.go:1386 (TestWarningFor) for newWarningState (NEW): inside and outside the window, expired, no expiry, boundaries, precedence and zero threshold. Check: go test ./pkg/accesstoken -run TestWarningFor fails on main
- [ ] 1.2 Write failing tests in internal/api/server/tokens_test.go (NEW) for 403 without the read grant and for the audit entry. Check: go test ./internal/token fails on main

## 2. Build

- [ ] 2.1 Add the warning fields to GET /api/server/caller/tokens with a personal token. Edit the route row in internal/api/server/tokens.go:543, the handler serveTokenRead in internal/api/server/tokens.go:232 and the declaration in the route table of internal/api/server (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description` then writes internal/api/apidesc/openapi.json; never edit it by hand). Check: API test returns expiry, seconds left and level
- [ ] 2.2 Add the same fields to the list reads under /api/server/tokens and /api/server/principals/{}/tokens. Edit the route rows and handlers in internal/api/server/tokens.go and the declaration in the route table of internal/api/server (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description` then writes internal/api/apidesc/openapi.json; never edit it by hand). Check: API test returns a level for each token

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Wire the fields in the server and write the docs page with the rotation path. The tool-compatible carriage is BLOCKED by the carriage open question. Check: docs page commands run as written in a test. Confirmed by: CI (an e2e step in the NEW file e2e/token_life_cycle_expiry_email_expiry_warning_display_docs_test.go runs each command of the docs page against the e2e test server).
- [ ] 4.2 Add an e2e step in the NEW file e2e/token_life_cycle_expiry_email_expiry_warning_display_test.go that reads GET /api/server/caller/tokens with a personal token against the real binary over TLS. The CLI text step is BLOCKED by the carriage open question. Check: make e2e step passes. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.3 Run the full check and the usability check from the docs page alone. Check: `make check` reports zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
