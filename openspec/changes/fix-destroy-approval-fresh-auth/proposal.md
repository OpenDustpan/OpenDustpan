---
version: 2
---

Issue: #3257.

## Why

Persona: a developer who approves a `destroy`, or who confirms their own job, in an installation that turned `freshAuthEnabled` on.

With the setting on, ADR 385 keeps the rules of ADR 227 decision 2: a destroy approval needs an MFA step in the last window, and a confirmation needs an MFA step or an SSO authentication in the last window. No route supplies that fact today. `approveJob` (internal/api/jobs/approve.go:172) and `confirmJob` (internal/api/jobs/confirm.go:77) build `job.Actor` with an empty `ConfirmAuth` and a zero AuthAt. So with the setting on, each destroy approval and each confirmation of an approve verdict fails closed with `ErrAuthStale`, for every person (issue 3257). The change of `fresh-auth-enabled-setting` removes the refusal with the setting off. This change removes it with the setting on.

The path after this change (setting on):

1. A person signs in. For a destroy approval the person gives a fresh step in the console (a passkey or TOTP, `/api/server/sign-in/{provider}?fresh=1` for an SSO person).
2. The person calls `POST /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}/approvals` (or `POST /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}/confirmations`) with the session cookie. The server reads the fresh step from the sealed session cookie and passes it to the job service.
3. The job service accepts the approval inside the window, and the audit entry names the kind and the time of the step.
4. Errors: no step, a step older than the window, an access token, an agent, or a step of a kind that the rule does not accept gets 403 with the existing `mfa.fresh-step-required` message and the sign-in path. It is the same refusal as today. No new code.

### Evidence

- Both handlers send an empty proof. Proof: `rg -n 'ConfirmAuth: ""' internal/api/jobs` shows internal/api/jobs/approve.go:172 and internal/api/jobs/confirm.go:77 on main (24 Nov 2026).
- The type of the fact exists, its source does not. `session.Claims` (pkg/session/cookie.go:226) has the time fields MFAAt and FreshAt and the field FreshKind, and `Sealer` (pkg/session/cookie.go:378) and `KeyStore` (pkg/session/cookie.go:444) are ports. On main no code issues a sealed access cookie: `rg -n 'SealAccess\(' --glob '*.go'` finds only the interface line. `Checker.Authenticate` (internal/sessionauth/sessionauth.go:109) reads a stored session (`c.sessions.Authenticate`), and the stored `session.Session` has no factor kind. So no request carries the fact today.
- The owner chose a sealed session cookie as the source of the fresh-auth fact (issue 3257, question 2; ADR 297). This change therefore depends on the change `session-fresh-cookie`, which issues the cookie at sign-in and at each step and reads it in `sessionauth`. That change is planned next. This change starts only when it is on main.
- The job service checks the proof three times and needs no change in its rules: `newDestroyFresh` (pkg/job/service_approve.go:380), `checkConfirmation` (pkg/job/service_confirm.go:126) and `checkDestroyProof` on the store clock (internal/store/jobstore/events.go:46).
- No session store is shared between instances. The fact is read from the cookie of the request, so it works behind any instance.

## Order of work

1. `session-fresh-cookie` (issue and read of the sealed cookie, with the factor kind and time).
2. `fresh-auth-enabled-setting`, task 2.4 (the approval route reads the policy; with the setting off it records `auth: none`). Merged in #3300 as a spec; its code lands first.
3. This change.

## Fix boundary

- A pure function in `pkg/session` maps the claims of a request to the proof of the job service. The jobs handlers call it. The job service and the store keep their checks.
- The map follows ADR 227 decision 1 and ADR 301: `passkey` and `mfa` give `ConfirmAuthMFA` with the time FreshAt; `sso` gives `ConfirmAuthSSO` with FreshAt; `password` and no kind give no proof.
- A request with an access token, with an agent, or with no session gives no proof. The service keeps its own refusal for each of them.
- With `freshAuthEnabled` off the handlers pass the proof too. The policy of `fresh-auth-enabled-setting` decides whether the service asks for it.

## Files

- `pkg/session/proof.go` (NEW) and `pkg/session/proof_test.go` (NEW)
- `internal/sessionauth/sessionauth.go`
- `internal/api/jobs/approve.go`, `internal/api/jobs/confirm.go`, `internal/api/jobs/jobs.go`
- `internal/api/jobs/approve_test.go`, `internal/api/jobs/confirm_redact_test.go`
- `e2e/e2e_test.go`
- `docs/guides/approval-rules.md`

## Check

- `go test ./pkg/session ./internal/api/jobs ./internal/sessionauth -count=1`
- `make e2e` with the step of a destroy approval by a session with a fresh step

## What main already does

- The service refuses a destroy approval with no MFA proof (`pkg/job/service_approve.go:380 (func newDestroyFresh)`)
- The service refuses a confirmation with no valid proof (`pkg/job/service_confirm.go:126 (func checkConfirmation)`)
- The store checks the age of the proof again on its own clock (`internal/store/jobstore/events.go:46 (func checkDestroyProof)`)
- The handler refuses every destroy approval with an approve verdict, with no regard to the session: it answers `ErrAuthStale` in a job that awaits approval. After task 2.4 of `fresh-auth-enabled-setting` it refuses only when the policy asks for the step (`internal/api/jobs/approve.go:116 (func approveJob)`)
- The confirmation handler sends an empty proof (`internal/api/jobs/confirm.go:45 (func confirmJob)`)
- The session checker resolves the stored session of the cookie and returns no factor kind (`internal/sessionauth/sessionauth.go:109 (func Authenticate)`)

## ADR check

- ADR 385 (owner, 2026-11-15): with the setting on, "the old rules". This change supplies the fact that those rules need and changes no rule. It depends on the change `fresh-auth-enabled-setting`.
- ADR 227 decision 1: "A fresh authentication is a built-in MFA step (ADR 46) or a new authentication at the SSO identity provider (ADR 71). It is fresh when its age is not above the window." Changed by ADR 301: "a passkey or TOTP is the fresh authentication of a person of a plain OAuth 2.0 provider or a trusted header." The map above is this text.
- ADR 227 decision 2 (owner): the window is one value for every step. The change reads it from the same setting.
- ADR 320 (extends ADR 227 decision 1): the check of a fresh authentication is "bound to the person of the session". The proof comes from the cookie of the person who signs the verdict, never from the body of the request.
- ADR 354 (extends ADR 227): "an agent never has a fresh authentication". The handler gives an agent no proof.
- ADR 250 (defense in depth): the handler, the service and the store each check the proof. A test for each layer.

## Open questions

Open questions: none.

Considered and answered:

- Does a `password` step count? No. ADR 227 decision 1 lists only an MFA step and an SSO authentication. A person with no factor and no SSO provider gets the refusal and the enrollment path that exist today.
- Does an SSO proof satisfy a destroy approval? No. `newDestroyFresh` needs MFA and stays as it is. A person of an SSO provider with no built-in factor turns the setting off or enrolls a factor (ADR 301). Not a change of this proposal.
- Does the client send the step in the body? No. A body field is untrusted. The cookie is sealed by the server.
