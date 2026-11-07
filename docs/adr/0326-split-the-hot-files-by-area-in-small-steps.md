# 326. Split the hot files by area, in small steps

Status: accepted · 2026-11-03. Decided by the project owner. Accepts RFC 120 in the form that its architecture review recommended

Changes:

- Extends ADR 17: one route test reads the routes of the built server and holds one public list for the whole server.
- Extends ADR 322 decision 1: the name-order placement rule of the baseline applies to the `queries.sql` file of each dialect too.

## Context

Parallel pull requests collide on a small number of central files. RFC 120
measured the 200 newest merged pull requests on `main` at `db6eccf`:

| File | PRs that changed it | PRs with a branch conflict |
| --- | --- | --- |
| `pkg/store/postgres/queries.sql` | 38 | 8 (12 merges) |
| `pkg/store/sqlite/queries.sql` | 35 | 8 (12 merges) |
| `pkg/dustpan/dustpan.go` | 29 | 4 (5 merges) |
| `e2e/coverage.go` | 28 | 5 (6 merges) |
| `GLOSSARY.md` | 23 | 0 |
| `pkg/identity/identity.go` | 9 | 1 (1 merge) |

A branch conflict is a merge of the branch that Git with the mergiraf driver
could not merge. Two of the 20 train fix commits changed `dustpan.go`. Seven
changed the API description and the clients, almost all for the version
raise that ADR 323 removed.

RFC 120 proposed one file for each area, with one explicit list of the areas
in code, for `queries.sql`, `dustpan.go` and `e2e/coverage.go`, and one route
test for the whole server.

An architecture review of RFC 120 on its pull request ran the measurements
again on `db6eccf`. It found these facts:

- A second count gave the same per-file counts and train fix counts. A new run of
  each merge of the 35 pull requests that changed the SQLite `queries.sql`
  gave the same 12 conflicting merges on 8 pull requests.
- mergiraf has no SQL grammar. The `queries.sql` conflicts are line
  conflicts.
- In 11 of the 12 conflicting merges, both sides added the queries of a
  different area at the end of the file. In 1 merge, both sides added the
  same queries, and no split helps. A placement rule removes the 11
  conflicts with no move, no change to an ADR and no conflict for the open
  pull requests. ADR 322 decision 1 already has this rule for the baseline.
- Two numbers of RFC 120 are wrong. With the rule "one query file for each
  store Go file", the queries go to 75 SQLite files and 77 PostgreSQL files,
  not about 50. 42 SQLite queries and 41 PostgreSQL queries have callers in
  more than one Go file. `CoverageAllowlist` joins 48 allowlist functions,
  not 92.
- The areas of a `queries.sql` split must be the areas of ADR 274, not the
  store Go files. The source of an area in ADR 274 decision 7 is a section
  marker. A change of that source is a change of an owner decision, and it
  needs a new ADR.
- The `areas()` list of RFC 120 for `dustpan.go` breaks the "no magic" rule.
  The order of the list is the order of construction, and an area can use
  only the areas above it. Go cannot check "above it" with one input type.
  One input type for each position gives about 25 types. One shared struct
  that the loop fills gives a nil field at run time when a line moves. That
  is a hidden order.
- `mountAPI` is security code. It applies the maintenance gate, CORS and the
  runner maintenance wrapper to each prefix. When each area returns its own
  mount kind, an area file with a wrong wrapper makes its routes skip that
  check. Today one function shows every wrapper.
- `mountAPI` needs no mount order. The root mux is `http.ServeMux`. It picks
  the most specific pattern, whatever the order of registration.
- A test in `pkg/dustpan` cannot read `e2e.ServedRoutes`. That list is in
  package `e2e` behind the `e2e` build tag, and `e2e` imports `pkg/dustpan`.
  The root mux of the server already records each registered pattern and
  each mounted path.
- A new `e2e/coverage_<area>.go` file without `//go:build e2e` compiles into
  the untagged package.

