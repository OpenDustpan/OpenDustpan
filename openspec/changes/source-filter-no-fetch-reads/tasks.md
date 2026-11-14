## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add a service test that a filter write keeps fence use and one audit record Check: NEW `TestSetKeepsFenceAndAudit` in NEW internal/sourcefiltersvc/service_test.go fails on main
- [ ] 1.2 Add a handler test that the PUT route calls the service with an access token under its existing gate, written in NEW internal/api/sourcefilter/service_call_test.go Check: NEW `TestPutCallsTheService` in NEW internal/api/sourcefilter/service_call_test.go on PUT /api/dustpan/v1/namespaces/{namespace}/source-filter fails on main before the move

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 2.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 State on docs/reference/source-filter-api.md that the server stores the filter and no fetch reads it yet Check: Read docs/reference/source-filter-api.md and see the stored-only sentence
- [ ] 3.2 Move encode, audit and fence steps from the handler into NEW internal/sourcefiltersvc and correct the wrong service-and-store comment Check: rg shows no direct store-port write from internal/api/sourcefilter/handlers.go
- [ ] 3.3 File the follow-up issue that removes the stored-only note when a fetch path first reads the filter Check: Follow-up issue for fetch enforcement and note removal exists with its number recorded in tasks.md
- [ ] 3.4 Verify wiring on PUT /api/dustpan/v1/namespaces/{namespace}/source-filter with an access token under the existing gate, then docs page and usability check. Confirmed by: CI (`make check` passes with zero findings). Check: `make check` passes with zero findings
