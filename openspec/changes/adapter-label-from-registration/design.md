## Context

The request middleware reads the adapter label at two points. The in-flight
gauge needs it before the handler runs, and the request counter and the
request log need it after the handler. `mountV1` (read only:
`pkg/dustpan/dustpan.go`, func mountV1) registers each v1 route on the root
mux by path only. The source patterns of an adapter (`routeSources`,
`pkg/dustpan/apidesc.go:103`) have a method and a path. A map that is keyed by
the method pattern gives a label to the request pattern after the handler, but it does
not give one to the root pattern that the gauge reads before the handler.

## Decisions

1. **One map, two key forms.** The label map is built once, when the server
   mounts the adapters. For each source pattern it holds the pattern with the
   method (the key that the request pattern has after the handler) and the path
   without the method (the key that the root mux gives to a v1 route before
   the handler). It also holds each pattern that `mountAPI` registers directly
   on the root mux (for example `/api/`), with the label of its adapter.
2. **The gauge reads the root mux pattern.** Before the handler runs, the
   middleware calls `http.ServeMux.Handler` on the root mux, takes the
   pattern, and reads its label from the map. The counter and the log read
   the label of the request pattern from the same map. Both use the same lookup, so
   one request has one label.
3. **No routing change.** `mountV1` keeps its path-only registration. The map
   follows the routing, not the reverse.
4. **A duplicate path with different labels fails at build time.** If two
   adapters register the same path (the method removed) with different labels,
   building the map returns an error that names the path and both adapters.
   The server does not start. No rule picks one label, because a rule that the
   operator cannot see is a hidden rule. Two adapters that register the same
   path with the same label are not an error.
5. **Unmatched requests.** A request that matches no pattern gets
   `metrics.AdapterServer`. The label set stays the four values of ADR 32.

## Tests

All tests are in `pkg/dustpan/adapter_label_internal_test.go`, and their names
start with `TestAdapterLabel`.

- `TestAdapterLabelFollowsRegistration` (task 1.1) checks the label of each
  mounted adapter and each of its patterns.
- `TestAdapterLabelDuplicatePathFails` builds the map from two adapters that
  register one path with two labels, and expects the error that names the path
  and both adapters. A second row, with the same label, expects no error.
- `TestAdapterLabelGaugeMatchesCounter` sends one v1 request to each adapter
  that has v1 routes, and expects that the label of the in-flight gauge and
  the label of the request counter are the same.

## ADR check

ADR 32 decision 5 is the only decision that sets the label values. It is not
owner-tagged. The label set does not change. See the proposal for the quoted
line and the Changed-by chain.
