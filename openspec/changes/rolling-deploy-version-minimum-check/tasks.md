## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing server test for old write refused and read allowed through existing write route and gate. The test is in the NEW file internal/servertest/version_gate_test.go. Check: TestVersionGateRefusesOldWrite fails on main, passes after fix; calls ANY /api/v2/state-versions, passes namespace write grant gate, uses access token
- [ ] 1.2 Write failing store test for atomic concurrent raise keeping the greatest version. The test is in the NEW file internal/instance/version_test.go. Check: TestRaiseMinimumKeepsGreatest fails on main, passes after fix; calls ANY /api/server/instances, passes installation read grant gate, uses access token

## 2. Build

- [ ] 2.1 Add the NEW type VersionGate, the NEW port VersionStore with the NEW methods RecordInstance, RaiseMinimum and CheckWriteAllowed, and the version rows with atomic raise and installation scope check via InstallationScope, in the NEW file internal/instance/version.go (next to the existing internal/instance/instance.go). The minimum check at Open stays in pkg/store/schema (schema.go: MinimumError, ErrBinaryBelowMinimum); the new gate reuses its comparison. Check: `go test -race ./pkg/store/schema/... ./internal/instance/...` passes
- [ ] 2.2 Wire CheckWriteAllowed into write paths and map refusal to 503 with Retry-After through belowMinimum (internal/api/problem/problem.go:501) and belowMinimumText (internal/api/pulumi/pulumi.go:1130). The write paths are in the existing file internal/api/problem/problem.go. Check: `go test -race ./internal/api/...` passes with Pulumi and TFE shapes checked

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run e2e step against real CLI binaries on existing TFE state-version and Pulumi checkpoint routes with mixed binary pool. Check: Real `tofu apply` and `pulumi up` via ANY /api/v2/state-versions with namespace write grant gate and access token show old write 503 and reads 200. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Docs page for minimum version behavior, route authorization actions listed, usability check from docs page alone. Check: `make check` passes with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
