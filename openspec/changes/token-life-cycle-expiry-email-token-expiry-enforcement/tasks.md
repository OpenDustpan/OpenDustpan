## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add a test that the authenticator refuses an idle-disabled token Check: NEW TestAuthenticateRefusesDisabledToken in pkg/accesstoken fails on main
- [x] 1.2 Add a table test in pkg/accesstoken/accesstoken_test.go that the response bytes and headers are identical for unknown, expired, revoked and disabled tokens Check: NEW TestRefusalIsIdenticalForEveryEndedCase fails on main
- [ ] 1.3 Add a test in pkg/accesstoken/accesstoken_test.go, through the code path, that the digest compare uses subtle.ConstantTimeCompare Check: NEW TestTokenCompareUsesConstantTime fails on main
- [ ] 1.4 Add an API test in NEW internal/api/server/token_refusal_test.go for the plain 401 with an audit reason on the three route families, and an unknown token entry with no token ID Check: NEW TestEndedTokenAnswers401WithAuditReason fails on main
- [ ] 1.5 Add a test in NEW pkg/accesstoken/limits_test.go that the settings resource shows the token limits Check: NEW TestSettingsShowTokenLimits fails on main
- [ ] 1.6 Add a test in NEW internal/api/server/tokens_ended_test.go that the ended list shows an expired and a revoked token Check: NEW TestCallerTokensStatusEndedListsEndedTokens fails on main

## 2. Implementation

- [x] 2.1 Add the shared refusal function in pkg/accesstoken/service.go (NEW function refuse, used by Service.AuthenticateToken at line 637) and route each 401 case through it Check: `go test ./pkg/accesstoken/...` passes
- [ ] 2.2 Write the audit entry with reason and token ID on each refusal, in the NEW refuse function in pkg/accesstoken/service.go Check: `go test ./internal/api/...` passes
- [ ] 2.3 Show the maximum lifetime, the inactivity period and the overlap with their source in the settings resource, in NEW pkg/accesstoken/limits.go Check: `GET /api/dustpan/v1/settings` lists each token limit
- [ ] 2.4 Document the status=ended filter in the List and read tokens section of docs/guides/access-tokens.md Check: `make docs-lint` passes and a docs test runs the command as written

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Check the route actions Check: Route test lists `ANY /api/dustpan/v1/me/tokens` and `ANY /api/server/tokens` with their actions
- [ ] 4.2 Run an e2e step over TLS on SQLite and PostgreSQL: create a personal access token through `ANY /api/dustpan/v1/me/tokens`, then run pulumi up and tofu apply with that token Check: e2e runs both CLIs with the token past its expiry and checks 401 and the audit entries. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.3 Write the docs page in the same pull request: edit docs/guides/access-tokens.md (exists on main) Check: The access tokens guide documents expiry, lifetime, inactivity disable, the rotation overlap and the status filter. `make docs-lint` passes on each edited guide.
- [ ] 4.4 Run the usability check Check: A Developer completes create, rotate, CLI use and the ended list from the docs page alone. A person's step.
- [ ] 4.5 Run the full check Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
