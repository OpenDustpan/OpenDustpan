# Guard 05: no route reads the store once for each row

Status: ready for implementation. Open questions: none.

Depends on spec 01 task 1 (the route exemplars and `internal/knownlist`).

## Problem

An N+1 read is a request that makes one store read, or one transaction,
for each row of a list. Its cost grows with the data, so a test on a small
data set does not see it, and the fixed budgets of
`internal/servertest/roundtrips_test.go` cover 10 request flows and the
backup only.

Real examples:

1. Issue #2934 (fixed by #2947). `GET /api/server/groups` computed the
   `protected` flag of each group of the page with `access.Protected`,
   which walks the reach of the group (`pkg/access/reach.go`): at least 2
   reads for each group, up to 1 000 groups a page. Before the change the
   list was one query. No test failed.
2. Issue #2125. `pkg/maintenance` `activeUpdates` calls `ActiveUpdate` once
   for each stack, and a restore runs it twice. The growth rule of
   `TestRoundTripsOfBackup` found it, and it is an allowance there today
   (`restoreGrowthAllowance`).
3. Issue #2127. A backup and a restore call `GetStateVersion`,
   `ReadWorkspaceByID` and `GetSecretKey` once for each state version. It is
   an allowance of the same test (`backupGrowthAllowance`).

Examples 2 and 3 show that a growth rule finds the class. It runs for the
backup and the restore only. Example 1 shows that a route has no such rule.

## The check

A growth sweep of every route, in `pkg/dustpan`, on PostgreSQL. It runs in
`go test -race ./...` on the test PostgreSQL that the package tests already
use (`internal/pgtest`, with its template database and its clones), as the
round-trip tests of `internal/servertest` do today.

### The shared growth rule

`growthLabel` and `growthProblems` move from
`internal/servertest/roundtrips_test.go` to a test helper package
`internal/roundtrip`, with the `roundTrip` type and the pgx tracer that
records it. `internal/servertest` and the sweep both use it. The rule stays
as it is: two runs, statement by statement, with no slack
(`growthSlack = 0`) and an allowance for each statement.

### What it enumerates

Every row of `srv.RouteDeclarations()` that has an exemplar of spec 01.
Today there are 168 GET rows (160 with the signed-in scope) and 209
mutating rows.

### The two worlds

- **Small:** the seeded world of spec 01, with 2 rows of each kind that a
  list can hold: stacks, updates, checkpoints and events, workspaces and
  state versions, collections and memberships, principals, groups and
  members, grants, access tokens, jobs, triggers, profiles and revisions,
  sinks, subscriptions and deliveries, audit entries, registry releases,
  runners, context entries, saved views, deleted units, history items.
- **Large:** the same world with 12 rows of each kind.

Both have fewer rows than the smallest default page size of a list route,
so a page holds every row in both worlds. A route whose default page is
smaller sets `pageSize` in its exemplar.

### What it asserts

- **GET rows.** Send the exemplar as an administrator to the small world
  and to the large world, and record the statements of each request with
  the tracer. The worlds are read-only for these requests, so one small
  and one large server serve every GET row. The rule fails a row when a
  statement runs more times in the large run than in the small run, above
  its allowance. The failure names the pattern, the statement and the two
  counts.
- **Mutating rows.** The same, on a fresh copy of each world for each row.
  Each copy is a clone of a template database of the seeded world
  (`pgtest.BuildTemplate` and `pgtest.CloneName`), so the sweep seeds each
  world once.

The sweep leaves out the statements that the round-trip tests already
leave out (the prepares and the reads that follow the clock), with the same
filter.

## Allowed exceptions

