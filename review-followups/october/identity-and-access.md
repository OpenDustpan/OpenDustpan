# Review follow-ups (October): identity and access

## 1. Specify RecordFailure's errors and failure-count, state, and audit effects for
PR #1319

- Files: internal/stepup/stepup.go:434
- Change: Specify RecordFailure's errors and failure-count, state, and audit effects for missing, expired, and non-open challenges.
- Acceptance: The method contract explicitly identifies the error and whether any mutation or audit append occurs for each state.

## 2. Require CreateChallenge to accept only session and access-token holder kinds
PR #1319

- Files: internal/stepup/stepup.go:394
- Change: Require CreateChallenge to accept only session and access-token holder kinds and return ErrInvalid for empty or unsupported kinds.
- Acceptance: Conformance cases cover both supported kinds and empty and unsupported kinds, checking that rejected challenges are not stored.

## 3. Document service responsibility for auditing ErrNotFound, ErrExpired, and
PR #1319

- Files: internal/stepup/stepup.go:443; docs/rfc/0102-cli-step-up-for-writes-that-need-a-fresh-authentication.md:1051
- Change: Document service responsibility for auditing ErrNotFound, ErrExpired, and ErrUsed refusals. Preserve store responsibility for ErrMismatch and ErrFactorRefused.
- Acceptance: The contract assigns exactly one auditing layer to each of the five refusal errors and explicitly prevents duplicate recording.

## 4. Have BeginTOTPAttempt issue a store-recorded reservation and require AcceptStep
PR #1319

- Files: internal/stepup/stepup.go:490; internal/stepup/stepup.go:516; internal/stepup/stepup.go:527
- Change: Have BeginTOTPAttempt issue a store-recorded reservation and require AcceptStep to validate and atomically consume it. Preserve acceptance of legitimately reserved attempts despite locks or disables set by that attempt or concurrent attempts.
- Acceptance: Tests reject fabricated reservations and reuse with a higher step without changing factor state. Valid reservations still pass under the documented lock and disable rules.

## 5. Bind AddKey and EnrollFirstKey to their key-add challenge and atomically mark
PR #1319

- Files: internal/stepup/stepup.go:369; internal/stepup/stepup.go:379
- Change: Bind AddKey and EnrollFirstKey to their key-add challenge and atomically mark it answered alongside the key insertion and audit append.
- Acceptance: Tests reject challenge reuse after successful enrollment, including after revocation before challenge expiry. Failed enrollment leaves the challenge unconsumed and stores no partial changes.

## 6. Provide a store operation that atomically deletes a person's step-up keys and
PR #1319

- Files: internal/stepup/stepup.go:367; internal/stepup/stepup.go:460; internal/stepup/stepup.go:535
- Change: Provide a store operation that atomically deletes a person's step-up keys and active and pending TOTP factors, revokes all their step-up credentials, and appends one reset audit entry.
- Acceptance: Tests verify complete reset with exactly one audit entry. An injected transaction failure leaves factors, credentials, and audit entries unchanged.

## 7. Remove the CSRF-versus-other-site case as evidence of reachable refusal
PR #1360

- Files: internal/errcode/errcode_test.go:39-50
- Change: Remove the CSRF-versus-other-site case as evidence of reachable refusal precedence. Retain the existing checks that each session error takes precedence over identity.ErrDenied.
- Acceptance: TestForbiddenOrder passes with separate CSRF-versus-denied and other-site-versus-denied cases. No case relies on simultaneous CSRF and other-site failures to establish request precedence.

## 8. Propagate unexpected CheckAccess errors without adding identity.ErrDenied
PR #1360

- Files: pkg/gate/service.go:338-347
- Change: Propagate unexpected CheckAccess errors without adding identity.ErrDenied. Preserve denial classification for actual access-limit refusals.
- Acceptance: Add a test where the inner authorizer allows and the access store fails; the returned error must match the store error but not identity.ErrDenied. Verify that an actual limit refusal still matches both ErrLimited and identity.ErrDenied.

## 9. Remove the synthetic CSRF-before-other-site precedence case
PR #1360

- Files: internal/errcode/errcode_test.go:39-50
- Change: Remove the synthetic CSRF-before-other-site precedence case. Preserve coverage of each session error against the lower-priority identity.ErrDenied cause.
- Acceptance: TestForbiddenOrder covers both session errors against identity.ErrDenied and passes. It does not join the two mutually exclusive session errors to claim reachable precedence.

## 10. Preserve CheckAccess infrastructure failures without adding identity.ErrDenied,
PR #1360

- Files: pkg/gate/service.go:338-347; internal/errcode/errcode_test.go:50
- Change: Preserve CheckAccess infrastructure failures without adding identity.ErrDenied, reserving denial wrapping for access refusals. Replace the mutually exclusive session-error precedence case with causes that can coexist.
- Acceptance: Tests inject store and gate.ErrUnavailable failures and verify they retain their original cause without matching identity.ErrDenied or classifying as access.denied. Replacement precedence coverage uses coexisting causes.

## 11. Extend scope-isolation coverage to exhaust one scope's shared failure budget
PR #1395

- Files: internal/authgate/authgate_test.go:460; internal/authgate/authgate.go:769
- Change: Extend scope-isolation coverage to exhaust one scope's shared failure budget while another scope uses the same source and counter. Exercise shared-budget marking, not only separate counter keys.
- Acceptance: The test must reach markShared for the exhausted scope and verify that the other scope remains usable. It must fail if shared-budget state leaks across scopes.

---
_Generated by [Claude Code](https://claude.ai/code)_
