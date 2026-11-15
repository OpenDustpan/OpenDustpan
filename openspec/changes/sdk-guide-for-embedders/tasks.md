## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing test for the NEW minimal embed program in the NEW file pkg/dustpan/guide_example_test.go Check: `go test ./pkg/dustpan -run TestGuideExampleBuilds -count=1` for the NEW test `TestGuideExampleBuilds` fails on main
- [ ] 1.2 Add failing test that the example serves GET /api/dustpan/v1/capabilities with an access token in the NEW file pkg/dustpan/guide_probe_test.go Check: `go test ./pkg/dustpan -run TestGuideProbes -count=1` for the NEW test `TestGuideProbes` fails on main
- [ ] 1.3 Add failing import check that the example imports only pkg packages in the NEW file pkg/dustpan/guide_imports_test.go Check: `go test ./pkg/dustpan -run TestGuideImportClosure -count=1` for the NEW test `TestGuideImportClosure` fails on main
- [ ] 1.4 Add failing tests for the storetest, blobtest and secrettest commands, including the failure text that names the method, in the NEW file pkg/dustpan/guide_conformance_test.go Check: `go test ./pkg/dustpan -run TestGuideFailureNamesMethod -count=1` for the NEW test `TestGuideFailureNamesMethod` fails on main

## 2. Guide work

- [ ] 2.1 Add the NEW constructors under pkg/dustpan and the NEW example with the guide page. The task adds the NEW top-level test `TestGuideExampleBuilds` in a `_test.go` file of `pkg/dustpan`, so that the Check runs it. Check: `go test ./pkg/dustpan -run TestGuideExampleBuilds -count=1` for the NEW test `TestGuideExampleBuilds` passes
- [ ] 2.2 Document conformance runs for own store, blob and secret adapters in the NEW file docs/guides/sdk-embedders.md. The task adds the NEW top-level test `TestGuideStoreDoc` in a `_test.go` file of `pkg/dustpan`, so that the Check runs it. Check: `go test ./pkg/dustpan -run TestGuideStoreDoc -count=1` for the NEW test `TestGuideStoreDoc` passes. `make docs-lint` passes on each edited guide.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Refresh the SDK snapshot Check: `make sdk-surface` passes and adds the new file `internal/tools/sdksurface/api.txt` for the NEW constructors
- [ ] 4.2 Run e2e smoke of the example server Check: GET /api/dustpan/v1/capabilities with an access token returns 200 against the NEW example server
- [ ] 4.3 Run usability check of the guide Check: Embedder completes the path from the docs page alone. A person's step.
- [ ] 4.4 Run full checks Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
