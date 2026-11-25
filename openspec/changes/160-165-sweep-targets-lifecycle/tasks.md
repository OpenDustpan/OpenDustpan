## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing tests for each spec scenario, including hostile commands, refusal of a pool without the sweeps purpose, disabled target, 404, 403 and audit entry. Check: TestSweepTargetsSpec in internal/unmanagedsvc fails on main
- [ ] 1.2 Write failing store conformance tests, including the concurrent start test, in the NEW file internal/unmanaged/storetest/scan_targets_test.go. Check: TestScanStoreTargetsAndSweeps in storetest fails on main

## 2. Build

- [ ] 2.1 Add the ScanStore adapters and baseline tables for sqlite and postgres. The ScanStore port is internal/unmanaged/ports.go:326. Write the sqlite adapter in the NEW file internal/store/sqlite/scan.go, the postgres adapter in the NEW file internal/store/postgres/scan.go and the tables in the baseline file of each dialect. The task adds the NEW top-level test `TestScanStore` in a `_test.go` file of `internal/store/sqlite`, so that the Check runs it. Check: go test -timeout 60m ./internal/store/sqlite/ -run TestScanStore -count=1
- [ ] 2.2 Add the service with NewTarget, StartScan, CancelScan and the NextScan table in the NEW file internal/unmanagedsvc/service.go. NewTarget and CancelScan are NEW names. Check: go test ./internal/unmanagedsvc/ -count=1
- [ ] 2.3 Wire the NEW routes with their actions and audit entries. Write the route declaration in the NEW file internal/api/unmanaged/routes.go and the adapter in the NEW file internal/api/unmanaged/handler.go. The server test calls the NEW route `POST /api/dustpan/v1/namespaces/{namespace}/sweep-targets`. BLOCKED by the NEW route itself until this task adds it; the credential kind is an access token, and the gate is the NEW action namespace:manage-sweeps. Check: servertest: the NEW `POST /api/dustpan/v1/namespaces/{namespace}/sweep-targets` with an access token that holds namespace:manage-sweeps returns a trg- ID, and the same call with an access token without the grant returns 403
- [ ] 2.4 SECURITY. Audit a fenced refusal in the service of task 2.2, in the file internal/unmanagedsvc/service.go. When the store returns namespace.ErrFenced, the service refuses the call, changes nothing and writes one audit entry with the outcome audit.OutcomeDenied. The SQL store adapters write no audit entry for this refusal; the service is the layer that audits it. Write the hostile test TestFencedRefusalAudited in the NEW file internal/unmanagedsvc/fenced_audit_test.go. It uses a store that returns namespace.ErrFenced for NewTarget, StartScan and CancelScan, and checks the refusal and the one denied entry for each call. The test fails when the service does not write the entry. Check: `go test ./internal/unmanagedsvc/ -run TestFencedRefusalAudited -count=1`

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 e2e step: with an access token, call the NEW routes `POST /api/dustpan/v1/namespaces/{namespace}/sweep-targets`, `POST .../sweeps`, `GET .../sweeps` and `POST .../sweeps/{id}/cancel`, which pass the gates of namespace:manage-sweeps and namespace:start-sweep, against the real directory. BLOCKED by 130-138-runner-protocol-claims for any step after queued. Check: make e2e step sweep-targets.
- [ ] 4.2 Docs page for targets and sweeps. Check: docs page commands run as written in a test.go runs each command of the docs page against the e2e test server).
- [ ] 4.3 Usability check from the docs page alone, including the busy and pool errors. Check: Namespace administrator completes the path from the docs.
- [ ] 4.4 Run the full gate with the route test. Check: `make check` with zero findings.
