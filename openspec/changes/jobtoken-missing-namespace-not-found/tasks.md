## 1. Failing test first

- [ ] 1.1 Add the NEW case `testIssueIntoMissingNamespace` ("a write to a namespace with no row fails with namespace.ErrNotFound") in `pkg/jobtoken/jobtokentest/issue_cases.go`, next to the case of a stale fence (:553), and list it in `issueCases` (:15). It makes a namespace, a unit, a starter and a job, deletes the namespace with the delete-namespace function of the harness (the field DeleteNamespace of jobtokentest.Harness), and calls `Issue` with the old fence; it expects `namespace.ErrNotFound`, not `namespace.ErrFenced`, and no record. Raise `caseCount` in `pkg/jobtoken/jobtokentest/jobtokentest.go:141` by one. No route: the test calls the store only. Check: `go test ./pkg/jobtoken/jobtokentest/ -run TestSuiteOverAMemoryStore -count=1` fails on main with ErrFenced from the memory double.

## 2. Build

- [ ] 2.1 In `pkg/jobtoken/jobtokentest/mem_test.go:176` (func memWorld.issueLocked), return `namespace.ErrNotFound` when the namespace of the fence has no entry in `nsByName`, before the check of the failover version, and keep `namespace.ErrFenced` for a stale fence and a fence of another namespace. Update the comment of `namespaceOf` (:218-220). Check: `go test ./pkg/jobtoken/jobtokentest/ -run TestSuiteOverAMemoryStore -count=1` passes.
- [ ] 2.2 In the port doc of `jobtoken.Store.Issue` (`pkg/jobtoken/store.go:16-41`), add `namespace.ErrNotFound` when the namespace of the fence has no row, and put it before `namespace.ErrFenced` in the order of the rules (:37-41). Check: `go vet ./pkg/jobtoken/` exits 0.

## 3. Definition of done

- [ ] 3.1 Write in the pull request body that the SQL job token stores of #2916 (in (read only) `pkg/store/sqlite` and (read only) `pkg/store/postgres`, on main; this change adds no port method and edits no adapter) run the new case on SQLite and PostgreSQL, and that they return `namespace.ErrNotFound` already. Check: the pull request body names #2916 and the case.
- [ ] 3.2 Run the checks. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
