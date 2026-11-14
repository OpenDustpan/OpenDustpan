## 1. Failing tests first

- [ ] 1.1 Add failing store and service tests for upload, sealing, hash bind, and mismatch refusal. Check: `go test ./pkg/job/ -run TestPutUploadedPlan -count=1` fails on main.
- [ ] 1.2 Add failing API tests for the upload route, auth actions, and error bodies. Check: `go test ./internal/api/... -run TestUploadPlanRoute -count=1` fails on main.

## 2. Build

- [ ] 2.1 Build upload of both parts with item key, stored plan, and approval binding. Check: `go test ./pkg/job/... -count=1` passes with the upload path and both-hash bind.
- [ ] 2.2 Enforce both-hash check in the state write and update start transactions. Check: `go test ./... -run TestUploadedHashMismatch -count=1` passes for OpenTofu state write and Pulumi update start.
- [ ] 2.3 Add uploaded fields to the change-order read route of mirror-approvals-between-review-request and show the weaker mark in the review view. Check: `curl -H 'Authorization: Bearer $T' $S/api/dustpan/v1/namespaces/$NS/change-orders/$CO` shows uploaded=true, the mark, and both hashes, and denies a caller without change order read.

## 3. Definition of done

- [ ] 3.1 Run the e2e step against the real server and the real OpenTofu CLI. Check: `make e2e RUN=uploaded-plan` runs a CI-uploaded OpenTofu plan, approves it, writes state with the same hashes, then retries with a changed hash and reads the refusal naming the change order.
- [ ] 3.2 Write the user docs page for uploaded plans. Check: Docs page renders and `make docs-lint` passes.
- [ ] 3.3 Run the definition of done: wiring, route with actions, e2e, docs, usability check from the docs page alone, and make check. Check: `make check` reports zero findings.