`pkg/dustpan/testdata/guards/growth-allowances.txt`. One line for each
allowance: the pattern, a tab, the statement label, a tab, the extra count
for each extra row, a tab, the reason. The reason says why the statement
must run once for each row (for example "the store writes each event with
its own insert"). The test refuses a line with an empty reason, a pattern
that no server serves, a statement that the route does not run, and an
allowance that is larger than the growth that the sweep sees. No inline
allowance in code. The owner reviews each change of the file.

Task 3 moves `backupGrowthAllowance` and `restoreGrowthAllowance` of
`internal/servertest` to a file of the same form,
`internal/servertest/testdata/growth-allowances.txt`, so that every
allowance is in a reviewed file.

## Acceptance criteria

1. The rule test of `internal/roundtrip` keeps the cases of
   `roundtrips_model_test.go` (no growth, one more statement, one more
   transaction, an allowance that covers the growth, an allowance that is
   too large).
2. A planted offender fails the sweep: a small handler with one list route
   that reads one row for each item of a page. It passes when the handler
   reads the page with one statement.
3. A planted offender on a mutating route (a delete that reads each member
   one at a time) fails the sweep.
4. An allowance line for a statement that does not grow fails the
   allowance check.
5. The known lists only shrink, through `internal/knownlist`.
6. `make check` passes. The pull request gives the run time of the sweep on
   the CI database.

## Existing offenders

- Confirmed, outside the routes: the 4 allowances of the backup and
  restore growth rule today (`GetStateVersion`, `ReadWorkspaceByID`,
  `GetSecretKey` for each state version, and `ActiveUpdate` for each stack
  in a restore), from issues #2125 and #2127. They stay allowances until
  their fixes, and task 3 moves them to a reviewed file.
- Routes: no confirmed offender. Issue #2934 is fixed. The sweep gives the
  exact list on its first run.
- A static prototype (a semgrep rule for a port read inside a loop in
  `internal/api` and the `internal/*svc` services) found 4 loops. None is an
  N+1: two are a batch scan and a poll loop of the jobs API, two are in
  the demo user setup and the trust rule exchange. So a static rule gives
  noise and misses reads behind a service call. This spec uses the
  dynamic sweep.

Plan: the first run writes its findings to
`pkg/dustpan/testdata/guards/growth-known.txt` with its limit. The list only
shrinks. Each fix removes its line and lowers the limit. A row with no
exemplar is on the `no-exemplar.txt` list of spec 01.

## Tasks

1. **The shared rule and the GET sweep.** Add `internal/roundtrip` and move
   the rule and the tracer to it. Add the two worlds, the GET sweep, the
   planted offender, and the known list from the first run. Acceptance:
   criteria 1, 2, 4, 5 and 6. Size: about 700 lines. Conflict area:
   `internal/servertest/roundtrips_test.go` (the moved functions only) and
   new files.
2. **The mutating sweep.** Add the template database of each world and the
   sweep of the mutating rows. Acceptance: criterion 3, and the known list
   holds the findings of the mutating rows. Size: about 300 lines.
   Conflict area: `pkg/dustpan` test files (new files).
3. **Move the backup allowances, then fix each known row.** Move the two
   allowance functions to their file. Each fix of a known row reads the
   page in one batch, adds a conformance case for the batch read first
   (CLAUDE.md: a new port method gets its conformance tests before any
   adapter), and removes its line. Acceptance: the line is gone and the
   limit is lower. Size: 100 to 400 lines each. Conflict area: the port
   and the store of the read.

## Constraints

- CLAUDE.md "Backend rules": a batch read is a port method with its
  conformance cases first, in both store adapters, with the query in name
  order in `queries.sql`.
- ADR 40: load and fault testing is a test tool, and the budgets only go
  down. The sweep follows the same rule: an allowance only goes down.
- ADR 322: an index for a batch read goes into the baseline of each
  dialect in place.
- ADR 12 and ADR 13: a batch read of a tool model stays in the package of
  that tool. A cross-tool read uses the inventory projection.
- CLAUDE.md "Tests": no wall-clock timing. The rule compares statement
  counts, which do not follow the speed of the machine.

## Decisions taken in this spec (industry default)

- **Count statements, not time.** A statement count is exact and the same
  on a slow and a fast machine. This is the rule of the round-trip tests
  today.
- **Two worlds of 2 and 12 rows of every kind, not one seeder for each
  kind.** One large world finds a read that grows with any kind, and the
  statement in the failure names the kind. This keeps the request count at
  2 for each row.
- **PostgreSQL only.** The tracer is a pgx tracer, as in
  `internal/servertest`. An N+1 is in the code above the store, so one
  dialect finds it.
- **A dynamic check, not a lint rule.** The static prototype above found
  noise only, and an N+1 behind a service call is not visible to a pattern
  match.

## Open questions

None.
