## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing placement test in NEW internal/runnerproxysvc/placement_test.go: sandbox with proxy rules has one proxy per claim and no direct route. Check: `go test ./internal/runnerproxysvc/ -run TestProxyIsOnlyExit -count=1` fails on main.
- [ ] 1.2 Add failing opt-in test in NEW internal/runnerproxysvc/optin_test.go: unit without proxy rules runs with no proxy and unchanged routing. Check: `go test ./internal/runnerproxysvc/ -run TestNoProxyWithoutRules -count=1` fails on main.
- [ ] 1.3 Add failing identity test in NEW internal/runnerproxysvc/identity_test.go: unnamed host, SNI and Host mismatch, and non-canonical path get refusal. Check: `go test ./internal/runnerproxysvc/ -run TestRequestIdentityChecks -count=1` fails on main.
- [ ] 1.4 Add failing bound dial test in NEW internal/runnerproxysvc/dial_test.go: tunnel dial connects only to guard-checked address, with mixed allowed and denied addresses. Check: `go test ./internal/runnerproxysvc/ -run TestBoundDialChecksEachAddress -count=1` fails on main.
- [ ] 1.5 Add failing log test in NEW internal/runnerproxysvc/blockedlog_test.go: refused request writes blocked line with host and canonical path, fixed reason, and no secret. Check: `go test ./internal/runnerproxysvc/ -run TestBlockedLineHasHostAndPath -count=1` fails on main.

## 2. Build

- [ ] 2.1 Build proxy mode of dustpan-launcher in NEW internal/runnerproxysvc/proxy.go with bound dialer, egress check (pkg/egress), tunnel rule to server host, and per-isolation start. Check: `go test -race ./internal/runnerproxysvc/ -count=1` passes.
- [ ] 2.2 Wire coordinator start in NEW internal/runnerproxysvc/coordinator.go, sandbox route with drop of other packets, fail-closed loss and start failure. Runs over the runner protocol routes under /runner/v1 with the runner key credential. BLOCKED by job-proxy-rule-sets for the proxy session paths and the pool settings. Check: `make e2e E2E_FOCUS=job-proxy-runtime` passes on SQLite and PostgreSQL through real binary over TLS. Confirmed by: CI (the agent writes the e2e test).
- [ ] 2.3 Add e2e step in NEW internal/runnerproxysvc/e2e_proxy_test.go with real runner job: allowed host passes, unnamed host refused with log line. Uses the runner protocol under /runner/v1 with the runner key and the job token. BLOCKED by job-proxy-rule-sets for proxy rule sets on the unit. Check: `make e2e E2E_FOCUS=job-proxy-runtime-blocked` passes; job log of refused host holds blocked line. Confirmed by: CI (the agent writes the e2e test).

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Declare authorization actions for proxy session paths under /runner/v1. Check: Route test lists no new public route and proxy session paths carry runner authorization actions.
- [ ] 4.2 Write docs page in NEW docs/guides/job-proxy-runtime.md: when proxy runs, tunnel behavior, pool ranges and upstream proxy, blocked log line, fail-closed loss. Check: `make docs-lint` passes; docs page commands run as written. Confirmed by: CI (an e2e step in the NEW file e2e/job_proxy_runtime_guard_docs_test.go runs each command of the docs page against the e2e test server).
- [ ] 4.3 Run usability check with runner operator path from docs page alone, then full definition of done. Check: `make check` passes with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
