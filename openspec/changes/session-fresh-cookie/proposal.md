---
version: 1
---

Issue: #3257.

## Why

Persona: a developer who approves a `destroy` or confirms a job, in an installation with `freshAuthEnabled` on.

The server must know how and when a person last gave a fresh authentication. On main a console session is a stored row (`session.Session`) with a provider and the time of the sign-in, but with no factor kind, and nothing else carries the fact (issue 3257, question 2: the owner chose a sealed session cookie). This change issues an access cookie (ADR 297 decision 1) next to the stored session at each sign-in, with the kind and the time of the fresh authentication, and reads it in `sessionauth`. The stored session stays the authority for who the person is. The cookie carries the fresh fact only. The change `fix-destroy-approval-fresh-auth` then passes the fact to the job service.

The path after this change:

1. A person signs in with a passkey, a password and a second factor, or an SSO provider. The server sets the session cookie as today and the access cookie with `freshKind` and `freshAt`.
2. For a step, the person signs in again (`/api/server/sign-in/{provider}?fresh=1` for SSO, the same sign-in for a local person). That sign-in issues a new access cookie with a new `freshAt`.
3. Each later request that the stored session authenticates reads the cookie. A missing, expired, forged or unbound cookie gives no fresh fact, never an error for the request.
4. Errors: none new. A request with no fact gets the refusals that exist today.

## Evidence

- `Checker.Authenticate` reads the stored session only (internal/sessionauth/sessionauth.go:132). The stored `session.Session` has `AuthTime` and `Provider` and no factor kind (pkg/session/session.go:123).
- The six places that set a session cookie (five sign-ins and the renewal at `localusers.go:741`): `signin.go:440`, `passwordsignin.go:259`, `passkeysignin.go:303`, `localusers.go:402` and `:741`, `devsignin.go:146` (internal/api/server, `sessionCookie(...)`).
- The claim types exist (`Claims`, `AccessClaims`, `FreshKind`, pkg/session/cookie.go:193-273). The sealer is the change `session-key-ring`.

## Fix boundary

- At each session start, the handler seals an access cookie with the claims below and sets `__Host-dustpan_access` (`session.AccessCookie`) with the attributes of ADR 297 decision 1.
- `sessionauth.Checker.Authenticate` opens the cookie and binds it to the stored session. It returns the fact in `Caller.Fresh`. The binding fails closed.
- The fresh fact is written only at a sign-in. A request never advances it.
- The access cookie lives at most `AccessLifetime` (5 minutes). A request that the stored session authenticates re-issues it near its expiry, with the same claims and a new expiry. It never changes `FreshAt`, `MFAAt` or `FreshKind`.

Out of scope:

- The refresh cookie, the refresh key and its proof, the sign-in ID as an authority, and the end of the stored session (ADR 297 decisions 3 to 8 and 12). The stored session stays the authority until a later change.
- The job service and the handlers of the approval (`fix-destroy-approval-fresh-auth`).

## Files

- `pkg/session/fresh.go` (NEW), `pkg/session/fresh_test.go` (NEW)
- `internal/sessionauth/sessionauth.go`, `internal/sessionauth/fresh_test.go` (NEW)
- `internal/api/server/signin.go`, `passwordsignin.go`, `passkeysignin.go`, `localusers.go`, `devsignin.go`, `internal/api/server/accesscookie.go` (NEW)
- `e2e/e2e_test.go`, `docs/guides/`

## Check

- `go test ./pkg/session ./internal/sessionauth ./internal/api/server -count=1`
- `make e2e` with a sign-in and a read of the fresh fact

## What main already does

- The sealer, the ring and the cookie claims types: ports and types only on main; the change `session-key-ring` implements them.
- `sessionCookie` sets the stored session cookie at each of the six sites.
- A step is a new sign-in with `fresh=1` that makes a new stored session (`freshSignIn`, internal/api/server/signin_test.go:1355).

## ADR check

- ADR 297 (owner direction) decision 1: the access cookie "holds ... the time of the last MFA step, and the time and the kind of the last fresh authentication (ADR 227): `passkey`, `mfa` ... `password` ... or `sso`. Each is zero or empty when none". This change fills those claims. "The expiry is the time of issue plus 5 minutes": kept.
- ADR 297 decision 2 (each request checks the cookie with no session read) is not met here: the stored session stays the authority. This change does not claim decision 2. A later change moves the authority to the cookie.
- ADR 227 decision 1 and ADR 301: which kinds are fresh. ADR 320: the check is "bound to the person of the session". The binding below follows it.
- ADR 354: an agent never has a fresh authentication. An agent and an access token get no cookie.
- ADR 250: each layer has a hostile test.

## Open questions

Open questions: none.

Considered and answered:

- Does a request refresh the fact? No. Only a sign-in writes `freshAt`. A re-issue copies the claims.
- What binds the cookie to the stored session? The principal ID, the session generation and the sign-in time (`SignInAt` equals the creation time of the stored session in milliseconds). Any difference gives no fact.
- Does a renewal after a password change carry a fact? No. It is not a sign-in. It sets empty claims.
- Which kind for which sign-in? Passkey gives `passkey`. A password with a second factor gives `mfa`. An SSO provider gives `sso` with the authentication time that the provider gives. A password alone gives `password`. A dev sign-in gives no kind.
