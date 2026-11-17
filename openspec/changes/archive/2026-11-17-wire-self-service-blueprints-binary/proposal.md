---
version: 1
---

Short form: bug. A prerequisite of `blueprints` (its e2e step 4.2 drives the
blueprint routes on the real binary).

## Problem

**Persona: platform engineer.** The guide `docs/guides/publish-a-blueprint.md`
and the reference `docs/reference/self-service-api.md` name six blueprint
routes (for example `GET /api/dustpan/v1/self-service/blueprints/{namespace}`). The binary answers
each one with 404 and the code `server.route-not-found`. A reader follows the
guide and cannot publish a blueprint. A feature is complete only when a user
can reach it.

## Evidence

- `WithSelfServiceBlueprints` (pkg/dustpan/selfservice.go:26) mounts the
  routes. Without it, the server serves none of them (its comment at :21-24).
- The built-in stores wire their ports in `sqliteStoreOptions`
  (pkg/dustpan/builtin_stores.go:386) and `postgresStoreOptions` (:401), with
  the shared list in `sharedStoreOptions` (:357). None of them passes
  `WithSelfServiceBlueprints`.
- Both stores already keep blueprints: `func (s *Store) SelfServiceBlueprints()`
  in pkg/store/sqlite/self_service_blueprints.go:21 and
  pkg/store/postgres/self_service_blueprints.go:22.
- Only the test server of pkg/dustpan/apidesc_server_test.go:67 passes the
  option, so the route tests see routes that the binary does not serve.

## Fix boundary

- `sqliteStoreOptions` and `postgresStoreOptions` in
  pkg/dustpan/builtin_stores.go add
  `WithSelfServiceBlueprints(st.SelfServiceBlueprints())`, as they do for the
  other ports of the store. No new flag and no new config: the binary opens
  the built-in store through `WithSQLite` or `WithPostgreSQL`, so it gets the
  routes with each built-in store, as it gets the job, drift and trigger
  routes.
- An SDK user with a store of its own keeps the option as it is.
- No change to the routes, the handlers, the actions or the public list.

## Check

`go test ./pkg/dustpan -run TestBuiltinStoreServesBlueprints` passes, and the
e2e step of e2e/self_service_blueprints_test.go passes on the real binary.
`rg -n WithSelfServiceBlueprints pkg/dustpan/builtin_stores.go` finds two lines.

## ADR check

- ADR 17 (every feature has a REST API that clients use): the routes exist;
  this change makes the binary serve them. No decision line changes.
- ADR 214 (a user documentation page in the same pull request): the reference
  page gets the line of task 1.4.
- No owner-tagged decision is touched. `rg -n "self-service|blueprint"
  docs/adr` finds no decision on a flag for these routes.

## Open questions

none. Considered: a flag to turn the routes on. The other ports of the
built-in store (jobs, drift, triggers, saved views) have no flag, and the
routes check their actions on the namespace, so a flag would hide a
documented API and add a default that no other port has.
