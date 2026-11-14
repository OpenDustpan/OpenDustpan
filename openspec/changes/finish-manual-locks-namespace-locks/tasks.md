## 1. Failing tests first

- [ ] 1.1 Add conformance cases for namespace lock cover, widen to joining units and inventory flag. Check: manuallocktest namespace coverage cases fail on main
- [ ] 1.2 Add dispatch tests: queued job waits with reason locked, running phase ends, timeout kept. Check: job dispatch locked-wait test fails on main
- [ ] 1.3 Add job-token exception tests for OpenTofu lock and Pulumi update start. Check: tool lock with pre-lock job token test fails on main
- [ ] 1.4 Add bulk lock and bulk unlock route tests with mixed results. Check: bulk lock per-item test fails on main

## 2. Definition of done

- [ ] 2.1 Wire namespace object through store, service, routes and audit entries. Check: `go test ./pkg/manuallock/... ./internal/... -run Lock -count=1` passes
- [ ] 2.2 Add e2e steps for namespace lock, dispatch wait and bulk calls against the real CLI. Check: e2e step with real OpenTofu CLI refused on locked workspace names holder and reason
- [ ] 2.3 Extend docs/guides/manual-locks.md with namespace locks and dispatch behavior. Check: docs page command runs as written
- [ ] 2.4 Run usability check from the docs page alone and make check with zero findings. Check: `make check` reports zero findings
- [ ] 2.5 Write denial audit entries for refused lock and refused bypass requests in the HTTP layer. Check: denied lock request audit test passes
- [ ] 2.6 Set the locked-refusal error text to name the holder, the reason and the read locks route. Check: locked refusal names read locks route in e2e output
