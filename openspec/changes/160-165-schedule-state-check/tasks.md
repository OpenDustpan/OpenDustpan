## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing schedule Tick test for due sweeps and due offsets. Check: `TestScheduleTickStartsDueSweepsAndStateChecks` fails on main, passes after.
- [ ] 1.2 Add failing RunStateCheck test for contested keys and open finding keys. Check: `TestStateCheckClassifiesContestedAndFindingKeys` fails on main, passes after.
- [ ] 1.3 Add failing busy-guard test for the NEW error code `unmanaged.scan-busy`. Check: `TestStateCheckRefusesDoubleStart` fails on main, passes after.

## 2. Build

- [ ] 2.1 Build schedule worker with fence check and idempotent start keys. Check: `go test ./internal/unmanaged/... ./pkg/durable/...` passes.
- [ ] 2.2 Build RunStateCheck from the resource index with audit entry and 10-minute expiry. Check: `go test ./internal/unmanaged/...` passes; audit entry holds namespace ID and counts.
- [ ] 2.3 Wire the NEW `POST /api/dustpan/v1/namespaces/{namespace}/state-checks` route with `namespace:start-sweep`. Check: Servertest calls existing route `GET /api/audit/entries` with an access token and the `namespace:start-sweep` gate; BLOCKED by NEW `POST /api/dustpan/v1/namespaces/{namespace}/state-checks` until its route lands.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run schedule Tick against the real store with fence move hostile case. Check: Servertest calls existing route `GET /api/audit/entries` with an access token and shows one start per due item plus fence-move refusal.
- [ ] 4.2 Write docs page for schedule interval, offset spread, and state check output. Check: Docs page commands run as written against a dev server.
- [ ] 4.3 Usability check, e2e step on the real store path, and full check. Check: E2E calls existing route `GET /api/audit/entries` with an access token and lists state-check rows; `make check` reports zero findings.
