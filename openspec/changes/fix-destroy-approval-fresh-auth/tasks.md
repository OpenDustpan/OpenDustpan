## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing table test TestProofOf in the NEW file pkg/session/proof_test.go, which calls the NEW `ProofOf` of task 2.1 (it does not compile on main): passkey, mfa, sso, password, none, unknown kind, zero time, future time. Check: `go test ./pkg/session -run TestProofOf -count=1` fails on main: the NEW symbol `ProofOf` is not defined on main
- [ ] 1.2 SECURITY. Add failing hostile tables, one for each layer of design.md, in `internal/api/jobs/approve_test.go` and `internal/api/jobs/confirm_redact_test.go` (forged cookie, body field, header, access token, agent, stale, SSO for a destroy). For each guard, show that the test fails when the guard is removed. Check: `go test ./internal/api/jobs -run 'TestApproveProof|TestConfirmProof' -count=1` fails on main
- [ ] 1.3 Add failing test TestApproveProofIgnoredWithSettingOff in `internal/api/jobs/approve_test.go`: with the setting off the approval needs no step, with the setting on it needs the step. Check: `go test ./internal/api/jobs -run TestApproveProofIgnoredWithSettingOff -count=1` fails on main

## 2. Fix

- [ ] 2.1 In the NEW file pkg/session/proof.go, add `Proof`, `ProofKind` and `ProofOf` as in design.md. Check: `go test ./pkg/session -count=1` passes
- [ ] 2.2 In `internal/sessionauth/sessionauth.go`, add the field `Proof` to `Caller` and set it in `Checker.Authenticate` from the opened cookie. Add the field to each literal of `sessionauth.Caller` that exhaustruct names. Check: `go test ./internal/sessionauth -count=1` passes and `golangci-lint run ./internal/sessionauth/...` is clean
- [ ] 2.3 In `internal/api/jobs/approve.go:161` and `internal/api/jobs/confirm.go:73`, set `ConfirmAuth` and `AuthAt` of the actor from the proof of the session of a person, and remove the comments that say that no part of the API knows the step. Check: `go test ./internal/api/jobs -count=1` passes
- [ ] 2.4 Add one comment on each redundant check that says that it is a defense-in-depth layer and names the layers it does not rely on. Check: `rg -n 'Defense in depth' internal/api/jobs/approve.go internal/api/jobs/confirm.go` shows each

## 3. Side effects (computed from main)

- [ ] 3.1 Not applicable: this change adds no route, so `internal/api/apidesc/openapi.json` and the clients do not change. Check: `make clients-check` is clean.
- [ ] 3.2 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.

## 4. Definition of done

- [ ] 4.1 Add the e2e step in `e2e/e2e_test.go`: with `freshAuthEnabled` on, a person with a session that has a fresh step approves a `destroy` through `POST /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}/approvals` with the real CLI flow, and a person with a stale step gets 403. Check: `make e2e` passes the step
- [ ] 4.2 Update the existing file docs/guides/approval-rules.md with the fresh step of the session and the refusal text. Check: `make docs-lint` is clean
- [ ] 4.3 Run the usability check from the docs page and `make check`. Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
