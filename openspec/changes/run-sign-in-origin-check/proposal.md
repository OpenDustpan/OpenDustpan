---
version: 1
---

Issue: #2983.

## Problem

Sign-in routes that start a session from a body must share one origin boundary. Three routes skip the shared check today, so a later change can open a cross-site gap without notice.

## Evidence

- The shared check exists as Checker.SignInFromBody. Proof: internal/sessionauth/sessionauth.go:189 (func Checker.SignInFromBody)
- The passkey finish handler admits a session through admitPasskeyFinish. Proof: internal/api/server/passkeysignin.go:135 (func Handler.admitPasskeyFinish)
- Sessions start through signinsvc startSession. Proof: internal/signinsvc/service.go:559 (func Service.startSession)

## Fix boundary

- Run sessionauth Checker SignInFromBody on POST sign-in/dev-code, POST sign-in/passkey/begin, and POST sign-in/passkey/finish.
- Refuse a cross-site text/plain request with no cookie and no spend on each of the three routes.
- Send Content-Type application/json from the dev-code e2e client.

Out of scope:

- Change of the password route behavior
- Change of the origin check rules
- New audit record for a refused request
- New rate limit for sign-in routes

## Check

- Add failing route tests for the three routes in internal/api/server: cross-site text/plain, missing Content-Type, origin before decode, malformed JSON, and finish retry. Check: `go test ./internal/api/server -run TestDevCodeOriginRefused|TestPasskeyBeginOriginRefused|TestPasskeyFinishOriginRefused` calls POST /api/dustpan/v1/sign-in/dev-code, POST /api/dustpan/v1/sign-in/passkey/begin and POST /api/dustpan/v1/sign-in/passkey/finish through the SignInFromBody origin gate with no session and no access token on the public path; the tests fail on main and pass after.
- Fix the dev-code e2e client in the NEW file e2e/devcode_signin_test.go to send Content-Type application/json. Check: No route: the test checks the NEW e2e client file content only.
- Wire the check into the three public sign-in routes in internal/api/server, with authorization unchanged. Check: Calls POST /api/dustpan/v1/sign-in/dev-code, POST /api/dustpan/v1/sign-in/passkey/begin and POST /api/dustpan/v1/sign-in/passkey/finish through the SignInFromBody origin gate with no session and no access token on the public path and keeps stored-grant authorization unchanged.
- Run a same-site e2e step against the real server for the three routes. Confirmed by: CI (sign-in e2e). Check: Calls POST /api/dustpan/v1/sign-in/dev-code, POST /api/dustpan/v1/sign-in/passkey/begin and POST /api/dustpan/v1/sign-in/passkey/finish through the SignInFromBody origin gate with no session and no access token on the public path from a same-site client and each passes the origin check.
- Run the usability check from the sign-in docs page. A person's step. Check: No route: the test reads the sign-in docs page only.
- Run make check with zero findings. Confirmed by: CI (make check). Check: `make check` with zero findings.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Shared sign-in origin check exists. (`internal/sessionauth/sessionauth.go:189 (func Checker.SignInFromBody)`)
- Session start path exists in the sign-in service. (`internal/signinsvc/service.go:559 (func Service.startSession)`)
- Passkey finish admission path exists. (`internal/api/server/passkeysignin.go:135 (func Handler.admitPasskeyFinish)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does any ADR in the pack govern the sign-in origin check? The pack has no ADR text. The adrs list is empty, so no decision line can be quoted. The change reuses the existing check without changing its rules.
- Does the change need new audit or rate-limit behavior? No. The issue asks only for one boundary. The change keeps error code sign-in.failed and adds no audit record or rate limit.
- Do the three routes need new authorization actions? No. Sign-in routes are public before a session starts, and authorization stays unchanged.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
