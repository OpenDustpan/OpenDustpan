## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing tests for error pass, replay, expiry, wrong unit or kind, evaluate mode, refusals, deny refusal and audit entry. Check: TestStateWriteBreakGlass_ErrorPassesOnce, TestStateWriteBreakGlass_ReplayRefuses, TestStateWriteBreakGlass_BadReasonRefuses and TestStateWriteBreakGlass_DenyRefuses fail on main.
- [ ] 1.2 Implement the check, the pass store and the record marker. Check: `go test ./pkg/gate/...` passes.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Wire the fields into the Pulumi and TFE-compatible routes with the action namespace:break-gate-error. Check: Route test lists the action and its object.
- [ ] 3.2 Add an e2e step with the real Pulumi CLI. Check: make e2e step: error pass proceeds once and replay fails.
- [ ] 3.3 Write the docs page and run the usability check. Check: The page lists reason, fresh auth, 15 minutes and single use; `make check` reports zero findings.
