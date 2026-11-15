## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing schedule Tick test for due sweeps and due offsets in NEW file internal/unmanaged/schedule_test.go. Check: `TestScheduleTickStartsDueSweepsAndStateChecks` fails on main, passes after.
- [ ] 1.2 Add failing RunStateCheck test for contested keys and open finding keys in NEW file internal/unmanaged/statecheck_test.go. Check: `TestStateCheckClassifiesContestedAndFindingKeys` fails on main, passes after.
- [ ] 1.3 Add failing busy-guard test for the NEW error code `unmanaged.scan-busy` in internal/unmanaged/unmanagedtest/mem_test.go. Check: `TestStateCheckRefusesDoubleStart` fails on main, passes after.

## 2. Build

- [ ] 2.1 Build schedule worker with fence check and idempotent start keys in NEW file internal/unmanaged/schedule.go. Check: `go test ./internal/unmanaged/... ./pkg/durable/...` passes.
- [ ] 2.2 Build RunStateCheck (NEW) from the resource index with audit entry and 10-minute expiry in NEW file internal/unmanaged/statecheck.go. Check: `go test ./internal/unmanaged/...` passes; audit entry holds namespace ID and counts.
- [ ] 2.3 Wire the NEW `POST /api/dustpan/v1/namespaces/{namespace}/state-checks` route with `namespace:start-sweep`. Declare it in internal/api/server/routes.go and add its adapter in NEW file internal/api/server/statechecks.go. Join the new apidesc labels into joinLabels in internal/api/server/operations.go (:187-198), the one join point of the labels. Check: Servertest calls existing route `GET /api/audit/entries` with an access token and the `namespace:start-sweep` gate; BLOCKED by NEW `POST /api/dustpan/v1/namespaces/{namespace}/state-checks` until its route lands.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run schedule Tick against the real store with fence move hostile case. Check: Servertest calls existing route `GET /api/audit/entries` with an access token and shows one start per due item plus fence-move refusal.
- [ ] 4.2 Write docs page for schedule interval, offset spread, and state check output in NEW file docs/guides/schedule-state-check.md. Check: Docs page commands run as written against a dev server. Confirmed by: CI (an e2e step in the NEW file e2e/160_165_schedule_state_check_docs_test.go runs each command of the docs page against the e2e test server). `make docs-lint` passes on each edited guide.
- [ ] 4.3 Usability check, e2e step on the real store path, and full check. Check: E2E calls existing route `GET /api/audit/entries` with an access token and lists state-check rows; `make check` reports zero findings. A person's step. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).
