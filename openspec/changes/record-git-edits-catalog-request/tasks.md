## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing unit tests for the compare in NEW file pkg/forge/gitdrift_test.go: changed inputs mark drift with commit and field names. Check: TestCompareMarksDriftWithCommitAndFields fails on main, passes after fix.
- [ ] 1.2 Add failing unit tests for the clear path in NEW file pkg/forge/gitdrift_test.go. Check: TestCompareClearsDriftOnMatch fails on main, passes after fix.
- [ ] 1.3 Add failing hostile tests for webhook signature, path traversal, and malformed document in NEW file pkg/forge/gitdrift_webhook_test.go. Check: TestWebhookRejectsBadSignature fails on main, passes after fix.
- [ ] 1.4 Add failing test for 409 request.drifted refusal in NEW file pkg/forge/gitdrift_writeback_test.go. Check: TestWriteRefusedOverDrift fails on main, passes after fix.
- [ ] 1.5 Add failing test for repeated delivery id in NEW file pkg/forge/gitdrift_webhook_test.go. Check: TestRedeliveryIdempotent fails on main, passes after fix.
- [ ] 1.6 Add failing tests for deleted file and the catch-up read at start in NEW file pkg/forge/gitdrift_catchup_test.go. Check: TestRemovedFileMarksFileRemoved and TestStartCatchUpReadsCurrentFile fail on main, pass after fix.

## 2. Build

- [ ] 2.1 Write the short ADR for the drift record, its statuses and its read path in NEW file docs/adr/drift-record.md. Check: make adr-index and make docs-lint pass with the new ADR. Confirmed by: the driver's gates.
- [ ] 2.2 Wire the webhook handler to the compare and the drift store in NEW file pkg/forge/gitdrift.go. Check: A recorded push delivery to the public webhook route makes the drift read show drifted with commit and fields.
- [ ] 2.3 Add the catch-up read at start in NEW file pkg/forge/gitdrift_catchup.go. Check: TestStartCatchUpReadsCurrentFile passes; no timer or poll exists in the code.
- [ ] 2.4 Add e2e steps in NEW file e2e/gitdrift_test.go with a recorded forge: edit, match, malformed file, delete. BLOCKED by the self-service request routes of #886 and the blueprints change: the drift read route and the public webhook route are new in this change, so the steps name them once they exist. Credential kinds: webhook signature for the push, personal token for the read. Check: e2e sends a signed push delivery to the public webhook route, then calls GET /api/dustpan/v1/namespaces/{namespace}/self-service/requests/{request}/drift with a personal token and sees drifted, in-sync, invalid-file and file-removed.
- [ ] 2.5 Add e2e step for the refusal and the exit by matching push in NEW file e2e/gitdrift_test.go. BLOCKED by the write-back route of the blueprints change; the step uses a personal token for the write-back call and a signed delivery for the exit. Check: Write-back answers 409 request.drifted, then succeeds after a matching signed push.
- [ ] 2.6 Write the docs page for the drift statuses, errors, and the drift read call in NEW file docs/git-edits-drift.md. Check: make docs-lint passes on the page.
- [ ] 2.7 Emit the mark and clear event and audit entry in NEW file pkg/forge/gitdrift.go. Check: Audit log holds one entry for mark and one for clear with commit id.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Declare the drift read route with self-service:read and the public webhook route in NEW file internal/api/apidesc/gitdrift.go (declaration) and NEW file pkg/dustpan/gitdrift_routes.go (adapter). Check: The route policy test passes with the webhook on the explicit public list.
- [ ] 4.2 Agent pre-run (human run-through in person-steps (record-git-edits-catalog-request 4.2)): Run the usability check for the drift path. Check: Developer completes the path from the docs page alone. Record the transcript in the pull request body.
- [ ] 4.3 Run full validation. Check: `make check` reports zero findings.
