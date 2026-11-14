## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. failing tests first

- [ ] 1.1 Add a failing output-state test for the ABA sequence in pkg/stack/output_states_recreate_test.go. Check: NEW TestOutputStatesRefusesAbaRecreateDuringRead fails on main and passes after the fix.
- [ ] 1.2 Add storetest conformance for the NEW method ReadCheckpointByStableID in pkg/store/storetest/updates.go. Check: The NEW storetest cases fail on the adapters of main and pass after the fix.

## 2. implementation

- [ ] 2.1 Implement the keyed checkpoint read in pkg/stack with the owner stable ID column and in the sqlite and postgres adapters. Check: `go test ./pkg/stack ./pkg/store/...` passes.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. definition of done

- [ ] 4.1 Wire the keyed read through the existing route GET /api/dustpan/v1/namespaces/{}/units/{}/outputs with an access token. Check: A server test calls GET /api/dustpan/v1/namespaces/{}/units/{}/outputs with an access token and covers the 403 and 404 paths.
- [ ] 4.2 Run the full checks and read the refusal message once for usability. A person's step. Check: `make check` reports zero findings.
