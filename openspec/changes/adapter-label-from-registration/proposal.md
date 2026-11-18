---
version: 1
---

Issue: #3162.

Short form: bug. Owner lane: F4 (it holds #2863 and #2861).

## Problem

**Persona: site administrator.** The administrator reads the request
metrics and the request log by adapter, to see which API is slow or fails.
Today a backup request shows as `inventory` as soon as its route moves to
the `/api/dustpan/v1/` prefix, because the label comes from the path
prefix, not from the adapter that serves the route. Every move of a route
changes its label, and the dashboard of the administrator is wrong without
a warning.

## Evidence

- `pkg/dustpan/dustpan.go:3493` (func adapterForRequest) maps the route
  pattern, or the path when no pattern matched, by prefix: every name under
  `v1Prefix` (`/api/dustpan/v1/`, :3723) except capabilities, `sign-in/` and
  `me/` gives `inventory`.
- The middleware calls it twice: once with the path only for the in-flight
  gauge (:3654), and once with `r.Pattern` after the handler (:3668), for
  `ObserveRequest` and the `adapter` attribute of the log line.
- `mountV1` (`pkg/dustpan/dustpan.go:3788`) registers each v1 route on the
  root mux by path only, with no method. A map that is keyed by method
  patterns does not match the root pattern that the gauge reads, so the gauge
  and the request counter can give two labels to one request (finding of the
  M7 lane on task 2.2).
- The adapters are listed by name in `pkg/dustpan/apidesc.go:29`
  (func adapters.allHandlers), and `routeSources` (:103, func adapters.routeSources) gives the
  registered patterns of each mounted adapter.
- On the head of #2863 (`f4/1497-backups`), the backup and restore routes are
  under the v1 prefix ((read only) `internal/api/server/routes.go`, line 170 (func Handler.backupRoutes)) and the
  prefix rule is unchanged, so they show as `inventory`. #2861 moves the audit
  chain, sweep and override routes the same way.
- ADR 32 decision 5: "The route label is the registered pattern, never the
  path. The adapter label is `pulumi`, `opentofu`, `inventory` or `server`."

## Fix boundary

- A structure that removes the bug class: each row of `allHandlers` names
  its metric label, and the server builds one map from each registered
  pattern to the label of its adapter when it mounts the adapters. The
  middleware reads the label of `r.Pattern` from that map. No code reads the
  path prefix.
- The labels stay the four of ADR 32. The rows: `pulumi` gives `pulumi`;
  `tfe` and `tofuhttp` give `opentofu`; `inventory` gives `inventory`; every
  other adapter gives `server`.
- The in-flight gauge takes the pattern from the root mux before the
  handler runs (`http.ServeMux.Handler`), so it uses the same map. The map
  holds the path of each source pattern, with the method removed, because
  `mountV1` registers v1 routes on the root mux by path only. It also holds
  each pattern that the server registers directly on the root mux, with the
  label of its adapter.
- No routing change. No rule picks a label when two adapters register the
  same path with different labels: building the map fails with an error that
  names the path and both adapters, and the server does not start.
- A request that matches no pattern gets `server`.
- Out of scope: new label values; the route label.

## Check

`go test ./pkg/dustpan/ -run 'TestAdapterLabel' -count=1` passes (it includes the test of the duplicate path and the test that the gauge label and the counter label are the same), and
`make check` reports zero findings (Confirmed by: CI).

## ADR check

**ADR 32: Observing the server** (not owner-tagged; changed by ADR 34, 60
and 210 in other decisions; ADR 34 changes decisions 3 and 8, ADR 60 decisions 1 and 6,
ADR 210 the route paths and action names, none of them the label set). Quote, decision 5: "The route label is the
registered pattern, never the path. The adapter label is `pulumi`,
`opentofu`, `inventory` or `server`." Effect: the label set stays; the label
now follows the registration, as the route label already does. The in-flight gauge is a metric of the same decision 5 family ("the requests in flight", decision 5), so it takes the same label as the counter. Conflict:
none. No other ADR or RFC (rg over the ADRs and the RFCs for adapter label, in-flight gauge and metrics labels) sets the label rule.

## Open questions

none. Considered: (1) a label value for each adapter (refused here: ADR 32
fixes four values, and a new set is a new decision); (2) keep the prefix
rule and add each moved prefix to it (refused: each later move breaks it
again, which is the bug class); (3) `inventory` also for the context
adapter (refused: the label names the inventory adapter only, and every
other tool-neutral adapter is `server`).
