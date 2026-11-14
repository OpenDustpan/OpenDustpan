## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Create NEW pkg/dustpan/authorizer_subset_test.go with NEW helper newEveryModuleServer that mounts all modules and records authorizer action and kind, sends one request per described operation with namespace acme through the existing route table with an access token, asserts non-empty records except the exempt allowlist, and asserts subset of op ActionObjects. Check: go test ./pkg/dustpan -run TestAuthorizerCallsAreSubsetOfActionObjects -count=1 fails on main then passes after fix
- [ ] 1.2 Write NEW test for the dropped required tag on an embedded-pointer promotion in NEW pkg/dustpan/embedded_required_test.go. Check: go test ./pkg/dustpan -run TestEmbeddedRequiredTag -count=1 fails on main

## 2. Fix nits

- [ ] 2.1 Keep IdempotencyKey in the chat Sender interface in pkg/notify/chat and pass it as the retry deduplication key; a repeated key returns the first result. Check: go test ./pkg/notify/chat/... -count=1 passes
- [ ] 2.2 Keep NewLoopback exported in pkg/notify/chat/loopback_e2e.go:27 with test-only docs and rename the gov-slack host test in the pkg/notify/chat tests. Check: go test ./pkg/notify/chat/... -count=1 passes
- [ ] 2.3 Fix the required tag on an embedded-pointer promotion so it raises a field error in internal/api/apidesc/read.go:209. Check: go test ./pkg/dustpan -run TestEmbeddedRequiredTag -count=1 passes
- [ ] 2.4 Deduplicate kindsOf from pkg/store/storetest/inventory_changes.go:42 and its second test copy into one shared test helper keyed by action. Check: rg -n 'func kindsOf' pkg internal shows one definition
- [ ] 2.5 Apply typed-schema nits N1 to N5 as written in the review comment of the typed schemas change in docs/rfc/0102-cli-step-up-for-writes-that-need-a-fresh-authentication.md and internal/api/apidesc/apidesc.go:54. Check: Each nit ticked in issue 1343
- [ ] 2.6 Apply the 8 RFC 102 nits of the round 2 review in docs/rfc/0102-cli-step-up-for-writes-that-need-a-fresh-authentication.md. Check: Each nit ticked in issue 1328 and make docs-lint passes

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Confirm the wiring and the existing gate TestEveryDescribedAdapterReadsThroughApidesc in pkg/dustpan/apidesc_gate_test.go:96 still pass. Check: go test ./pkg/dustpan -count=1
- [ ] 4.2 Run make check with zero findings. Confirmed by: the driver's gates. Check: make check
