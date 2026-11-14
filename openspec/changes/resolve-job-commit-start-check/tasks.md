## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 BLOCKED by the missing documented jobs start route in the pack: write the failing test of head resolution on the existing start path in internal/api/jobs/start.go with unit job-start authorization and an access-token credential. Confirmed by: the driver's gates. Check: NEW TestStartResolvesHeadCommit in internal/api/jobs fails on main.
- [ ] 1.2 BLOCKED by the missing documented jobs start route in the pack: write the failing hostile tests of the verdict (force push, merge around gate, missing rules, timeout, rate limit) on the same start path with an access-token credential. Confirmed by: the driver's gates. Check: NEW TestStartTrustedVerdict in internal/api/jobs fails on main.

## 2. Definition of done

- [ ] 2.1 BLOCKED by the missing documented jobs start route in the pack: wire the trusted-source-check setting, ResolveCommitAtStart, and the audit record into the existing start path in internal/api/jobs/start.go with unit job-start authorization and an access-token credential. Confirmed by: the driver's gates. Check: Existing start path in internal/api/jobs/start.go wired with unit job-start authorization and access-token credential.
- [ ] 2.2 BLOCKED by the missing documented jobs start route in the pack: add the docs page for the setting and an e2e step that starts a job with an access-token credential and reads the resolved commit and verdict. Confirmed by: the driver's gates. Check: Docs page renders for the trusted-source-check setting.
- [ ] 2.3 BLOCKED by the missing documented jobs start route in the pack: run the wiring, the audit record, the e2e step, the docs page, and a usability check of the start output with an access-token credential. Confirmed by: the driver's gates. Check: `make check` with zero findings.
