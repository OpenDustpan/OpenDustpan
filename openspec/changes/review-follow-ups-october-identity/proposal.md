---
version: 1
---

Issue: #2711.

## Problem

As site administrator, I keep sign-in and access decisions auditable: I need each step-up refusal to name its error, its state effect, and the one layer that audits it, so a review shows exactly one entry per refusal.

## Evidence

- The step-up package exists on main. Proof: internal/stepup/stepup.go
- The gate service exists on main. Proof: pkg/gate/service.go
- The error-order test exists on main. Proof: internal/errcode/errcode_test.go
- The step-up RFC is discussing and decides nothing. Proof: docs/rfc/0102-cli-step-up-for-writes-that-need-a-fresh-authentication.md

## Fix boundary

- Specify the errors and the count, state, and audit effects of RecordFailure for missing, expired, and non-open challenges.
- Make CreateChallenge accept only session and access-token holder kinds with ErrInvalid otherwise.
- Assign one auditing layer to each of the five refusal errors.
- Store a TOTP reservation, bind key enrollment to its challenge, and add an atomic reset.
- Split the CSRF and other-site cases of TestForbiddenOrder and propagate unexpected CheckAccess errors with no added denial.

Out of scope:

- Changes to sign-in, tokens, or sessions.
- New routes, error codes, or audit kinds.

## Check

- Write the NEW test TestRecordFailureStates in the NEW file internal/stepup/contract_test.go for missing, expired, and non-open challenges. Check: The NEW test `TestRecordFailureStates` fails on main via `go test ./internal/stepup/ -run TestRecordFailureStates -count=1`.
- Write the NEW test TestCreateChallengeKinds in the NEW file internal/stepup/contract_test.go for session, access-token, empty, and unsupported kinds with nothing stored on refusal. Check: The NEW test `TestCreateChallengeKinds` fails on main via `go test ./internal/stepup/ -run TestCreateChallengeKinds -count=1`.
- Write the NEW test TestStepupAuditLayer in the NEW file internal/stepup/contract_test.go for exactly one entry per refusal error. Check: The NEW test `TestStepupAuditLayer` fails on main via `go test ./internal/stepup/ -run TestStepupAuditLayer -count=1`.
- Write the NEW tests TestTOTPReservation and TestTOTPConcurrentConsume in the NEW file internal/stepup/reservation_test.go for fabricated, reused, higher-step, and concurrent reservations. Check: The NEW tests `TestTOTPReservation` and `TestTOTPConcurrentConsume` fail on main via `go test ./internal/stepup/ -run TestTOTPReservation -count=1`.
- Write the NEW tests TestKeyAddBinding and TestAtomicReset in the NEW file internal/stepup/reservation_test.go for reuse after revocation, failed enrollment, one reset entry, and injected transaction failure. Check: The NEW tests `TestKeyAddBinding` and `TestAtomicReset` fail on main via `go test ./internal/stepup/ -run TestKeyAddBinding -count=1`.
- Write the NEW test TestCheckAccessPropagatesUnexpected in pkg/gate/service_test.go for an unexpected CheckAccess error with no added denial. Check: The NEW test `TestCheckAccessPropagatesUnexpected` fails on main via `go test ./pkg/gate/ -run TestCheckAccessPropagatesUnexpected -count=1`.
- Split TestForbiddenOrder in internal/errcode/errcode_test.go into separate CSRF-versus-denied and other-site-versus-denied cases with no joint case. Check: `go test ./internal/errcode/ -run TestForbiddenOrder -count=1` passes after the split.
- Fix the contracts in internal/stepup/stepup.go and pkg/gate/service.go to meet the requirements. Check: `go test ./internal/stepup/ ./internal/errcode/ ./pkg/gate/ -count=1` passes.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has a step-up helper around RecordFailure and challenge checks. (`internal/stepup/stepup.go:588 (func CheckKeyAdditionChallenge)`)
- Main has CreateChallenge in the mfa port. (`pkg/mfa/mfa.go:234`)
- Main has Service.CheckAccess in the gate service. (`pkg/gate/service.go:297 (func Service.CheckAccess)`)
- Main has TestForbiddenOrder. (`internal/errcode/errcode_test.go:26`)
- Main defines stepup ErrMismatch. (`internal/stepup/stepup.go:57`)
- Main defines stepup ErrFactorRefused. (`internal/stepup/stepup.go:63`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Which ADR decides step-up challenge handling and error precedence? The pack lists no accepted ADR for step-up or errcode ordering. RFC 102 is discussing and decides nothing, so adr_check stays empty and the issue text decides the contracts.
- What does item 8 require when CheckAccess returns an unexpected error? The issue text says to propagate unexpected CheckAccess errors without adding identity.ErrDenied and to keep denial only for access-limit refusals. The change follows that sentence and adds a hostile test.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
