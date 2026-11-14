## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing limiter test for AllowRefusal: per-principal bucket, global caps, and summary entry Check: TestRefusalLimiterDropsOverLimit in internal/api/server fails on main
- [ ] 1.2 Add failing server test for PATCH /api/dustpan/v1/me/preferences over-limit refusal with access token Check: TestPreferencesRefusalOverLimitKeepsAnswer using PATCH /api/dustpan/v1/me/preferences with access token in internal/api/server fails on main
- [ ] 1.3 Add failing server test for over-limit refusal on the existing token route handler Handler.tokenRoutes, called with an access token, passing the existing route authorizer Check: TestTokenRefusalOverLimitKeepsAnswer driven by Handler.tokenRoutes in internal/api/server/tokens.go with access token fails on main

## 2. Implementation

- [ ] 2.1 Implement NEW internal/api/server/refusallimit.go refusalLimiter with fixed allowlist, per-process LRU table, global caps, summary entry, and metric hook, and wire it into the refusal audit path Check: go test -race ./internal/api/server/... for PATCH /api/dustpan/v1/me/preferences with access token

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire route with existing authorization actions for PATCH /api/dustpan/v1/me/preferences with access token, add e2e refusal burst step with access token, update docs page, run usability check from docs page alone Check: PATCH /api/dustpan/v1/me/preferences refusal burst with access token stores at most the limit plus one summary entry; docs page states the limits; `make check` with zero findings
