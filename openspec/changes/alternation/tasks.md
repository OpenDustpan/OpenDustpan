## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. E2e

- [ ] 1.1 In e2e/ha_alternation_test.go (NEW) add make e2e-ha harness with three serve binaries on one PostgreSQL database and S3 service container behind round-robin TLS proxy; the step calls ANY /api/server/instances, passes the /health/ready gate, uses an access token credential Check: TestE2EHAAlternation fails on main: proxy record lacks one instance for one tool. Confirmed by: CI (the agent writes the e2e test).
- [ ] 1.2 In e2e/ha_alternation_test.go (NEW) run the full milestone 1 Pulumi scenario through the proxy with shared registry URL key; the step calls ANY /api/server/instances, passes the /health/ready gate, uses an access token credential Check: TestE2EHAAlternation runs the Pulumi scenario with exit 0 through the proxy. Confirmed by: CI (the agent writes the e2e test).
- [ ] 1.3 In e2e/ha_alternation_test.go (NEW) run the full milestone 2 OpenTofu scenario through the proxy with shared registry URL key; the step calls ANY /api/server/instances, passes the /health/ready gate, uses an access token credential Check: TestE2EHAAlternation runs the OpenTofu scenario with exit 0 through the proxy. Confirmed by: CI (the agent writes the e2e test).
- [ ] 1.4 In e2e/ha_alternation_test.go (NEW) check the proxy forwarding record for alternation with no sticky session; the step calls ANY /api/server/instances, passes the /health/ready gate, uses an access token credential Check: TestE2EHAAlternation asserts each instance served each tool and successive requests alternate; voids and retries at most 3 times naming the instance. Confirmed by: CI (the agent writes the e2e test).
- [ ] 1.5 In e2e/ha_alternation_test.go (NEW) cover lock handover across instances through the proxy; the step calls ANY /api/server/instances, passes the /health/ready gate, uses an access token credential Check: TestE2EHAAlternationLockHandover takes a workspace lock via one instance and releases it via another with exit 0. Confirmed by: CI (the agent writes the e2e test).

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Update docs/guides/high-availability.md with the three-instance proxy layout Check: High-availability guide names the alternation topology, proxy TLS trust setup for both CLIs, shared S3 bucket check, and the proxy record check
- [ ] 3.2 Run full definition of done: wiring through proxy, real CLI traffic, docs page, usability check from docs page, make check Check: `make check` reports zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
