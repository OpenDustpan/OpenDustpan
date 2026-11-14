## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. E2e scenarios

- [ ] 1.1 In e2e/output_sharing_test.go extend reduced-deployment checks to assert export envelope version 3, features preserved when present, clean handling when absent, and the sole root resource custom field false. Reads use GET /api/dustpan/v1/namespaces/{}/units/{}/outputs with an access token. Check: NEW tests ReducedWithFeatures and ReducedWithoutFeatures fail on main for missing assertions and pass after. Confirmed by: CI (the agent writes the e2e test).
- [ ] 1.2 In e2e/stack_paging_test.go add one scenario for issue items 2 and 3: pulumi stack ls without --all from a project directory, 5 current-project stacks plus 2 other-project stacks, existing harness paging config. The real CLI uses an access token. Check: NEW test ProjectFilterPaging passes with each of 5 expected stacks once, no other-project stack, more than one page fetched. Confirmed by: CI (the agent writes the e2e test).
- [ ] 1.3 In e2e/output_sharing_test.go obtain job tokens through the existing runner-harness claim path with a runner credential after runner-protocol-renew-fence-namespace lands. Give the e2e runner a stored grant for ActionRunnerClaim on the pool. Read via GET /api/dustpan/v1/namespaces/{}/units/{}/outputs with an access token. Enable the skipped job scenarios for every harness-listed Pulumi and OpenTofu version with allow, deny, and operator positive control. Check: ReducedDocumentAndAllowDenyMatrix and OperatorPositiveControl pass without skips on SQLite and on Linux with PostgreSQL. Confirmed by: CI (the agent writes the e2e test).
- [ ] 1.4 In e2e/output_sharing_test.go inject a fake token issuer that returns success with an empty or missing secret. Assert outputReader and checkOperatorSavesNoCrossRule fail before use, as namespaceViewer does. Check: EmptyAndMissingSecretGuard and AdminCallsUnchanged fail on main and pass after. Confirmed by: CI (the agent writes the e2e test).

## 2. Definition of done

- [ ] 2.1 Wire the scenarios into the e2e suite with the real CLI binaries and runner-harness claims. Reads use GET /api/dustpan/v1/namespaces/{}/units/{}/outputs with an access token on SQLite and on Linux with PostgreSQL. Confirmed by: CI e2e matrix on both stores. Check: `make e2e TEST=TestOutputSharingRules`
- [ ] 2.2 Run the e2e step against the real CLI with the access token on GET /api/dustpan/v1/namespaces/{}/units/{}/outputs, and run `make check`. Confirmed by: the driver's gates. Check: `make check` with zero findings
