## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing placement test: sandbox with proxy rules has one proxy per claim and no direct route. Check: `go test ./internal/runnerproxysvc/ -run TestProxyIsOnlyExit -count=1` fails on main.
- [ ] 1.2 Add failing opt-in test: unit without proxy rules runs with no proxy and unchanged routing. Check: `go test ./internal/runnerproxysvc/ -run TestNoProxyWithoutRules -count=1` fails on main.
- [ ] 1.3 Add failing identity test: unnamed host, SNI and Host mismatch, and non-canonical path get refusal. Check: `go test ./internal/runnerproxysvc/ -run TestRequestIdentityChecks -count=1` fails on main.
- [ ] 1.4 Add failing bound dial test: tunnel dial connects only to guard-checked address, with mixed allowed and denied addresses. Check: `go test ./internal/runnerproxysvc/ -run TestBoundDialChecksEachAddress -count=1` fails on main.
- [ ] 1.5 Add failing log test: refused request writes blocked line with host and canonical path, fixed reason, and no secret. Check: `go test ./internal/runnerproxysvc/ -run TestBlockedLineHasHostAndPath -count=1` fails on main.

## 2. Build

- [ ] 2.1 Build proxy mode of dustpan-launcher with bound dialer, egress check, tunnel rule to server host, and per-isolation start. Check: `go test -race ./internal/runnerproxysvc/ -count=1` passes.
- [ ] 2.2 Wire coordinator start, sandbox route with drop of other packets, fail-closed loss and start failure. Runs over the runner protocol routes under /runner/v1 with the runner key credential. BLOCKED by job-proxy-rule-sets for the proxy session paths and the pool settings. Check: `make e2e E2E_FOCUS=job-proxy-runtime` passes on SQLite and PostgreSQL through real binary over TLS.
- [ ] 2.3 Add e2e step with real runner job: allowed host passes, unnamed host refused with log line. Uses the runner protocol under /runner/v1 with the runner key and the job token. BLOCKED by job-proxy-rule-sets for proxy rule sets on the unit. Check: `make e2e E2E_FOCUS=job-proxy-runtime-blocked` passes; job log of refused host holds blocked line.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Declare authorization actions for proxy session paths under /runner/v1. Check: Route test lists no new public route and proxy session paths carry runner authorization actions.
- [ ] 4.2 Write docs page: when proxy runs, tunnel behavior, pool ranges and upstream proxy, blocked log line, fail-closed loss. Check: `make docs-lint` passes; docs page commands run as written.
- [ ] 4.3 Run usability check with runner operator path from docs page alone, then full definition of done. Check: `make check` passes with zero findings.
