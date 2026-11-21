## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing tests for each spec scenario in `pkg/job/uploaded_plan_test.go` (NEW, package job). Check: `go test ./pkg/job/ -run TestUploadedPlan -count=1` fails on main.

- [ ] 1.2 SECURITY. Build the SHA-256 check of each plan part in NEW file `pkg/job/uploaded_plan.go` (package job), in the constructor NEW `checkUploadedPlan`. It refuses a part whose bytes do not match the claimed hash. Check: `go build ./pkg/job/` passes and the constructor refuses a wrong hash.
- [ ] 1.3 SECURITY. Write the hostile test for the part hash check in `pkg/job/uploaded_plan_test.go` (NEW, package job), test name `TestUploadedPlanPartHashMismatch`. It sends a wrong claimed hash straight to `checkUploadedPlan`. Check: `go test ./pkg/job/ -run TestUploadedPlanPartHashMismatch -count=1` passes and the call is refused.
- [ ] 1.4 SECURITY. Build the hash comparison at apply claim open in NEW file `pkg/job/uploaded_plan_claim.go` (package job), in the function NEW `checkApplyClaimHash`. It refuses to open the claim when the presented hash is missing or is not the approved plan hash. Check: `go build ./pkg/job/` passes and the function refuses an old hash and an empty hash.
- [ ] 1.5 SECURITY. Write the hostile test for the apply claim hash comparison in `pkg/job/uploaded_plan_test.go` (NEW, package job), test name `TestUploadedPlanClaimHashMismatch`. It sends an old hash and no hash straight to `checkApplyClaimHash`. Check: `go test ./pkg/job/ -run TestUploadedPlanClaimHashMismatch -count=1` passes and both calls are refused.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done
