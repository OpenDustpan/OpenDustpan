# Guard 01: no existence oracle in a refusal

Status: ready for implementation. Open questions: none.

This spec also defines two parts that specs 02, 03 and 05 use: the route
exemplars and the known-list reader. They land in task 1 of this spec.

## Problem

A caller that may not read an object must get the same answer for "the
object exists" and "the object does not exist". The class is a route whose
resolver reads stored state before the route check, and whose answer then
depends on that state: the status, the error code, the text, the arguments,
a header, or the action that the denial names.

Real examples:

1. Issue #2923. `DELETE /api/server/tokens/{id}` and
   `POST /api/server/tokens/{id}/rotate` resolve the object from the stored
   token (`internal/api/server/tokens.go:374-392`, `resolveTokenOwner`) and
   pick the action from the resolved kind (`tokens.go:358-365`,
   `revokeActions`). The 403 text that `internal/api/problem/problem.go:345-365`
   (`denied`) writes names the action and the kind. A signed-in caller with
   no grant gets `principal:manage-tokens on this principal` for a service
   token and `server:manage-tokens on this server` for an unknown ID. Open
   PR #2949 fixes these two routes only.
2. Issue #2954. The same review names eight more resolvers that read stored
   state before the check: `resolveJob`, `resolveTrigger`,
   `resolveStoredTrigger`, `resolveWorkspaceID`, `resolveStateVersionID`,
   `resolveStateOutputID`, `resolveUnitByID` and `resolveDeletedUnit`.
   Nothing checks them as a group today. Each one relies on a comment that
   says "answer alike".
3. Issue #1844 (closed). The approver check told a namespace role whether an
   installation group exists and how many members it has.

The existing route checks (`pkg/dustpan/route_actions_test.go`) prove that
each route declares actions and that no route answers without a credential.
No check compares two answers of one route.

## The check

A two-world probe of every route, in `pkg/dustpan`, next to
`TestNoRouteAnswersWithoutACredential`. It runs in `go test -race ./...`, so
`make check` runs it.

### What it enumerates

Every row of `srv.RouteDeclarations()` of the "every module" server whose
pattern has at least one wildcard and whose scope is not public. Today that
is 300 rows: 272 with the signed-in scope, 24 with the credential scope and
4 with the scope `none` (the OpenTofu HTTP backend). The probe reads the
rows from the server, so a new route is in the probe the moment it is
registered.

### The route exemplars (shared part)

A route exemplar is one valid request of one route: the value of each
wildcard, the query, the body, the caller kind and the status that an
administrator gets. Specs 02, 03 and 05 use the same exemplars.

- `pkg/dustpan/exemplar_test.go` holds the type, the seeded world and the
  wildcard defaults. The seeded world is one SQLite server with the "every
  module" options. An administrator makes one object of each kind through
  the API: a namespace, a project and a stack, a workspace with a state
  version and an output, a collection, a principal and a group, a service
  token, a grant, a job, a trigger, a profile, a sink, a subscription and a
  delivery, a saved view, a registry release, a runner pool and a runner,
  a trust rule, a self-service blueprint.
- The wildcard defaults map a wildcard name to the stored value, for the
  names that mean one thing in every route: `namespace`, `project`,
  `stack`, `collection`, `unit_id`, `unitID`, `tool`, `principal`,
  `update`, `pool`, `state_version_id`, `workspace`, `profile`,
  `blueprint`, `organization`, `sink_id`, `subscription_id`, `trigger_id`,
  `installation_id`, `view_id`, `job_id`, `runner`, `workspace_id`,
  `output_id`, `delivery_id`, `rule_id`, `system`, `host`, `account`.
- A wildcard name that means different things in different routes (`id`,
  `name`, `version`, `kind`, `type`, `operation`, `step`, `part`, `at`,
  `token`, `sha256`, `sequence`, `seq`, `revision`, `phase`, `fingerprint`,
  `urn`, `address`, `os`, `arch`, `rule`) has no default. A route with such
  a wildcard needs an exemplar row of its own in
  `pkg/dustpan/exemplar_<adapter>_test.go`, one file for each adapter, so
  that two changes to two adapters touch different files.
