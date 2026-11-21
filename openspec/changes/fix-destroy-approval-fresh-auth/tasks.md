## Order of work

This change starts after `session-fresh-cookie` (the source of the proof) and task 2.4 of `fresh-auth-enabled-setting` are on main.

## 0. Confirm against the code first

- [ ] 0.0 Stop with BLOCKED when a dependency is not on main. Run `ls -d openspec/changes/archive/*-session-fresh-cookie && rg -q 'freshstep\.' internal/api/jobs/approve.go`. Check: the command exits 0. When it exits non-zero, write `BLOCKED: session-fresh-cookie is not archived on main, or approve.go has no freshstep gate (task 2.4 of fresh-auth-enabled-setting)` as the report, change no file and open no pull request
- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing table test TestProofOf in the NEW file pkg/session/proof_test.go, which calls the NEW `ProofOf` of task 2.1 (it does not compile on main): passkey, mfa, sso, password, none, unknown kind, zero time, future time. Check: `go test ./pkg/session -run TestProofOf -count=1` fails on main: the NEW symbol `ProofOf` is not defined on main
- [ ] 1.2 SECURITY. Add failing hostile tables, one for each layer of design.md, in `internal/api/jobs/approve_test.go` and `internal/api/jobs/confirm_redact_test.go` (forged cookie, body field, header, access token, agent, stale, SSO for a destroy). For each guard, show that the test fails when the guard is removed. Check: `go test ./internal/api/jobs -run 'TestApproveProof|TestConfirmProof' -count=1` fails on main
- [ ] 1.2a SECURITY. Add failing hostile tables for the layers below the handler, each in its own test and each failing when its guard is removed: `TestCallerProofHostile` in `internal/sessionauth/sessionauth_test.go` (cookie of another person, cookie of an older generation, expired cookie, cookie that names another sign-in, a stored session with no cookie); `TestDestroyFreshHostile` in `pkg/job/service_approve_test.go` (token, agent, non-person, SSO proof for a destroy, proof after the approval time, proof older than the window); `TestCheckDestroyProofHostile` in `pkg/store/storetest` for each adapter (proof older than the window on the store clock, proof after the store time, `auth` other than `mfa`). Check: each test fails on main or with its guard removed
- [ ] 1.2b Add failing test TestApprovalAuditNamesProof in `internal/api/jobs/approve_test.go` and `pkg/job/jobtest`: the audit entry of an accepted approval names the kind (`mfa` or `sso`) and the time of the step, and the entry of a refusal names the reason and no proof value. Check: `go test ./internal/api/jobs ./pkg/job -run TestApprovalAuditNamesProof -count=1` fails on main
- [ ] 1.3 Add failing test TestApproveProofIgnoredWithSettingOff in `internal/api/jobs/approve_test.go`: with the setting off the approval needs no step, with the setting on it needs the step. Check: `go test ./internal/api/jobs -run TestApproveProofIgnoredWithSettingOff -count=1` fails on main

## 2. Fix

- [ ] 2.1 In the NEW file pkg/session/proof.go, add `Proof`, `ProofKind` and `ProofOf` as in design.md. Check: `go test ./pkg/session -count=1` passes
- [ ] 2.2 In `internal/sessionauth/sessionauth.go`, add the field `Proof` to `Caller` and set it in `Checker.Authenticate` from the claims that the reader of `session-fresh-cookie` returns for the cookie of the request, bound to the person, the generation and the sign-in of the stored session. A missing, expired or unbound cookie gives the zero proof and never an error. Add the field to each literal of `sessionauth.Caller` that exhaustruct names. Check: `go test ./internal/sessionauth -count=1` passes and `golangci-lint run ./internal/sessionauth/...` is clean
- [ ] 2.3 In `internal/api/jobs/approve.go:161` and `internal/api/jobs/confirm.go:73`, set `ConfirmAuth` and `AuthAt` of the actor from the proof of the session of a person, and remove the comments that say that no part of the API knows the step. Check: `go test ./internal/api/jobs -count=1` passes
- [ ] 2.3a In `internal/api/jobs/approve.go:143-165`, replace the unconditional refusal of a destroy approval by a check that asks `freshstep.Policy.Required` (task 2.4 of `fresh-auth-enabled-setting`): with the step required and no valid proof in the actor, answer the existing refusal; otherwise pass the actor to the service, which decides. The approval of a `destroy` then reaches the service. Check: `go test ./internal/api/jobs -run TestApproveProofIgnoredWithSettingOff -count=1` passes
- [ ] 2.4 Add one comment on each redundant check that says that it is a defense-in-depth layer and names the layers it does not rely on. Check: `rg -n 'Defense in depth' internal/api/jobs/approve.go internal/api/jobs/confirm.go` shows each

## 3. Side effects (computed from main)

- [ ] 3.1 Not applicable: this change adds no route, so `internal/api/apidesc/openapi.json` and the clients do not change. Check: `make clients-check` is clean.
- [ ] 3.2 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.

## 4. Definition of done

- [ ] 4.1 Add the e2e step in `e2e/e2e_test.go`: with `freshAuthEnabled` on, a person with a session that has a fresh step approves a `destroy` through `POST /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}/approvals` with the real CLI flow, and a person with a stale step gets 403. Check: `make e2e` passes the step
- [ ] 4.2 Update the existing file docs/guides/approval-rules.md with the fresh step of the session and the refusal text. Check: `make docs-lint` is clean
- [ ] 4.3 Run the usability check from the docs page and `make check`. Check: `make check` with zero findings.
