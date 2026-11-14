## 1. Failing tests first

- [ ] 1.1 Add failing dispatcher test: unchanged outputs skip, changed outputs start, missing prior starts. Check: `go test ./pkg/trigger/... -run TestUpstreamOutputsChanged_SkipsWhenDigestMatches` fails on main.
- [ ] 1.2 Add failing store test: final write stores root-outputs digest without secret values for both tool shapes. Check: `go test ./pkg/stack/... ./pkg/workspace/... -run TestFinalWrite_StoresRootOutputsDigest` fails on main.

## 2. Build

- [ ] 2.1 Write digest at final write and compare in Dispatcher.Upstream against the immediate prior final digest. Check: `go test ./pkg/trigger/... ./pkg/stack/... ./pkg/workspace/...` passes.
- [ ] 2.2 Add the when outputs-changed value to trigger create and replace with validation. Check: `curl -X PUT /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}` with when outputs-changed returns 200 and the read shows the value.

## 3. Definition of done

- [ ] 3.1 Add e2e step with real Pulumi and OpenTofu binaries: apply A twice, once with a changed output. Check: `make e2e TEST=trigger-outputs-changed` applies A twice and shows one follower plan only after the changed output.
- [ ] 3.2 Add the docs page for the when field. Check: `make docs-lint` passes and the triggers page documents when outputs-changed.
- [ ] 3.3 Run usability check of the path. Check: Developer follows the docs page alone from a clean install and names the one command that explains a skipped start.
- [ ] 3.4 Run full check: wiring, route actions, e2e, docs, usability, make check. Check: `make check` reports zero findings.
