## 1. Failing tests first

- [ ] 1.1 Add failing API test for reading an own-namespace record by gdr- ID. Check: TestDecisionRecordReadReturnsStoredRecord fails on main with 404.
- [ ] 1.2 Add failing test for in-scope read without the read action. Check: TestDecisionRecordReadDeniedGives403 fails on main with wrong status or content leak.
- [ ] 1.3 Add failing hostile test for cross-namespace, unauthenticated, malformed, and expired IDs. Check: TestDecisionRecordReadForeignGives404 fails on main with record leak or 500.
- [ ] 1.4 Add failing test that denials, waits, and warnings at each decision point carry the record ID and link. Check: TestStateWriteRefusalNamesRecordId fails on main with missing record ID.

## 2. Definition of done

- [ ] 2.1 Wire the read route with its gate read actions and error codes. Check: New route GET /api/dustpan/v1/namespaces/{namespace}/decision-records/{id} returns 200 with the record for an allowed caller, 403 in scope without the action, and 404 for missing or foreign IDs.
- [ ] 2.2 Update the API description for the read route. Check: `make generate` leaves no diff and the new route and its actions show in the API description.
- [ ] 2.3 Update the user docs page for the read route and verify the command runs as written. Check: `make docs-lint` passes and `curl /api/dustpan/v1/namespaces/{namespace}/decision-records/{id}` runs as written and opens the named record.
- [ ] 2.4 Run usability check, e2e step against the real CLI, and `make check` with zero findings. Check: Developer opens the record from the docs page alone; each error names the one command that helps.
