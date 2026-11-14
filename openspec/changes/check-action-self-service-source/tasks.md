## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the failing test that a reach refusal has no count of unreadable objects in pkg/access/access_test.go:792. Check: NEW test `TestReachRefusalOmitsCount` fails on main and passes after the fix

## 2. Definition of done

- [ ] 2.1 Change principalReachRefusal to omit the count while the audit record keeps it. Check: NEW test `TestReachRefusalOmitsCount` passes in `go test -race ./pkg/access/...`
- [ ] 2.2 Wire the refusal text, update the docs page, run the e2e step, and run the usability check. Confirmed by: CI (e2e step and docs page) Check: Call ANY /api/server/principals/{}/tokens passing the reach check gate with an access token, see the documented refusal without a count, read the docs page, and complete the usability check from the docs page alone. Confirmed by: CI (e2e step and docs page)
- [ ] 2.3 Run the full check. Confirmed by: CI (make check) Check: `make check` reports zero findings
