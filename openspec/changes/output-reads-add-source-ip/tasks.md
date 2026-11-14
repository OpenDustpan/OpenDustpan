## 1. Failing tests

- [ ] 1.1 Add failing test that a denied output read writes SourceIP, UserAgent and RequestID. Check: TestDenialEntryHasRequestFields fails on main, passes after the fix, and covers the 403 no-rule case, the 409 client-encrypted case and the empty-fields case.
- [ ] 1.2 Add failing test that a flood of refused output reads stops after the recorder budget while reads stay refused. Check: TestDenialFloodIsLimited fails on main, passes after the fix, and covers the flood-stop scenario with reads still refused.
- [ ] 1.3 Add failing tests that a limiter drop and any other audit-append failure still refuse the read. Check: TestDroppedDenialStaysRefused and TestDenialAppendFailureStaysRefused fail on main and pass after the fix.
- [ ] 1.4 Add failing test that an allowed read after a sharing rule returns outputs with no denial entry. Check: TestAllowedReadWritesNoDenial fails on main and passes after the fix.

## 2. Fix

- [ ] 2.1 Pass request fields from the handler to the checker and write denials through refusal.Recorder. Check: rg for the checker audit append shows only the recorder path.
- [ ] 2.2 Run the affected unit and conformance suites. Check: `go test -race ./pkg/outputshare/... ./internal/outputsharesvc/...` passes.

## 3. Definition of done

- [ ] 3.1 Check wiring, route and authorization actions. Check: Route test lists the outputs read route with its authorization actions.
- [ ] 3.2 Run the e2e output-sharing step. Check: e2e output-sharing read with and without a rule passes against the real CLI.
- [ ] 3.3 Update the user docs in the same pull request. Check: Docs page for output sharing names the denial fields and the limit.
- [ ] 3.4 Check the console audit view shows the new denial fields. Check: Namespace administrator reads a denial in the console audit view and sees the client address, user agent and request ID.
- [ ] 3.5 Run make check with zero findings. Check: `make check` reports zero findings.