- Each exemplar also gives an unknown value for each wildcard: a value of
  the same syntax that the seeded world does not hold.

Coverage today: 179 of the 300 rows have only wildcards with a default, so
the defaults cover them with no row of their own. 121 rows need a row.

### What it asserts

For each enumerated row, the probe sends requests as a caller that holds no
grant: a signed-in person for the signed-in scope, and a credential of the
right kind with no right on the object for the credential scope (a runner of
another pool, a token of another namespace). It sends:

- request A: every wildcard has its stored value;
- request B(w), for each wildcard w: w has its unknown value, the others
  keep their stored value.

It fails the row when request A and any request B(w) differ in one of these:
the status, the error code, the arguments of the code, the message, the
`WWW-Authenticate` header, the `Retry-After` header. It ignores the request
ID and the `Date` header. The failure names the pattern, the wildcard and
the two answers.

It also fails a row when the resolver reads the request body (the probe
gives a body that counts reads) and the exemplar has no body. So a resolver
that resolves its object from the body, such as
`resolveServiceTokenCreate`, needs a body with a stored and an unknown
value.

It fails when a row has no exemplar and is not on the known list, when a
wildcard name has no default and the row has no exemplar, and when an
exemplar names a pattern that no server serves.

### The known-list reader (shared part)

`internal/errcode/gate_test.go` already has the form of a list that only
shrinks: a header, a line `limit N`, then one sorted entry on each line. The
test fails for a new entry, for an entry that the check no longer finds,
and for a limit that is not the line count. Task 1 moves `readKnown`,
`checkKnown` and `checkLimit` into a test helper package
`internal/knownlist`, and the errcode gate calls it. Every guard of this
spec set uses it.

## Allowed exceptions

`pkg/dustpan/testdata/guards/oracle-exceptions.txt`. One line for each
exception: the pattern, a tab, the wildcard, a tab, the reason. The reason
is a full sentence and says why a caller with no grant may learn the
existence. The test refuses a line with an empty reason, a pattern that no
server serves, and a line that the probe does not need. There is no inline
marker. A change of the file is a line of the diff that the owner reviews
(`.github/CODEOWNERS`).

Expected entries: none on day one. A route that gives the same value to
every caller (for example a public discovery document) is public and not in
the probe.

## Acceptance criteria

1. The probe runs in `go test ./pkg/dustpan` and fails for each row whose
   two answers differ.
2. Mutation rows, in a table test of the comparison function with made-up
   answers (no server), each fail the check: a status that differs (403 and
   404); a code that differs; the same code with an argument that differs
   (`objectKind` principal and server); a message that differs; a
   `WWW-Authenticate` header that differs. A row where only the request ID
   differs passes.
3. A planted offender fails the probe. The probe is a function of a
   handler, the route facts and the exemplars, as `probeUnauthenticated` is
   today. The test runs it on a small handler with one route whose resolver
   answers 404 for an unknown ID and lets the route check refuse a stored
   one, and asserts that the probe names this pattern. It also runs it on a
   copy of the real facts with that one route added (the `mutate` and
   `addRoute` helpers of `route_actions_test.go`).
4. The coverage check fails for a planted route with an ambiguous wildcard
   and no exemplar, and for an exemplar of a pattern that no server serves.
5. The known list check fails when an entry is added without a raise of the
   limit, when the limit is higher than the entries, and when a listed row
   now passes.
6. `make check` passes with the known list in place.

## Existing offenders

Found by reading the code (the probe does not exist yet, so its first run
gives the exact list):

