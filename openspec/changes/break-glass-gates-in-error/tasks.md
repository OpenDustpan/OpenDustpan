## 1. Failing tests first

- [ ] 1.1 Add failing conformance tests for error pass, replay, expiry on the store clock, wrong unit or kind, unlocked-write and unit-import kinds, evaluate mode, refusals, deny refusal, and override audit entry. Check: TestStateWriteBreakGlass_ErrorPassesOnce, TestStateWriteBreakGlass_ReplayRefuses, TestStateWriteBreakGlass_BadReasonRefuses, and TestStateWriteBreakGlass_DenyRefuses fail on main.
- [ ] 1.2 Implement break-glass check, single-use pass store with conditional consume, and decision record link. Check: `go test ./pkg/gate/... ./internal/api/stategate/...` passes.
- [ ] 1.3 Wire break-glass through Pulumi and TFE-compatible state.write routes with the namespace:break-gate-error action. Check: `GET /api/dustpan/v1/namespaces/{namespace}/decision-records/{id}` shows the break-glass marker on the passed write.

## 2. Definition of done

- [ ] 2.1 Add e2e step against real CLI for error pass and single use. Check: E2E step with real CLI writes state while a unit gate in enforce mode is in error, then reuses the pass and fails.
- [ ] 2.2 Write docs page for break-glass at state.write. Check: Docs page for state.write break-glass lists reason, fresh auth, 15-minute single use and override audit.
- [ ] 2.3 Run usability check, wiring check and make check. Check: `opendustpan status` names the gate in error and `make check` reports zero findings.
