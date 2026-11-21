## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing test in NEW file internal/outputsharesvc/denial_fields_test.go that a denied output read writes SourceIP, UserAgent and RequestID. Check: TestDenialEntryHasRequestFields fails on main, passes after the fix, and covers the 403 no-rule case, the 409 client-encrypted case and the empty-fields case.
- [ ] 1.2 Add failing test in NEW file internal/outputsharesvc/denial_flood_test.go that a flood of refused output reads stops after the recorder budget while reads stay refused. Check: TestDenialFloodIsLimited fails on main, passes after the fix, and covers the flood-stop scenario with reads still refused.
- [ ] 1.3 Add failing tests in NEW file internal/outputsharesvc/denial_failclosed_test.go that a limiter drop and any other audit-append failure still refuse the read. Check: TestDroppedDenialStaysRefused and TestDenialAppendFailureStaysRefused fail on main and pass after the fix.
- [ ] 1.4 Add failing test in NEW file internal/outputsharesvc/denial_allowed_test.go that an allowed read after a sharing rule returns outputs with no denial entry. Check: TestAllowedReadWritesNoDenial fails on main and passes after the fix.

## 2. Fix

- [ ] 2.1 Pass request fields from the handler to the checker and write denials through refusal.Recorder (`internal/api/refusal/refusal.go:84`) in NEW file internal/outputsharesvc/denial.go. Check: rg for the checker audit append shows only the recorder path.
- [ ] 2.2 Run the affected unit and conformance suites in the packages internal/outputsharesvc and pkg/outputshare. Check: `go test -race ./pkg/outputshare/... ./internal/outputsharesvc/...` passes.

## 3. Definition of done

- [ ] 3.1 Check wiring, route and authorization actions. Check: Route test lists the outputs read route with its authorization actions.
- [ ] 3.2 Run the e2e output-sharing step on the existing outputs read route with an access token. Check: e2e output-sharing step for `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit}/outputs` with an access token passes against the real CLI.
- [ ] 3.3 Update the user docs in the same pull request in the existing guide docs/guides/output-sharing.md, extended with a new section. The page names the denial fields and the limit. Check: Docs page docs/guides/output-sharing.md for output sharing names the denial fields and the limit. `make docs-lint` passes on each edited guide.
- [ ] 3.4 Check the console audit view shows the new denial fields. Check: Namespace administrator reads a denial in the console audit view and sees the client address, user agent and request ID.
- [ ] 3.5 Run make check with zero findings. Check: `make check` reports zero findings.
