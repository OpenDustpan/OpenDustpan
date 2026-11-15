## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the failing server test for start, second start, name conflict and 423 during copy in NEW internal/servertest/namespace_move_test.go. It calls the NEW route POST /api/dustpan/v1/namespaces/{namespace}/moves (declaration in the route table of the adapter package (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description` then writes internal/api/apidesc/openapi.json; never edit it by hand), adapter in NEW internal/api/inventory/namespace_move.go) with an access token that holds the NEW action and a fresh step. BLOCKED by the NEW moves routes until they are built. Check: TestNamespaceMoveStart fails on main
- [ ] 1.2 Write the failing store test for the target-first handoff and ErrFenced for an old fence in NEW pkg/namespace/move_test.go. Check: TestNamespaceMoveHandoff fails on main
- [ ] 1.3 Write the failing tests for the allowlist, the 512 MiB limit, the 30 minute limit, the rollback states and the denied action, with an access token on the NEW routes, in NEW pkg/namespace/move_limits_test.go. Check: TestNamespaceMoveLimits and TestNamespaceMoveRollback fail on main

## 2. Build

- [ ] 2.1 Add the four NEW routes with the action installation:move-namespace and the fresh step. Declare them in the route table of the adapter package (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description` then writes internal/api/apidesc/openapi.json; never edit it by hand) and write the handlers in NEW internal/api/inventory/namespace_move.go. Check: The route test lists each route with its action
- [ ] 2.2 Carry the default collection and the catch-all rule by stable ID in NEW pkg/namespace/move.go. Check: TestNamespaceMoveCarry passes, including a namespace with none
- [ ] 2.3 Run the move as a durable operation with the drain of writes, the crash resume and the rollback in NEW pkg/namespace/move_operation.go. Check: TestNamespaceMoveCrash passes
- [ ] 2.4 Add the line of each NEW error code (the code and its arguments, as `TestNewCodesHaveTheirLine` prints it) in its place in the name order: `collection.name-conflict` in `internal/errcode/testdata/catalog/collection.txt`; `namespace.move-in-progress` in `internal/errcode/testdata/catalog/namespace.txt`; `namespace.move-conflict` in `internal/errcode/testdata/catalog/namespace.txt`; `namespace.move-target-unknown` in `internal/errcode/testdata/catalog/namespace.txt`. No route: the test reads the catalog only. Check: `go test ./internal/errcode/ -run TestNewCodesHaveTheirLine -count=1` passes.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run an e2e step with two dev installations. It calls the NEW moves routes with an access token of the Operator and a fresh step. BLOCKED by the NEW moves routes until they are built. Check: make e2e step namespace-move passes. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Add the docs page for moves in NEW docs/guides/namespace-move.md. It covers the target, the write block, the name conflicts and the rollback. Check: The commands of the page run in a test. `make docs-lint` passes on each edited guide.
- [ ] 4.3 Do the usability check: an operator completes a move from the docs page alone. Check: Each error names the one command that helps. A person's step.
- [ ] 4.4 Run all checks. Check: make check with zero findings. Confirmed by: CI (the agent runs the fast checks only).
