## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing hostile test for `checkCommit` direct in `pkg/forge/github/commit.go:301` Check: `go test ./pkg/forge/github -run TestCheckCommitHostile`
- [ ] 1.2 Add failing hostile test for `checkChanges` direct in `pkg/forge/github/commit.go:364` Check: `go test ./pkg/forge/github -run TestCheckChangesHostile`
- [ ] 1.3 Add failing hostile tests for `validTrailer`, `hasTrailer`, and `isTrailerBlock` direct in `pkg/forge/github/commit.go:502` Check: `go test ./pkg/forge/github -run TestTrailerBlockHostile`
- [ ] 1.4 Add conformance cases for missing repository on Commit and FindCommit plus ErrRefused without write permission in `pkg/forge/forgetest/commit_cases.go` Check: `go test ./pkg/forge/... -run TestForgetestCommitMissingRepo`

## 2. Fix

- [ ] 2.1 Fix `hasTrailer` in `pkg/forge/github/commit.go:1066` to call `isTrailerBlock` with prose-plus-trailer case Check: `rg -n isTrailerBlock pkg/forge/github/commit.go` shows `hasTrailer` calls `isTrailerBlock`
- [ ] 2.2 Bound moved-head retry at 3 and cache base blob IDs in `pkg/forge/github/commit.go:195` Check: `rg -n requireAncestor pkg/forge/github/commit.go` shows bound of 3 and cached base blobs with no file patches
- [ ] 2.3 Update adapter doc in `pkg/forge/github/commit.go:51` and port doc in `pkg/forge/forge.go:251` for author, signature, head-follow, limit, and clock Check: Doc comment in `pkg/forge/github/commit.go:51` names app author, Co-authored-by trailer, signature wait, head-follow bound, 500-commit limit, and since clock with 5-minute skew margin

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run forge unit and conformance suites in `pkg/forge/github` and `pkg/forge/forgetest` Check: `go test -race ./pkg/forge/...` passes
- [ ] 4.2 Run existing roundtrip suites in `internal/servertest/roundtrips_test.go`, `internal/servertest/roundtrips_conflict_test.go`, and `internal/servertest/roundtrips_model_test.go` via GET /api/dustpan/v1/namespaces/{namespace}/settings passing the namespace settings read gate with an access token, with no new behavior Check: `go test ./internal/servertest -run TestRoundTrips` in `internal/servertest/roundtrips_test.go` plus `internal/servertest/roundtrips_conflict_test.go` and `internal/servertest/roundtrips_model_test.go`
- [ ] 4.3 BLOCKED by no user-visible route for the Forge Commit adapter; exercises write-back through the existing fake GitHub harness with a GitHub App credential kind and no new route or gate. Confirmed by: CI (make check and e2e-forge) Check: `make check` with zero findings