## Options considered

| Option | Why it was refused |
| --- | --- |
| Split `queries.sql` by area now | The placement rule removes 11 of the 12 measured conflicts at no cost. The split waits for data. |
| One query file for each store Go file | It gives about 76 files and does not match the areas of ADR 274. |
| An `area` type and an ordered `areas()` list in `dustpan.go` | The order has a meaning that the compiler cannot check. Each area returns its own mounts, so the security wrappers of `mountAPI` spread over about 25 files. |
| No change to `dustpan.go` | 4 of 200 pull requests had a branch conflict, and 2 train fix commits came from it. The smaller split gets most of the gain. |
| A route test that reads `e2e.ServedRoutes` | The `pkg/dustpan` tests cannot import package `e2e`. |
| Split `GLOSSARY.md` | It had no branch conflict. One file shows that a new term is not a second word for a term of another area. |
| Split the API description and the clients | They are generated. A conflict is solved with a new run of the generator. |
| Split the action constants of `identity.go` | 9 of 200 pull requests and no train fix commit. One block shows a second name for one right. |
| Pull requests do not commit the generated clients | It changes the definition of done, and a reviewer no longer sees the client change. |

## Decision

1. **A new query block goes in its place in the name order of
   `queries.sql`, never at the end (owner).** This applies now, to the
   `queries.sql` file of each dialect. A new query goes next to the queries
   of its area, in the name order of those queries. The file is not in name
   order today. No pull request sorts the whole file.
2. **`e2e/coverage.go` splits into one file for each area (owner).** Each
   `e2e/coverage_<area>.go` holds the route builder, the stub stores and the
   allowlist function of its area, and starts with `//go:build e2e`.
   `ServedRoutes` and `CoverageAllowlist` stay in `e2e/coverage.go`, one
   explicit list of calls each.
3. **One route test for the whole server (owner).** One test builds the
   server and reads its routes from the root mux: the registered patterns
   and the mounted paths. It does not read `e2e.ServedRoutes`. It fails when
   two adapters serve the same route, or when a route that is not public
   declares no action. It holds one public list for the whole server. The
   public list of each adapter stays.
4. **`queries.sql` splits only when the data shows the need (owner).** One
   week after decision 1 is in `CLAUDE.md`, the conflicts on `queries.sql`
   are counted again. The split goes ahead only when that count shows that
   it is necessary. Then the query files are the areas of ADR 274, the split
   comes after step 8 of RFC 103, and a new ADR changes ADR 274 decision 7.
5. **`pkg/dustpan/dustpan.go` splits in the smaller form (owner).** The
   options, the settings struct, the defaults, the checks and the
   constructor of each area move to their own file. The assembly stays plain
   sequential calls with explicit arguments in one function. `mountAPI`
   stays as it is. There is no `area` type and no `areas()` list. Each pull
   request moves one area. The first move gets a review before the next.
6. **These files do not split (owner):** `GLOSSARY.md`, the API description,
   the generated clients and their golden files, and the action constants of
   `pkg/identity/identity.go`.
7. **Each split pull request gets an architecture review before it merges
   (owner).**

## Consequences

- `CLAUDE.md` holds the placement rule of decision 1 in its SQL rule. No
  test enforces the rule. The file is in the order in which areas were
  added, not in name order, and a check of new blocks only must compare the
  file with the merge base of the pull request. The reviewer and the merge
  session check the place of each new block.
- The moves of decisions 2, 3 and 5 are separate pull requests. Each one is
  a pure move, and its description shows the moved lines, for example with
  `git diff --color-moved=dimmed-zebra`.
- Open pull requests that change a moved file get one conflict each. Each
  move lands just after a train.
- The error-code catalog stays the model of an explicit list: one file for
  each area and one list that names each area function.
- ADR 274 decision 7 does not change. A later ADR changes it only if
  decision 4 starts the split.
