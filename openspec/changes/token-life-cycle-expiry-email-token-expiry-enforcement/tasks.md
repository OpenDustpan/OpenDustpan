## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add a test that the authenticator refuses an idle-disabled token Check: NEW TestAuthenticateRefusesDisabledToken in pkg/accesstoken fails on main
- [ ] 1.2 Add an API test for 401 with audit entries on the three route families Check: NEW TestEndedTokenAnswers401WithAudit fails on main
- [ ] 1.3 Add a test that the settings resource shows the token limits Check: NEW TestSettingsShowTokenLimits fails on main

## 2. Implementation

- [ ] 2.1 Map the refusal to 401 with audit entries on all routes Check: `go test ./internal/api/...` passes
- [ ] 2.2 Show the maximum lifetime, the inactivity period and the overlap with their source Check: `GET /api/dustpan/v1/settings` lists each token limit

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Check the route actions Check: Route test lists `ANY /api/server/caller/tokens` and `ANY /api/server/tokens` with their actions
- [ ] 4.2 Run an e2e step with the real CLIs over TLS on SQLite and PostgreSQL, using a personal access token as bearer credential on `POST /token` and `ANY /api/server/tokens` Check: e2e runs pulumi up and tofu apply with an ended token and checks 401 and the audit entries
- [ ] 4.3 Write the docs page in the same pull request Check: The token life cycle page documents expiry, lifetime, inactivity disable and the 24-hour rotation overlap
- [ ] 4.4 Run the usability check Check: A Developer completes create, rotate and CLI use from the docs page alone
- [ ] 4.5 Run the full check Check: `make check` reports zero findings
