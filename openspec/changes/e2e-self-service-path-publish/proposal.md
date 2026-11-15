---
version: 1
---

Short form: tech debt (a test with a wrong skip). Adopts open PR #2610
(`e2e-persona-paths`, three persona path tests). Issue: none, PR #2610.

## Problem

**Persona: platform engineer (Priya).** Priya publishes the blueprint `pg` at
version 1 with the HTTP API, as `docs/guides/publish-a-blueprint.md` shows.
The persona path test of this step must run that path on the real server. It
does not run it: PR #2610 skips it.

PR #2610 adds `e2e/path_self_service_test.go`. On the PR head, the step
`publish_pg_v1` (lines 55-60) calls `selfServiceBlocked` with this reason:
"the server serves no Blueprint route in a running installation: neither the
binary nor the e2e harness sets the WithSelfServiceBlueprints option, which
only the SDK and one SDK test use".

## Evidence

The reason is wrong on `main`. The PR branched at 7f3850f8a, before the change
`wire-self-service-blueprints-binary` merged (#3134, e2ae5e768).

- `rg -n WithSelfServiceBlueprints pkg/dustpan/builtin_stores.go` finds
  lines 396 and 412: `sqliteStoreOptions` and `postgresStoreOptions` pass
  the option, so the binary serves the six blueprint routes with each
  built-in store and no flag.
- `rg -n WithSelfServiceBlueprints e2e/harness.go` finds lines 843 and 845
  (`blueprintStoreOptions`): the in-process harness server passes it too.
- `e2e/self_service_blueprints_test.go` already drives the list route and the
  draft route on both stores. It passes with `Check: blueprintAnswer(...)`,
  which fails on `server.route-not-found`.
- `docs/reference/self-service-api.md:38-58` documents the routes the step
  needs: `PUT .../draft` with `If-Match`, `POST .../versions` with
  `{"draftVersion": n}`, and `GET .../versions/{version}`.

No route and no wiring is missing for the step. The other five steps of the
file stay skipped for the reasons in the PR, which this change does not
re-check: no form check route, no form submit route, no job from a form, no
Blueprint version in the inventory row, no console form.

## Fix boundary

Work on the PR branch `e2e-persona-paths`, in `e2e/path_self_service_test.go`
only, after a merge of `origin/main` into the branch (a clean merge, no hand
edit).

- Replace the body of `t.Run("publish_pg_v1", ...)` with a real step: with the
  harness token, `PUT /api/dustpan/v1/self-service/blueprints/{namespace}/pg/draft`
  with a valid document and `If-Match: "0"`, then `POST .../pg/versions` with
  the draft version from the answer, then `GET .../pg/versions/1`. The answers
  are 200, 201 and 200, and the version description names `blueprint` pg,
  `number` 1 and the draft version.
- The step uses the document shape of `internal/api/selfservice/handler_test.go`
  (`draftJSON`) with the name `pg`.
- Keep the other five steps and their reasons as they are. Keep the file
  comment true: it says Priya publishes `pg` at version 1.
- The comment of `TestPathSelfService` ("Main builds the Blueprint drafts and
  versions behind a store that no server wires in") is wrong for the same
  reason. Change it to say that the publish step runs and the other steps wait.
- The PR body, section "What it adds", says "All six steps stay skipped" and
  gives the wrong reason. Change both lines.
- No change to a route, a handler, a store or the docs.

## Check

`make e2e` passes with the step `TestPathSelfService/<store>/publish_pg_v1`
run, not skipped, on SQLite and on PostgreSQL. The other five steps report
skipped with their reasons. `golangci-lint run --build-tags e2e ./e2e/...`
reports nothing.

## ADR check

- ADR 12: "A tool is supported, and listed as such, only when the e2e suite
  drives its real CLI across a version matrix and the server passes those
  steps." The blueprint routes are tool-neutral and no CLI calls them, so this
  step drives the HTTP API, as `e2e/self_service_blueprints_test.go` does. Not
  owner-tagged. Changed-by chain: ADR 13, 15, 35, 209, 248, 305 (none changes
  this line).
- ADR 17: "Each feature that OpenDustpan ships has a documented REST API. The
  console, the CLI and every other client use only that API." The step uses
  only the documented API. Owner decision ("The project owner made this
  decision on 2026-10-10"). No conflict: the step proves the decision. Chain:
  ADR 30, 210, 222, 224, 266, 305, 326.
- ADR 214: "A pull request that adds or changes a feature that a person or a
  client can see adds or changes its page in the user documentation." The
  guide and reference page exist and are not changed. Not owner-tagged. Chain:
  ADR 315.
- `rg -n -i "persona path|persona-path" docs/adr docs/rfc` finds no decision
  on persona path tests. `rg -n -i "e2e" docs/adr/INDEX.md` finds ADR 244 only
  (the move check), which does not apply.
- No owner-tagged decision conflicts.

## Open questions

none. Considered: (1) a new wiring change: not needed, the evidence shows the
wiring on main. (2) the other five skips: they stay, the lead scoped them out.
(3) the first `If-Match` value: `insertSelfServiceDraft`
(pkg/store/sqlite/self_service_blueprints.go:89) creates a draft at version 0,
so the first write sends `"0"`; task 1.2 proves it. (4) who merges: the
execution driver finishes PR #2610 and the normal merge flow applies.
