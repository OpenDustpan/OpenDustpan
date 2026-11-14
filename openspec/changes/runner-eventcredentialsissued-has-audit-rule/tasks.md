## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Fix

- [ ] 1.1 Add failing producer and hostile issuance audit tests in pkg/runner. Check: TestEveryAuditEventHasAProducer fails on main and passes after the fix.
- [ ] 1.2 Write the entry in the NEW IssueCredentials path and remove EventCredentialsIssued from the unproduced set. Check: `rg EventCredentialsIssued pkg/runner/handoff_test.go` shows no unproduced entry and `go test ./pkg/runner/ -count=1` passes.

## 2. Definition of done

- [ ] 2.1 Wire the writer into the served issuance path and verify the audit entry. Check: Server test calls ANY /runner/v1 issuance with a runner credential and finds the audit entry.
- [ ] 2.2 Run the full checks and usability review of the audit entry. Confirmed by: CI (make check). Check: `make check` passes with zero findings.
