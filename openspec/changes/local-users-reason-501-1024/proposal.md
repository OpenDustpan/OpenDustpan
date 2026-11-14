---
version: 1
---

Issue: #2274, #2457, #2484.

## Problem

A site administrator manages local users with a reason for each change. A reason of 501 characters answers 500 or 503, and a longer reason answers that no reason was given, so the administrator cannot fix the input.

## Evidence

- A reason of 501 to 1024 characters on create answers 500 server.internal, and unlock with 501 answers 503 local-user.unavailable. Proof: Issue #2274 table: 501 to 1024 answers 500 server.internal internal error on create; unlock with 501 answers 503 local-user.unavailable.
- A reason of 1025 and more answers 400 local-user.reason-required with the wrong text. Proof: Issue #2274 table: 1025 and more answers 400 local-user.reason-required the change needs a reason.
- The create rolls back with no user and no audit entry. Proof: Issue #2274: The create rolls back (no user, no audit entry), so no partial state.

## Fix boundary

- Refuse a local-user create or unlock reason above 500 Unicode code points with 400 NEW error code `local-user.reason-too-long` that names the limit.
- Keep a 500-rune reason accepted and keep the missing-reason answer as local-user.reason-required.
- Write no user and no audit entry on refusal; change no lock state on refused unlock.
- State the 500 Unicode code point limit in the guide.

Out of scope:

- Body-size mapping for namespaces, restores, sign-in, and principals from #2457.
- Runner pool field-specific validation messages from #2484.
- Server log cause text for failed requests.

## Check

- Add failing server tests in pkg/dustpan/localusers_test.go:203 for create reasons of 500, 501, 1024, and 1025 runes that call route `POST /api/dustpan/v1/local-users` with credential `access token` through the authorization gate Check: POST /api/dustpan/v1/local-users with 501 runes fails on main with 500 in pkg/dustpan/localusers_test.go:203.
- Add failing server tests in pkg/dustpan/localusers_test.go:203 that call route `POST /api/dustpan/v1/local-users/unlock` with credential `access token` through the authorization gate and show an overlong reason changes no state Check: Route `POST /api/dustpan/v1/local-users/unlock` with 501 and 1025 runes fails on main with credential `access token` through the authorization gate.
- Add failing multibyte boundary test in pkg/dustpan/localusers_test.go:203 with 500 multibyte Unicode code points Check: Multibyte reason of 500 runes passes the length check.
- Use one shared 500-rune limit in the create and unlock paths in pkg/dustpan/localusers.go:206 after auth and before any store write Check: POST /api/dustpan/v1/local-users with 501-rune reason answers 400 local-user.reason-too-long.
- Add the NEW error code entry, its spec, and its catalog test in internal/errcode/catalog_localuser.go:33 with text that names the rune limit and the rerun command Check: Catalog test passes for `local-user.reason-too-long`.
- Update the local-user guide page in docs/guides/local-users.md (NEW if absent) with the 500 Unicode code point limit and the error code Check: make docs-lint passes and the page states the 500-rune limit.
- Search the other local-user reason paths in pkg/dustpan/localusers.go:206 for the same length bug and file a follow-up when it shows; BLOCKED by unlisted disable, delete, and reset reason routes on main Check: `rg -n reason pkg/dustpan/localusers.go` reviewed and follow-up filed when the same bug shows.
- Add e2e step in e2e/coverage_signin.go:138 against route `POST /api/dustpan/v1/local-users` and route `POST /api/dustpan/v1/local-users/unlock` with credential `access token` through the authorization gate. Confirmed by: CI (make e2e) Check: e2e step covers create and unlock overlong reasons. Confirmed by: CI (make e2e)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Local-user reason-required and unavailable codes exist with specs. (`internal/errcode/catalog_localuser.go:33 (func localUserSpecs)`)
- Local password routes have server tests. (`pkg/dustpan/localusers_test.go:203 (func TestLocalPasswordRoutes)`)
- Reason round-trip is covered in the audit suite. (`pkg/audit/audittest/audittest.go:935 (func testReasonRoundTrip)`)
- The local-user service constructor exists. (`pkg/dustpan/localusers.go:206 (func newLocalUserService)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Should a long reason be truncated instead of refused? No. The audit log stores the reason verbatim. Truncation would record text the caller did not write. Refuse input above the limit with a code that names the limit.
- Is the 500 proven to come from the audit append limit? Not proven in the pack. Issue #2274 says it looks like the audit append refuses a reason above 500 characters. This change validates at the service boundary before any store write.
- Should this change also fix body-size and runner messages? No. Issues #2457 and #2484 describe other routes. They stay out of scope.
- Is the 500 limit counted in characters or bytes? 500 Unicode code points (runes). A multibyte boundary test covers 500 runes.
- Do other local-user reason routes share the same bug? Only create and unlock change here. A search task checks the other local-user paths and files a follow-up if the bug shows.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
