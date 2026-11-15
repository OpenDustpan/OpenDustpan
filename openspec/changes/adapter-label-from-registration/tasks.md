## 1. Failing test first

- [ ] 1.1 Add `TestAdapterLabelFollowsRegistration` (NEW) in `pkg/dustpan/adapter_label_internal_test.go`: for each mounted adapter of a test server and each pattern of its route source (`routeSources`, `pkg/dustpan/apidesc.go:101`), the label that the middleware writes is the label of its adapter row; a backup route under the v1 prefix is `server`, and an inventory route is `inventory`. No route: the test reads the label map and the route sources only. Check: `go test ./pkg/dustpan/ -run TestAdapterLabelFollowsRegistration -count=1` fails on main.

## 2. Build

- [ ] 2.1 Add the metric label to each row of `adapters.allHandlers` (`pkg/dustpan/apidesc.go:30`): the NEW field `label` of `namedHandler` (:68), one of the constants of `internal/metrics/server.go:15-18`. `pulumi` is `metrics.AdapterPulumi`; `tfe` and `tofuhttp` are `metrics.AdapterOpenTofu`; `inventory` is `metrics.AdapterInventory`; every other row is `metrics.AdapterServer`. Check: `go test ./pkg/dustpan/ -run TestEveryHandlerFieldIsListed -count=1` passes.
- [ ] 2.2 Build the map from each registered pattern to its label once, when the server mounts the adapters, from `routeSources`, and replace `adapterForRequest` (`pkg/dustpan/dustpan.go:3382`) with a lookup in that map. The in-flight gauge (:3545) takes the pattern from the root mux with `http.ServeMux.Handler` before the handler runs; a request that matches no pattern gets `metrics.AdapterServer`. Remove the prefix rule and replace `TestAdapterForRequest` with the test of 1.1. Update the comment of the labels in `internal/metrics/server.go:11-13`: the label comes from the adapter that registered the route. Check: `go test ./pkg/dustpan/ ./internal/metrics/ -run 'TestAdapterLabel' -count=1` passes.

## 3. Definition of done

- [ ] 3.1 Write in the pull request body that #2863 and #2861 need no label change of their own once this lands, because their routes take the label of the server adapter. Check: the pull request body names #2863 and #2861.
- [ ] 3.2 Run the checks. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
