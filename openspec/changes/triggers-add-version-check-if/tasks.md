## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add store-port conformance cases in `pkg/trigger/triggertest/store.go:562` (func caseLastSaver) for the version compare on replace: stale, concurrent same ETag, replace after delete, and baseline row at version 1. Check: TestTriggerReplaceStaleVersionFails fails on main.
- [ ] 1.2 Add handler tests in the NEW file `internal/api/triggers/serve_test.go` for ETag on the single read, the version member on the list, If-Match on PUT, and 412 for a stale version. Check: TestGetTriggerAnswersETag and TestReplaceTriggerNeedsIfMatch fail on main.

## 2. Fix

- [ ] 2.1 Wire the version through the record, the store and Handler.replaceTrigger in `internal/api/triggers/serve.go:202` (func Handler.replaceTrigger), add the three NEW codes in `internal/errcode/catalog_trigger.go:7`, use WithETag in `internal/api/apidesc/collect.go:120` and WithIfMatch in `internal/api/apidesc/collect.go:105` in the API description, update the header-flag test and the trigger reference page; this route is the operation that unblocks the 428 conformance case missing-required-if-match-gives-428. Check: PUT with the fresh ETag returns 200 with a new ETag; the old ETag returns 412; no If-Match returns 428.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run an e2e step against the real server on PUT /api/dustpan/v1/namespaces/{}/units/{}/triggers/{} with an access token for the read ETag and the stale PUT, Confirmed by: CI (make e2e) Check: E2E replaceTrigger with fresh and stale ETag on the real server.
- [ ] 4.2 Check the docs page and the usability of the path, then run the full checks, A person's step. Check: `make check` with zero findings.
