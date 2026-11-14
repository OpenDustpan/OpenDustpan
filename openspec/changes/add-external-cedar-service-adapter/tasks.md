## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write the conformance run of the adapter with a test service Check: gatetest.RunEngine fails on main
- [ ] 1.2 Write hostile transport and protocol tests Check: go test ./pkg/gate/... fails on main

## 2. Definition of done

- [ ] 2.1 Wire the configuration into server start and write the docs page in docs/reference/ Check: make docs-lint passes
- [ ] 2.2 Run an e2e step with gates on a tool path, blocked by the owner decisions Check: make e2e passes
- [ ] 2.3 Run the usability check and the full check Check: make check reports zero findings
