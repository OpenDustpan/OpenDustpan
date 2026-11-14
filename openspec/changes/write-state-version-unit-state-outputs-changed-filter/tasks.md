## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add a failing dispatcher test: unchanged outputs skip, changed outputs start, missing prior starts, mixed when values, retry replays the same pair. It drives Dispatcher.Upstream in process. BLOCKED by no route: the test calls the port with no credential. Check: `go test ./pkg/trigger/... -run TestUpstreamOutputsChangedSkipsWhenDigestMatches` fails on main.
- [ ] 1.2 Add a failing store test: publishUnitState stores the digest without secret values for both tool shapes, with the secret-mark flip case. In process; no route or credential. Check: `go test ./pkg/store/... -run TestPublishUnitStateStoresRootOutputsDigest` fails on main.
- [ ] 1.3 Add a failing trigger save test through `PUT /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}` with a personal access token: unknown when refused, default applied, change reads back. Check: `go test ./pkg/trigger/... -run TestSaveTriggerWhenValue` fails on main.

## 2. Build

- [ ] 2.1 Write the digest in publishUnitState and compare in Dispatcher.Upstream against the immediate prior digest. BLOCKED by no route: both run in process on the store and the dispatcher, with no credential. The baseline of each dialect holds the new column. Check: `go test ./pkg/trigger/... ./pkg/store/...` passes.
- [ ] 2.2 Add when outputs-changed to trigger create and replace with validation. Check: `PUT /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}` with a personal access token and when outputs-changed returns 200.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Add an e2e step with real CLI binaries: apply A twice, once with a changed output, then read `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers` with a personal access token. Check: `make e2e` runs the step over TLS on SQLite and PostgreSQL and shows one follower plan only after the changed output.
- [ ] 4.2 Add the docs page for the when field. Check: `make docs-lint` passes and the triggers page documents when outputs-changed.
- [ ] 4.3 Run the usability check of the path. Check: Developer follows the docs page alone and names the one command that explains a skipped start.
- [ ] 4.4 Run the full check. The route `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers` keeps its action, tested with a personal access token. Check: `make check` reports zero findings.
