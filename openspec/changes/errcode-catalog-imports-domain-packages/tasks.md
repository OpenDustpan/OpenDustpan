## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Fix

- [ ] 1.1 Add the NEW import guard test `TestCatalogHasNoDomainImports` in internal/errcode that denies imports of pkg/gate, pkg/identity, pkg/ipallow and internal/sessionauth. Check: The NEW test `TestCatalogHasNoDomainImports` fails on main and passes after the fix: `go test ./internal/errcode -run TestCatalogHasNoDomainImports -count=1`.
- [ ] 1.2 Move the Forbidden domain-error mapping from internal/errcode/errcode.go to internal/api/problem and internal/api/refusal with errors.As matching in the order gate, identity, ipallow, sessionauth, and pass plain fields to errcode.New. Check: `go test ./internal/errcode ./internal/api/problem ./internal/api/refusal -count=1` passes plus `go build ./...`.
- [ ] 1.3 Delete the removed errcode helpers from internal/errcode/errcode.go and update each caller found by rg to pass plain code and values. Check: `rg -n -F errcode.New pkg internal cmd` lists only plain-field callers plus `go build ./...`.

## 2. Definition of done

- [ ] 2.1 Run the affected unit tests, the golden check, and make check. Check: `go test ./internal/errcode ./internal/api/... -count=1` passes, goldens are unchanged and `make check` reports zero findings. Confirmed by: CI (make check).