- Confirmed: 2 rows. `DELETE /api/server/tokens/{id}` and
  `POST /api/server/tokens/{id}/rotate` (issue #2923). If PR #2949 merges
  first, these rows pass.
- Candidates: 26 rows on the eight resolvers of issue #2954
  (`resolveWorkspaceID` 8, `resolveStateVersionID` 7, `resolveTrigger` 3,
  `resolveJob` 2, `resolveUnitByID` 2, `resolveDeletedUnit` 2,
  `resolveStoredTrigger` 1, `resolveStateOutputID` 1). Their comments say
  that unknown and denied answer alike. The probe proves or refutes each.
- Not yet covered: 121 rows that need an exemplar row of their own.

Plan: the first run writes its findings to
`pkg/dustpan/testdata/guards/oracle-known.txt` with its limit. The 121 rows
without an exemplar go on `pkg/dustpan/testdata/guards/no-exemplar.txt`
with its limit. Both files only shrink. A confirmed oracle is fixed in its
own pull request, which removes its line and lowers the limit.

## Tasks

1. **Exemplars, the known-list helper and the probe.** Add
   `internal/knownlist` and move the errcode gate to it. Add the seeded
   world, the wildcard defaults, the probe, the comparison function with its
   mutation table, the planted adapter, and the two known lists from the
   first run. Acceptance: criteria 1 to 6. Size: about 1 200 lines, most of
   it the seeded world and the test. Conflict area: `pkg/dustpan/*_test.go`
   (new files only), `internal/errcode/gate_test.go`.
2. **Exemplar rows, one adapter at a time.** Add the rows of one or two
   adapters for each pull request, and remove them from `no-exemplar.txt`.
   Any oracle that a new row finds goes on `oracle-known.txt` in the same
   pull request. Acceptance: the limit of `no-exemplar.txt` goes down by the
   number of rows added. Size: 100 to 300 lines each. Conflict area: one
   `exemplar_<adapter>_test.go` file and one line block of
   `no-exemplar.txt`.
3. **Fix the confirmed oracles.** One pull request for each resolver
   family. Each resolver gives the object from the request alone, or maps
   "missing" and "not allowed" to one answer, as PR #2949 does. Each fix
   adds the hostile rows at the three layers that ADR 250 asks for (see
   Constraints) and removes its line from `oracle-known.txt`. Acceptance:
   the line is gone and the limit is lower. Size: 50 to 200 lines each.
   Conflict area: the adapter of the resolver.

## Constraints

- ADR 250 and CLAUDE.md "Security code": the probe is the HTTP-handler
  layer. A fix also has a hostile table at the service and the store port
  layer, and a test for each step. A check that looks redundant keeps a
  defense-in-depth comment.
- ADR 182 and ADR 276: the answer keeps a code of the catalog and its
  arguments. A fix may drop the arguments of `access.denied` for a route
  whose object kind comes from stored state, because the code declares no
  argument (`errcode.DeniedArgs`).
- ADR 73: the Pulumi Cloud-compatible and TFE-compatible routes are
  contracts. A fix on such a route must pass `make e2e`. If a real CLI
  needs a 404 for a missing object that its caller may read, the probe still
  holds, because its caller holds no grant.
- ADR 12 and ADR 13: the probe and the exemplars stay in `pkg/dustpan`
  tests and add nothing to a shared domain package.
- ADR 66: no issue or ADR number in the code, the comments or the test
  names. The lists name patterns, not issues.
- CLAUDE.md "Tests": standard `testing`, black-box `_test` package,
  `t.Parallel()`, `t.Context()`, a `leak_test.go` in each new test package
  (`internal/knownlist`).

## Decisions taken in this spec (industry default)

- **The caller of the probe holds no grant.** A caller who may read the
  namespace can list its objects, so existence is not secret to that
  caller. The rule protects the caller with no grant. This is the rule of
  the existing comments ("a caller without rights").
- **The probe compares answers, not timing.** A timing oracle needs a
  statistical test, which is flaky in CI. The wall-clock guard of PR #2967
  keeps timing out of tests. Timing stays a review item.
- **One wildcard changes at a time.** This finds the wildcard that leaks
  and keeps the request count linear in the wildcards.

## Open questions

None.
