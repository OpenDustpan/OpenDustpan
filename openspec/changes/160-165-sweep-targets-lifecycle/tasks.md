## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing tests for each spec scenario, including hostile commands, namespace pool refusal, disabled target, 404, 403 and audit entry. Check: TestSweepTargetsSpec in internal/unmanagedsvc fails on main
- [ ] 1.2 Write failing store conformance tests, including the concurrent start test. Check: TestScanStoreTargetsAndSweeps in storetest fails on main

## 2. Build

- [ ] 2.1 Add the ScanStore adapters and baseline tables for sqlite and postgres. Check: go test ./pkg/store/sqlite/ -run TestScanStore -count=1
- [ ] 2.2 Add the service with NewTarget, StartScan, CancelScan and the NextScan table. Check: go test ./internal/unmanagedsvc/ -count=1
- [ ] 2.3 Wire the NEW routes with their actions and audit entries. The test calls the NEW route `POST /api/dustpan/v1/namespaces/{namespace}/sweep-targets`, passes the gate of the action namespace:manage-sweeps, and uses an access token as the credential. Check: servertest: the NEW `POST /api/dustpan/v1/namespaces/{namespace}/sweep-targets` with an access token that holds namespace:manage-sweeps returns a trg- ID, and the same call with an access token without the grant returns 403

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 e2e step: with an access token, call the NEW routes `POST /api/dustpan/v1/namespaces/{namespace}/sweep-targets`, `POST .../sweeps`, `GET .../sweeps` and `POST .../sweeps/{id}/cancel`, which pass the gates of namespace:manage-sweeps and namespace:start-sweep, against the real directory. BLOCKED by 130-138-runner-protocol-claims for any step after queued. Check: make e2e step sweep-targets
- [ ] 4.2 Docs page for targets and sweeps. Check: docs page commands run as written in a test
- [ ] 4.3 Usability check from the docs page alone, including the busy and pool errors. Check: Namespace administrator completes the path from the docs
- [ ] 4.4 Run the full gate with the route test. Check: `make check` with zero findings
