## 0. Confirm against the code first

- [ ] 0.1 Confirm the changes `agent-connection-document-and-secrets`, `agent-secret-filter`, `agent-model-adapters`, `agent-connection-probe` and `dusty-agent-principal-on-model-connect` are on main. BLOCKED by them.
- [ ] 0.2 Confirm there is no route under `/api/agent/connection` that writes: `rg -n 'agent/connection' internal/api`.
- [ ] 0.3 Read the `ConnectModel` of main and note where it writes the connection.
- [ ] 0.4 Read ADR 129 decisions 29 and 32 and note any change since the spec.

## 1. Failing tests first

- [ ] 1.1 Route tests: the answers 202, 400, 412, 428, 429 and 403, as a table.
- [ ] 1.2 Service tests: two writers, earlier probes canceled, a stale version at the promote step, a failed audit write rolls back.
- [ ] 1.3 The tests of `ConnectModel` with `ConnectionStore.Promote`.
- [ ] 1.4 For each guard of the layers table, a test that fails when the guard is removed. Check: remove the guard, see the test fail, put the guard back.

## 2. Build

- [ ] 2.1 `internal/agentsvc/connect.go`: `PutConnection`.
- [ ] 2.2 `internal/api/server`: the route, the action and the object. Error codes in the catalog file.
- [ ] 2.3 Change the connection write of `ConnectModel` to `ConnectionStore.Promote`. Remove the caller-less `SettingsStore.PutConnection` if the change 1 did not.
- [ ] 2.4 The last step of the probe calls `ConnectModel`.

## 3. Side effects

- [ ] 3.1 `make generate`, then `go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`, then `make clients-generate`. Never edit a generated file by hand.
- [ ] 3.2 The page `docs/guides/connect-a-model.md`: the document, the secret item, the `PUT`, the probe and the result, with each command. A test runs each command.

## 4. Definition of done

- [ ] 4.1 e2e step: create a secret item, send the `PUT` against a fake model server, read the probe until `passed`, read the connection, and see Dusty in the principal list.
- [ ] 4.2 Usability check: an administrator with the page only connects a model, and fixes a document after a failed probe.
- [ ] 4.3 `make check` passes with zero findings. Paste the output into the pull request.
