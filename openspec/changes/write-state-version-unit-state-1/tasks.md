## 1. Failing tests first

- [ ] 1.1 Add failing store test: Pulumi checkpoint write sets current version keyed by stable unit ID. Check: go test ./pkg/stack/... -run TestCurrentVersionOnCheckpoint -count=1 fails on main
- [ ] 1.2 Add failing store test: OpenTofu finalize sets current version and calls observe on final writes only. Check: go test ./pkg/workspace/... -run TestCurrentVersionOnFinalize -count=1 fails on main
- [ ] 1.3 Add failing store tests: observe failure rolls back, duplicate final is idempotent, missing observer refuses final write. Check: go test ./pkg/stack/... -run TestFinalObserveRollback|TestDuplicateFinalIdempotent|TestNoObserverRefusesFinal -count=1 fails on main

## 2. Implementation

- [ ] 2.1 Implement current-version writes in both tool transactions with injected observer port keyed by stable unit ID. Check: go test ./pkg/stack/... ./pkg/workspace/... -count=1 passes
- [ ] 2.2 Apply the final-version observe rule in the same fenced transaction of each tool store. Check: go test ./pkg/stack/... ./pkg/workspace/... -run 'Final|Observer' -count=1 passes

## 3. Definition of done

- [ ] 3.1 Add e2e step with real Pulumi CLI and OpenTofu binaries showing current and final versions. Check: make e2e E2E_FOCUS=state-versions against the real CLI binaries over TLS on SQLite and PostgreSQL
- [ ] 3.2 Update user docs for current and final state versions. Check: docs page for state versions updated in the same pull request
- [ ] 3.3 Run full check with zero findings. Check: make check with zero findings
