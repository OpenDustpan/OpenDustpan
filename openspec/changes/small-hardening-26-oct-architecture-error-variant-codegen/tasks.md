## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Fix

- [ ] 1.1 Add failing unit test for duplicate generated error-variant name in the NEW file `internal/api/apidesc/errorvariants_duplicate_test.go`. Check: The NEW test file fails on main and passes after the fix.
- [ ] 1.2 Refuse duplicate generated name in checkedErrorVariants in `internal/api/apidesc/errorvariants.go:79`. Check: `go test ./internal/api/apidesc -count=1` passes.

## 2. Definition of done

- [ ] 2.1 Confirm wiring in `internal/api/apidesc/errorvariants.go:52`: duplicate error from checkedErrorVariants fails API description build at server start. Check: `go test ./pkg/dustpan -run TestAPIDescriptionErrorVariantsMatchTheCatalog -count=1` passes.
- [ ] 2.2 Run usability check from the server-start error alone and run `make check` in `internal/api/apidesc/errorvariants.go:79`. Confirmed by: CI (`make check`). Check: `make check` passes with zero findings.
