## 1. Failing tests

- [ ] 1.1 Add failing conformance run of the service adapter with a test service Check: gatetest.RunEngine passes against the test service in pkg/gate/gatetest/run_engine.go
- [ ] 1.2 Add failing tests for bad auth, missing echo, version mismatch, unknown and extra policy IDs, timeout, and redirect Check: go test ./pkg/gate/... reports hostile transport and protocol answers mapped to gate.ErrUnavailable and diagnostics to gate.ErrRuleFailed
- [ ] 1.3 Add failing test for save fallback refusal and outage paths Check: go test ./pkg/gate/... reports save refused with ErrUnavailable during outage and access-limit-only calls still decided

## 2. Build

- [ ] 2.1 Implement the external service adapter of gate.Engine Check: go test ./pkg/gate/... passes with the new adapter
- [ ] 2.2 Wire configuration and the save check operation with refusal fallback Check: go test ./pkg/gate/... passes for save check with refusal fallback and miss record
- [ ] 2.3 Add the CI job with the pinned service image Check: CI log shows the Rust validator run through the pinned service image
- [ ] 2.4 Write the configuration page in docs/reference/ Check: docs/reference page renders and make docs-lint passes

## 3. Definition of done

- [ ] 3.1 Wire the route with its authorization actions Check: API route for gate save declares its authorization actions and the object of each action
- [ ] 3.2 Run e2e step against the real CLI with gates on a tool path Check: `make e2e` passes with gates evaluated on tool paths
- [ ] 3.3 Run usability check of the configuration path Check: docs/reference page lets the persona complete setup alone and each error names the one command that helps
- [ ] 3.4 Run full check with zero findings Check: `make check` reports zero findings
