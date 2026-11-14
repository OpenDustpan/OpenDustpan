## 1. Failing tests first

- [ ] 1.1 Add failing unit tests for the compare: changed inputs mark drift with commit and field names. Check: TestCompareMarksDriftWithCommitAndFields fails on main, passes after fix.
- [ ] 1.2 Add failing unit tests for the clear path. Check: TestCompareClearsDriftOnMatch fails on main, passes after fix.
- [ ] 1.3 Add failing hostile tests for webhook signature, path traversal, and malformed document. Check: TestWebhookRejectsBadSignature fails on main, passes after fix.
- [ ] 1.4 Add failing test for 409 request.drifted refusal. Check: TestWriteRefusedOverDrift fails on main, passes after fix.
- [ ] 1.5 Add failing test for repeated delivery id. Check: TestRedeliveryIdempotent fails on main, passes after fix.
- [ ] 1.6 Add failing test for deleted file, ignored non-home ref, and the catch-up read at start. Check: TestRemovedFileMarksFileRemoved and TestStartCatchUpReadsCurrentFile fail on main, pass after fix.

## 2. Build

- [ ] 2.1 Wire the webhook handler to the compare and the drift store. Check: POST a recorded push delivery to the public webhook route and the drift read shows drifted with commit and fields.
- [ ] 2.2 Add the catch-up read at start and on manual redelivery. Check: TestStartCatchUpReadsCurrentFile passes; no timer or poll exists in the code.
- [ ] 2.3 Add e2e steps with a recorded forge: edit, match, malformed file, delete. Check: e2e run shows drifted, in-sync, invalid-file and file-removed through the drift read.
- [ ] 2.4 Add e2e step for the refusal and the exit by matching submit. Check: e2e checks 409 request.drifted with no write, then in-sync after submit.
- [ ] 2.5 Write the docs page for the drift statuses, errors, and the drift read call. Check: make docs-lint passes on the page.
- [ ] 2.6 Emit the mark and clear event and audit entry. Check: Event log and audit log hold one entry for mark and one for clear with commit and delivery ids.

## 3. Definition of done

- [ ] 3.1 Declare the drift read route with self-service:read and the public webhook route. Check: The route policy test passes with the webhook on the explicit public list.
- [ ] 3.2 Run the usability check for the drift path. Check: Developer completes the path from the docs page alone.
- [ ] 3.3 Run full validation. Check: `make check` reports zero findings; merge waits for the accepted short drift-record ADR.
