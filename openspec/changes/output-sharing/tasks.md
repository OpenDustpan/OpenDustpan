## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [x] 1.1 Cross-tool read tests with a naming rule exist in internal/outputsharesvc/checker_test.go:305 (TestCheckerDecisions). Check: the test passes on main.
- [x] 1.2 Refusal tests exist in internal/outputsharesvc/checker_test.go:445 (TestCheckerAuditsADenialInTheProducerNamespace) and internal/outputsharesvc/reader_test.go:137 (TestReaderRefusals). Check: the tests pass on main.
- [ ] 1.3 Add a failing test in internal/outputsharesvc/checker_test.go that reads a rule from the stored rules. Name it TestCheckerReadsStoredRules (NEW). Check: TestCheckerReadsStoredRules fails on main, passes after the fix.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Build and prove

- [ ] 3.1 Change Checker in internal/outputsharesvc/checker.go:52 (type Checker) to read the rules stored by the depended change. Both read checks already exist on main. Check: `go test -race ./pkg/outputshare/... ./internal/outputsharesvc/...` passes.
- [x] 3.2 The Checker and the Reader are wired in pkg/dustpan/outputsharing.go:59 and :82. Check: TestOutputReadPaths in e2e/output_sharing_test.go:65 passes on main.
- [ ] 3.3 Add a mutation test for the nil-port constructor guard in NEW file pkg/outputshare/checker_test.go. Check: `go test -run TestNewChecker_NilPort ./pkg/outputshare/...` fails when the nil guard is removed.
- [ ] 3.4 Extend TestOutputSharingRules in e2e/output_sharing_test.go:549 to use a stored sharing rule, with the 403 path for a missing rule. Check: e2e/output_sharing_test.go with `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/outputs` and a job token passes against SQLite and PostgreSQL through the real binary over TLS. Confirmed by: CI (the agent writes the e2e test).
- [ ] 3.5 Update docs/guides/output-sharing.md in the same pull request. Remove the line 'The server does not keep sharing rules yet.' Check: docs/guides/output-sharing.md shows both tool reads and the 403 error shape with its one commands. `make docs-lint` passes on each edited guide.
- [ ] 3.7 SECURITY. Build the per-job-token denial rate limit in internal/outputsharesvc/checker.go (type Checker). After the limit, a denied outputs read returns 429 and writes no more audit entries until the window ends. If the audit store fails, the read fails closed with 500 and no outputs. Check: `go test -race ./internal/outputsharesvc/...` passes and a denied read past the limit returns 429.
- [ ] 3.8 SECURITY. Add a hostile test TestCheckerDeniedReadFlood in NEW file internal/outputsharesvc/ratelimit_test.go. It sends a flood of denied reads for one job token straight to the Checker. Check: `go test -run TestCheckerDeniedReadFlood ./internal/outputsharesvc/...` fails when the rate limit is removed, and the test sees 429 after the limit and no extra audit writes.
- [ ] 3.9 SECURITY. Add a hostile test TestCheckerAuditStoreOutage in NEW file internal/outputsharesvc/auditoutage_test.go. It makes the audit store fail on a read and on a denial, and sends both straight to the Checker. Check: `go test -run TestCheckerAuditStoreOutage ./internal/outputsharesvc/...` fails when the fail-closed guard is removed, and the test sees 500 with no outputs.
- [ ] 3.6 Run usability check and full check. Check: Developer follows docs/guides/output-sharing.md alone and each error names its one command; `make check` passes with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
