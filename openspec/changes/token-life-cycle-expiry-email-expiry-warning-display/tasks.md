## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write failing tests for newWarningState: inside and outside the window, expired, no expiry, boundaries, precedence and zero threshold. Check: go test ./pkg/accesstoken -run TestWarningFor fails on main
- [ ] 1.2 Write failing tests for 403 without the read grant and for the audit entry. Check: go test ./internal/token fails on main

## 2. Build

- [ ] 2.1 Add the warning fields to GET /api/server/caller/tokens with a personal token. Check: API test returns expiry, seconds left and level
- [ ] 2.2 Add the same fields to the list reads under /api/server/tokens and /api/server/principals/{}/tokens. Check: API test returns a level for each token

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire the fields in the server and write the docs page with the rotation path. The tool-compatible carriage is BLOCKED by the carriage open question. Check: docs page commands run as written in a test
- [ ] 4.2 Add an e2e step that reads GET /api/server/caller/tokens with a personal token against the real binary over TLS. The CLI text step is BLOCKED by the carriage open question. Check: make e2e step passes
- [ ] 4.3 Run the full check and the usability check from the docs page alone. Check: `make check` reports zero findings
