## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. tests

- [ ] 1.1 Add failing route tests for the three routes in internal/api/server: cross-site text/plain, missing Content-Type, origin before decode, malformed JSON, and finish retry. Check: `go test ./internal/api/server -run TestDevCodeOriginRefused|TestPasskeyBeginOriginRefused|TestPasskeyFinishOriginRefused` calls POST /api/dustpan/v1/sign-in/dev-code, POST /api/dustpan/v1/sign-in/passkey/begin and POST /api/dustpan/v1/sign-in/passkey/finish through the SignInFromBody origin gate with no session and no access token on the public path; the tests fail on main and pass after.
- [ ] 1.2 Fix the dev-code e2e client in the NEW file e2e/devcode_signin_test.go to send Content-Type application/json. Check: No route: the test checks the NEW e2e client file content only. Confirmed by: CI (the agent writes the e2e test).

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. definition of done

- [ ] 3.1 Wire the check into the three public sign-in routes in internal/api/server, with authorization unchanged. Check: Calls POST /api/dustpan/v1/sign-in/dev-code, POST /api/dustpan/v1/sign-in/passkey/begin and POST /api/dustpan/v1/sign-in/passkey/finish through the SignInFromBody origin gate with no session and no access token on the public path and keeps stored-grant authorization unchanged.
- [ ] 3.2 Run a same-site e2e step against the real server for the three routes. Confirmed by: CI (sign-in e2e). Check: Calls POST /api/dustpan/v1/sign-in/dev-code, POST /api/dustpan/v1/sign-in/passkey/begin and POST /api/dustpan/v1/sign-in/passkey/finish through the SignInFromBody origin gate with no session and no access token on the public path from a same-site client and each passes the origin check.
- [ ] 3.3 Run the usability check from the sign-in docs page. A person's step. Check: No route: the test reads the sign-in docs page only.
- [ ] 3.4 Run make check with zero findings. Confirmed by: CI (make check). Check: `make check` with zero findings.
