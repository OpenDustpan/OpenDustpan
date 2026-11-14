## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing tests for the Ref fields of NotFoundError and AlreadyExistsError in NEW pkg/stack/errors_args_test.go. Check: go test ./pkg/stack -run TestNotFoundArgs fails on main
- [ ] 1.2 Write failing tests for the update, state, lease and batch fields in NEW pkg/stack/errors_args_test.go. Check: go test ./pkg/stack -run TestUpdateErrorArgs fails on main
- [ ] 1.3 Write failing tests for the reveal path argument and for the innermost precedence in NEW pkg/stack/errors_args_test.go. Check: go test ./pkg/stack -run 'TestRevealPathArgs|TestNestedPrecedence' fails on main
- [ ] 1.4 Write failing store tests for both dialects in pkg/store/storetest. Check: go test ./pkg/store/storetest -run TestStoreErrorArgs fails on main

## 2. Build

- [ ] 2.1 Implement the typed errors in pkg/stack and read them in internal/api/pulumi with errcode.New. Check: go test ./pkg/stack ./internal/errcode passes
- [ ] 2.2 Return UpdateConflictError from the delete refusal in pkg/store/sqlite/lifecycle.go and its PostgreSQL copy, with Active checked. Check: go test ./pkg/store/storetest -run TestDeleteRefusalArgs passes

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Update the frozen lists and the API description for the added arguments in internal/errcode/testdata/catalog. Confirmed by: CI (make check) Check: the errcode shipped-list hash test passes
- [ ] 4.2 Check stack, rename, delete, update and reveal flows with the real Pulumi CLI with an access token — BLOCKED by <Pulumi route-to-method map not in pack>. Confirmed by: CI (make e2e) Check: `make e2e` passes
- [ ] 4.3 Add the docs page for the error arguments and check usability from the page alone, then run make check with zero findings. Confirmed by: CI (make check) Check: `make check` with zero findings
