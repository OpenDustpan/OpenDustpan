## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing servertest in internal/servertest/backup_test.go for SIGTERM drain then backup takeover on another instance; the test calls route ANY /api/server/instances through its authorization gate with access token credential. Check: TestDrainReleasesBackupForTakeover fails on main, passes after fix.
- [ ] 1.2 Add failing readiness test in pkg/dustpan/health_internal_test.go for 503 during drain with Retry-After. Check: TestReadiness503DuringDrain calls GET /health/ready during drain; fails on main, passes after fix.

## 2. Build

- [ ] 2.1 Wire SIGTERM to draining state in NEW file internal/instance/drain.go with NEW `drain-timeout` of 30 s per instance. Check: `curl -i https://server/health/ready` shows 503 during drain.
- [ ] 2.2 Refuse new durable claims while draining and release held claims at drain end in NEW file internal/instance/drain.go. Check: GET ANY /api/server/instances with access token through existing authorization gate shows draining state.
- [ ] 2.3 Document drain, timeout and claim takeover in NEW file docs/operator/rolling-deploy.md. Check: Docs page runs as written in review and states proxy timeouts exceed drain timeout. Confirmed by: CI (an e2e step in the NEW file e2e/rolling_deploy_graceful_drain_release_docs_test.go runs each command of the docs page against the e2e test server).

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run e2e drain step against real CLI through proxy; the step calls route ANY /api/v2/state-versions through its authorization gate with access token credential. Check: Real CLI loop of `tofu apply` and `pulumi up` through proxy with no failed command during one instance drain.
- [ ] 4.2 Run wiring, route with authorization actions, docs page and usability check. Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
