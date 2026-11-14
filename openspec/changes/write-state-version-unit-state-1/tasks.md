## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing store test: Pulumi checkpoint write sets current version keyed by stable unit ID. Check: go test ./pkg/stack/... -run TestCurrentVersionOnCheckpoint -count=1 fails on main
- [ ] 1.2 Add failing store test: OpenTofu finalize sets current version and calls observe on final writes only. Check: go test ./pkg/workspace/... -run TestCurrentVersionOnFinalize -count=1 fails on main
- [ ] 1.3 Add failing Pulumi store tests: observe failure rolls back, duplicate final version ID is idempotent, missing observer refuses final write. Check: go test ./pkg/stack/... -run TestFinalObserveRollback|TestDuplicateFinalIdempotent|TestNoObserverRefusesFinal -count=1 fails on main
- [ ] 1.4 Add failing OpenTofu store tests: observe failure rolls back, duplicate final version ID is idempotent, out-of-order commits keep newest. Check: go test ./pkg/workspace/... -run TestFinalObserveRollback|TestDuplicateFinalIdempotent|TestConcurrentFinalKeepsNewest -count=1 fails on main

## 2. Implementation

- [ ] 2.1 Implement current-version writes in both tool transactions with injected observer port keyed by stable unit ID. Check: go test ./pkg/stack/... ./pkg/workspace/... -count=1 passes
- [ ] 2.2 Apply the final-version observe rule through the wiring delegate in the same fenced transaction of each tool store. Check: go test ./pkg/stack/... ./pkg/workspace/... -run 'Final|Observer|Concurrent' -count=1 passes

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Add e2e step with real Pulumi CLI and OpenTofu binaries showing current and final versions on existing routes. The Pulumi CLI authenticates with a personal access token on the Pulumi state route GET /api/stacks/{namespace}/{project}/{stack}/export. OpenTofu authenticates with a personal access token on GET /api/v2/workspaces/{workspace_id}/current-state-version. Check: make e2e E2E_FOCUS=state-versions runs `pulumi up` and `tofu apply` over TLS on SQLite and PostgreSQL, then reads GET /api/stacks/{namespace}/{project}/{stack}/export with the personal token and GET /api/v2/workspaces/{workspace_id}/current-state-version with the personal token
- [ ] 4.2 Update user docs for current and final state versions. Check: docs page for state versions updated in the same pull request
- [ ] 4.3 Run full check with zero findings. Check: make check with zero findings
