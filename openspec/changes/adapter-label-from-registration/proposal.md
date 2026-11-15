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

- `pkg/dustpan/dustpan.go:3382` (func adapterForRequest) maps the route
  pattern, or the path when no pattern matched, by prefix: every name under
  `v1Prefix` (`/api/dustpan/v1/`, :3613) except capabilities, `sign-in/` and
  `me/` gives `inventory`.
- The middleware calls it twice: once with the path only for the in-flight
  gauge (:3545), and once with `r.Pattern` after the handler (:3558), for
  `ObserveRequest` and the `adapter` attribute of the log line.
- The adapters are listed by name in `pkg/dustpan/apidesc.go:30`
  (func adapters.allHandlers), and `routeSources` (:101) gives the
  registered patterns of each mounted adapter.
- On the head of #2863 (`f4/1497-backups`), the backup and restore routes are
  under the v1 prefix (`internal/api/server/routes.go:179-199`) and the
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
  handler runs (`http.ServeMux.Handler`), so it uses the same map.
- A request that matches no pattern gets `server`.
- Out of scope: new label values; the route label.

## Check

`go test ./pkg/dustpan/ -run 'TestAdapterLabel' -count=1` passes, and
`make check` reports zero findings (Confirmed by: CI).

## ADR check

**ADR 32: Observing the server** (not owner-tagged; changed by ADR 34, 60
and 210 in other decisions). Quote, decision 5: "The route label is the
registered pattern, never the path. The adapter label is `pulumi`,
`opentofu`, `inventory` or `server`." Effect: the label set stays; the label
now follows the registration, as the route label already does. Conflict:
none.

## Open questions

none. Considered: (1) a label value for each adapter (refused here: ADR 32
fixes four values, and a new set is a new decision); (2) keep the prefix
rule and add each moved prefix to it (refused: each later move breaks it
again, which is the bug class); (3) `inventory` also for the context
adapter (refused: the label names the inventory adapter only, and every
other tool-neutral adapter is `server`).
