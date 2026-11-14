## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing dispatcher test: unchanged outputs skip, changed outputs start, missing prior starts, mixed when values, retry replays same pair. It drives Dispatcher.Upstream in process; no route or credential applies. Check: `go test ./pkg/trigger/... -run TestUpstreamOutputsChangedSkipsWhenDigestMatches` fails on main.
- [ ] 1.2 Add failing store test: final write stores the digest without secret values for both tool shapes, with the secret-mark flip case. Check: `go test ./pkg/stack/... ./pkg/workspace/... -run TestFinalWriteStoresRootOutputsDigest` fails on main.
- [ ] 1.3 Add failing trigger save test: unknown when refused, default applied, change reads back. Check: `go test ./pkg/trigger/... -run TestSaveTriggerWhenValue` fails on main.

## 2. Build

- [ ] 2.1 Write the digest at final write and compare in Dispatcher.Upstream against the immediate prior digest. In process; no route or credential applies. The baseline of each dialect holds the new column. Check: `go test ./pkg/trigger/... ./pkg/stack/... ./pkg/workspace/...` passes.
- [ ] 2.2 Add when outputs-changed to trigger create and replace with validation. Check: `PUT /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}` with a personal access token and when outputs-changed returns 200.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Add an e2e step with real CLI binaries: apply A twice, once with a changed output, then read triggers with `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers` and a personal access token. Check: `make e2e` runs the step over TLS on SQLite and PostgreSQL and shows one follower plan only after the changed output.
- [ ] 4.2 Add the docs page for the when field. Check: `make docs-lint` passes and the triggers page documents when outputs-changed.
- [ ] 4.3 Run the usability check of the path. Check: Developer follows the docs page alone and names the one command that explains a skipped start.
- [ ] 4.4 Run the full check: wiring, route actions, e2e, docs, usability. Check: `make check` reports zero findings.
