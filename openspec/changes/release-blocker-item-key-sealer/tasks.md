## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add a failing start-up test for an item key with an unknown wrapper ID in NEW pkg/secret/itemkey_startcheck_test.go Check: NEW TestCheckWrapperRefusesUnknownItemKeyWrapperID fails on main
- [ ] 1.2 Add a failing rewrap test for live item key blobs in NEW pkg/secret/itemkey_rewrap_test.go Check: NEW TestRewrapItemKeysRewritesLiveBlobs fails on main
- [ ] 1.3 Add a failing blob naming test for the v0 token in NEW pkg/secret/itemkey_blobkey_test.go Check: NEW TestItemKeyBlobKeyUsesV0 fails on main

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 2.2 Check the callers that type-assert on `Wrapper`: `pkg/store/storetest/delete_audit.go`. Check: the named files change in the same pull request
- [ ] 2.3 Regenerate `internal/db` (`make generate`, sqlc); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Implement the fail-closed CheckWrapper, the repeatable RewrapItemKeys and ItemKeyBlobKey with v0 Check: `go test -race ./pkg/secret/...` passes
- [ ] 3.2 Run the full check gate for the change. Confirmed by: CI (make check) Check: `make check` reports zero findings
