## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write the conformance run of the adapter with a test service in pkg/gate/external_engine_conformance_test.go (NEW). It calls gatetest.RunEngine (pkg/gate/gatetest/run_engine.go:29) with a factory for NewExternalEngine (NEW). Check: gatetest.RunEngine fails on main
- [ ] 1.2 Write hostile transport and protocol tests in pkg/gate/external_engine_hostile_test.go (NEW). Check: go test ./pkg/gate/... fails on main

## 2. Definition of done

- [ ] 2.1 Wire the configuration into server start and write the docs page docs/reference/external-cedar-service.md (NEW). Check: make docs-lint passes
- [ ] 2.2 Add the e2e step in e2e/external_cedar_gate_test.go (NEW): set the gate engine to the external Cedar service with `PUT /api/dustpan/v1/settings`, put a gate with `PUT /api/dustpan/v1/namespaces/{namespace}/gates/{id}`, both with an access token, then run a plan of the tool path that the gate decides. The CI image is the pinned image of the official Rust cedar CLI (ADR 377 decision 2), pinned by digest. Check: the e2e step passes in CI. Confirmed by: CI (the agent writes the e2e test).
- [ ] 2.3 Run the usability check and the full check Check: make check reports zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
