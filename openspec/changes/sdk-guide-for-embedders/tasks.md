## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing test for the NEW minimal embed program Check: `go test ./pkg/dustpan -run TestGuideExampleBuilds -count=1` for the NEW test `TestGuideExampleBuilds` fails on main
- [ ] 1.2 Add failing test that the example serves GET /api/dustpan/v1/capabilities with an access token Check: `go test ./pkg/dustpan -run TestGuideProbes -count=1` for the NEW test `TestGuideProbes` fails on main
- [ ] 1.3 Add failing import check that the example imports only pkg packages Check: `go test ./pkg/dustpan -run TestGuideImportClosure -count=1` for the NEW test `TestGuideImportClosure` fails on main
- [ ] 1.4 Add failing tests for the storetest, blobtest and secrettest commands, including the failure text that names the method Check: `go test ./pkg/dustpan -run TestGuideFailureNamesMethod -count=1` for the NEW test `TestGuideFailureNamesMethod` fails on main

## 2. Guide work

- [ ] 2.1 Add the NEW constructors under pkg/dustpan and the NEW example with the guide page Check: `go test ./pkg/dustpan -run TestGuideExampleBuilds -count=1` for the NEW test `TestGuideExampleBuilds` passes
- [ ] 2.2 Document conformance runs for own store, blob and secret adapters Check: `go test ./pkg/dustpan -run TestGuideStoreDoc -count=1` for the NEW test `TestGuideStoreDoc` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Refresh the SDK snapshot Check: `make sdk-surface` passes and adds the new file `internal/tools/sdksurface/api.txt` for the NEW constructors
- [ ] 4.2 Run e2e smoke of the example server Check: GET /api/dustpan/v1/capabilities with an access token returns 200 against the NEW example server
- [ ] 4.3 Run usability check of the guide Check: Embedder completes the path from the docs page alone
- [ ] 4.4 Run full checks Check: `make check` with zero findings
