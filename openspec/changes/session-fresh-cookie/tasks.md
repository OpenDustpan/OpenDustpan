## 0. Confirm against the code first

- [ ] 0.0 Stop with BLOCKED when the sealer is not on main. Run `ls -d openspec/changes/archive/*-session-key-ring`. Check: the command exits 0. When it exits non-zero, write `BLOCKED: session-key-ring is not archived on main` as the report, change no file and open no pull request
- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing table tests TestClaimsOf and TestBindFact in the NEW file pkg/session/fresh_test.go, which call the NEW `ClaimsOf` and `BindFact` of task 2.1 (they do not compile on main): each method of sign-in, each mismatch of the binding. Check: `go test ./pkg/session -run 'TestClaimsOf|TestBindFact' -count=1` fails on main: the NEW symbols are not defined on main
- [ ] 1.2 SECURITY. Add failing hostile tables, one for each layer of design.md: `TestBindFactHostile` in pkg/session/fresh_test.go, `TestCallerFreshHostile` in the NEW file internal/sessionauth/fresh_test.go (forged value, value of another kind, cookie with no stored session, cookie of another person), `TestAccessCookieHandlerHostile` in the NEW file internal/api/server/accesscookie_test.go (failed sign-in, token, agent), and `TestReissueKeepsFreshAt` and `TestReissueOnlyAcceptedCookie` (a re-issue seals a cookie only when `BindFact` accepted it: a forged, expired or unbound cookie gets no new cookie) in the same file, `TestSSOAuthTimeHostile` in internal/api/server/accesscookie_test.go (an SSO authentication time that is unknown, absent, in the future or older than `session.MaxTokenFreshness` never gives a time of now or a kind `sso`: the fact is empty), and `TestRenewSessionNoFreshFact` (a renewal after a password change sets an access cookie with no kind and no times, and drops any fact of the earlier cookie). For each guard, show that the test fails when the guard is removed. Check: `go test ./pkg/session ./internal/sessionauth ./internal/api/server -run 'Hostile|TestReissue|TestRenewSessionNoFreshFact' -count=1` fails on main
- [ ] 1.3 Add failing test TestSignInSetsAccessCookie in internal/api/server/accesscookie_test.go for each of the five sign-in sites: the response sets both cookies, with the attributes of ADR 297 decision 1 and the kind of its method. The sixth site, `renewSession`, sets an access cookie with empty claims. Check: `go test ./internal/api/server -run TestSignInSetsAccessCookie -count=1` fails on main

## 2. Fix

- [ ] 2.1 In the NEW file pkg/session/fresh.go, add `FreshFact`, `ClaimsOf` and `BindFact` as in design.md. Check: `go test ./pkg/session -count=1` passes
- [ ] 2.2 In internal/sessionauth/sessionauth.go, add the field `Fresh` to `Caller` and set it in `Checker.Authenticate` from `BindFact` of the opened cookie, after the stored session authenticated. A missing, expired or unbound cookie gives the zero fact and no error. Add the field to the literal of `Caller` at :157. Check: `go test ./internal/sessionauth -count=1` passes
- [ ] 2.3 In the NEW file internal/api/server/accesscookie.go, add `setAccessCookie` and call it next to `sessionCookie` at internal/api/server/signin.go:440, passwordsignin.go:259, passkeysignin.go:303, localusers.go:402 and devsignin.go:146. At localusers.go:741 (`renewSession`, after a password change) call it with empty claims: a renewal is not a sign-in and never mints or carries a fresh fact. The SSO time comes only from the authentication time that the provider gave, and `ClaimsOf` gives an empty fact when that time is zero, in the future, or older than `session.MaxTokenFreshness`. Check: `go test ./internal/api/server -run TestSignInSetsAccessCookie -count=1` passes
- [ ] 2.4 In internal/api/server/accesscookie.go, add the re-issue near the expiry for a request that the stored session authenticated, with the claims of the opened cookie and a new expiry. Check: `go test ./internal/api/server -run TestReissueKeepsFreshAt -count=1` passes
- [ ] 2.5 Add one comment on each redundant check that says that it is a defense-in-depth layer and names the layers it does not rely on. Check: `rg -n 'Defense in depth' pkg/session/fresh.go internal/sessionauth/sessionauth.go internal/api/server/accesscookie.go` shows each

## 3. Side effects (computed from main)

- [ ] 3.1 Not applicable: this change adds no route, so `internal/api/apidesc/openapi.json` and the clients do not change. Check: `make clients-check` is clean.
- [ ] 3.2 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.

## 4. Definition of done

- [ ] 4.1 Add the e2e step in e2e/e2e_test.go: a local person signs in with a passkey and gets both cookies, and a request with the access cookie of another person gets no fresh fact. Check: `make e2e` passes the step
- [ ] 4.2 Update the existing file docs/guides/approval-rules.md with the fresh fact of the session and how a step is made. Check: `make docs-lint` is clean
