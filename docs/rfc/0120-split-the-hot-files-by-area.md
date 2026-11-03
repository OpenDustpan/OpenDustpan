---
rfc: 120
title: Split the hot files by area, with one explicit list of the areas
status: accepted
adr: 326
created: 2026-11-03
---

# RFC 120. Split the hot files by area, with one explicit list of the areas

Status: accepted (ADR 326). The project owner accepted it on 3 November 2026
in the form that its architecture review recommended. That form differs from
this text: see "Outcome".

## Problem

Parallel work on the server collides on a small number of central files. The
carve-out study of 2026-11-02 (`docs/reviews/swarm-carve-out-2026-11-02.md` on
branch `pm/swarm-carve-out`) classified 558 open work items. 309 of them are
core items, because they must change a shared-core file. The unheld periphery
makes 53 independent conflict groups. Most core items are core only because
they add a query, a store, a route, a coverage entry or a term to a central
file. Two items that do this in different domains then change the same file.

The owner said about a split of these central lists: "that will probably be
approved if there are no drawbacks. Get an architectural review on the change
before it ships." This RFC gives the proposal and the drawbacks of each part.

### Hard rule for every proposal

Each split keeps **one explicit, ordered list in code that names every
area**. There is no `init()` self-registration, no blank import, no
reflection and no order that comes from file names. A reader sees every area
and the order in one place ("No magic" in `CLAUDE.md`). A test or the
compiler fails when an area is missing from the list.

The model is the error-code catalog on `main`: one file `catalog_<area>.go`
for each area, and one function `areas()` in `catalog.go` that names each
area function, one on each line. A new area adds one file and one line. Two
pull requests that add two areas change two different lines, so Git and
mergiraf merge them.

### Method of the measurements

All numbers come from `main` at `b72ef46` (2026-11-03).

- **Last 200 merged PRs.** The 200 newest commits on `main` whose subject
  ends with `(#N)`. Each one is a squash merge of one pull request. They span
  2026-10-22 to 2026-11-03. The count is the number of these commits that
  changed the file.
- **Train fix commits.** The 20 commits whose subject starts with
  `train N:` (trains 27 to 42). These are the repairs that the merge session
  made after it picked pull requests onto `main`. The "fold the net change"
  commits of trains 20 to 23 and the batch commits of trains 1 to 3 are
  landings, not repairs, so they do not count.
- **Branch conflicts.** For each of the 200 pull requests, the script fetched
  `refs/pull/N/head` and ran `git merge-tree --write-tree -X no-renames` again
  on each merge commit of the branch (441 merges, almost all of them merges of
  `main` into the branch). The mergiraf merge driver of the checkout was on,
  as it is for the sessions that do the work. A file that the merge cannot
  merge counts as one conflict. So each count is a conflict that mergiraf too
  could not resolve, and that the author had to resolve. The authors may have
  merged with other settings, so the counts are an estimate.
- **Open PRs.** The 20 open pull requests on 2026-11-03, with the files that
  each changes from its merge base.

| File | Last 200 PRs | Branch conflicts (PRs) | Train fix commits | Open PRs that change it |
| --- | --- | --- | --- | --- |
| `pkg/store/postgres/queries.sql` | 38 | 8 (12 merges) | 0 | 4 (#2080, #2093, #2095, #2108) |
| `pkg/store/sqlite/queries.sql` | 35 | 8 (12 merges) | 0 | 4 (the same) |
| `pkg/store/postgres/internal/db/queries.sql.go` (generated) | 38 | 3 (3 merges) | 0 | - |
| `pkg/store/postgres/internal/db/models.go` (generated) | 23 | 0 | 0 | - |
| `pkg/dustpan/dustpan.go` | 29 | 4 (5 merges) | 2 | 3 (#2085, #2095, #2108) |
| `e2e/coverage.go` | 28 | 5 (6 merges) | 0 | 3 (#2095, #2108, #2320) |
| `internal/api/apidesc/openapi.json` (generated) | 31 | 12 (16 merges) | 7 | 2 (#2080, #2108) |
| `clients/typescript/src/client.ts` (generated) | 27 | 10 (13 merges) | 7 | - |
| `GLOSSARY.md` | 23 | 0 | 0 | 2 (#1892, #2108) |
| `pkg/identity/identity.go` (action constants) | 9 | 1 (1 merge) | 0 | 2 (#1892, #2108) |
| `internal/api/server/routes_test.go` | 7 | 0 | 0 | 2 (#2095, #2108) |

The 7 train fix commits on the API description and the clients are almost all
"raise the API description to 0.N.0". ADR 323 decision 2 removed that bump, so
these 7 do not come back. The 2 train fix commits on `dustpan.go` are both
in train 39: the pick of #2263 put a field next to lines that `main` had
changed, gofmt aligned the struct literal again, and the result passed the
`funlen` limit.

Of the 441 merge commits on the 200 branches, 77 had at least one file that a
merge with mergiraf cannot merge. They were on 46 of the 200 pull requests.
Most conflicting files were generated: the TypeScript client (13 to 35 merges
for each file), `openapi.pin` (28), `openapi.json` (16), `document.go` (15)
and the Go golden files (10 to 11). Many of these were the version bump that
ADR 323 removed. The hand-written files with the most conflicts were the two
`queries.sql` files (12 merges on 8 PRs each), `e2e/coverage.go` (6 merges on
5 PRs) and `dustpan.go` (5 merges on 4 PRs). `GLOSSARY.md`, `models.go` and
`internal/api/server/routes_test.go` had no conflict.

## 1. `queries.sql` of each dialect, split by area

### Current state

- `pkg/store/sqlite/queries.sql` holds 686 named queries in 5074 lines.
  `pkg/store/postgres/queries.sql` holds 719 named queries in 5506 lines.
- `sqlc.yaml` names one query file for each dialect. sqlc writes one file
  `internal/db/queries.sql.go` for each dialect (576 KB for SQLite).
- 38 of the last 200 PRs changed the PostgreSQL file, and 35 changed the
  SQLite file. Each of them also changed the generated `queries.sql.go`. Of
  the hunks in the SQLite file, 16 of 95 were in the last 5 percent of the
  file. The rest were spread over the file, next to the queries of the same
  table.
- The Go code of each store is already one file for each area
  (`pkg/store/sqlite/access.go`, `audit.go`, `backup_*.go` and so on). Only the
  SQL is central.

### Proposed layout

```
pkg/store/sqlite/queries/access.sql
pkg/store/sqlite/queries/audit.sql
...
pkg/store/postgres/queries/access.sql
pkg/store/postgres/queries/audit.sql
...
```

- One query file for each area. The file name is the name of the Go file of
  the store that calls the queries (`access.sql` next to `access.go`). The two
  dialects use the same file names.
- `sqlc.yaml` lists each query file of each dialect explicitly, one file on
  each line, in name order. This is the one ordered list of the areas. sqlc
  v1.31.1 accepts a list of paths for `queries`.
- sqlc then writes one generated file for each query file
  (`internal/db/access.sql.go`). `models.go` and `db.go` stay one file each.

The author checked sqlc v1.31.1 on a two-file sample:

- A list of two query files gives two generated files, `a.sql.go` and
  `b.sql.go`.
- A query name that two files declare fails `sqlc generate` with
  `duplicate query name: GetT` and exit status 1. `sqlc diff` in `make check`
  then fails the pull request.

### Checks that keep the split honest

1. A Go test in each store package reads `sqlc.yaml` and fails when a file in
   `queries/` is not in the list, when the list names a file that does not
   exist, or when the list is not in name order. sqlc reads only the listed
   files, so without this test a new file that is not listed would be
   silently left out.
2. The same test fails when the two dialects do not have the same list of
   file names. A reader then finds the twin of a query in the file of the
   same name.
3. sqlc refuses a duplicate query name across files (checked above).
4. `sqlc diff` stays in `make check`.

### ADRs that this touches

- ADR 3 and ADR 274: each dialect keeps its own schema and queries. The split
  does not share SQL between the dialects. No change.
- ADR 274 decision 7 says the generator writes one interface for each area
  "from a section marker in `queries.sql`". With this split, the area is the
  query file, and no section marker is necessary. This is a change to the
  wording of decision 7: "from the query file of the area". The intent (an
  area package sees only its own queries) stays. The split makes decision 7
  simpler to build, because sqlc already gives one generated file for each
  area.
- ADR 274 decision 2 orders its steps after the move of the store adapters
  to `internal/store/` (ADR 272, RFC 103 step 8). The split is a mechanical
  move, so it can go before or after that move. If it goes first, the scripted
  move of RFC 103 moves more files. The review should choose the order.
- The SQL rule in `CLAUDE.md` names `pkg/store/<dialect>/queries.sql`. It
  changes to `pkg/store/<dialect>/queries/<area>.sql`.

### Migration cost

- One pull request: about 1405 named queries move into about 50 files for
  each dialect, with a script that assigns each query to the Go file that
  calls it. The generated code changes file names, but the generated Go names
  do not change, so no caller changes.
- The 4 open PRs that change `queries.sql` (#2080, #2093, #2095, #2108) each
  get one conflict. The author of each one moves the new queries to the area
  file and runs `make generate`. Do the move just after a train, while the PM
  holds new SQL pull requests for one train.

### Drawbacks

- One file no longer shows every query of a dialect. `rg` over `queries/`
  does the same search.
- A query that two areas use must live in one of them. The script puts it in
  the file of its first caller. A shared-core file (`core.sql`) may be
  necessary for the fence and the namespace queries. That file stays a hot
  file.
- `models.go` stays one generated file. Each schema change still changes it.
  It was in 23 of the last 200 PRs, but it had no branch conflict, because a
  regeneration on the merged schema gives it again.
- `sqlc.yaml` becomes a central list. Each new area adds one line to it. A
  new query in an existing area does not change it. New areas are rare
  compared to new queries.
- The baseline migration of each dialect (ADR 322) stays one file. This RFC
  does not split it.

## 2. The wiring in `pkg/dustpan/dustpan.go`

### Current state

- `dustpan.go` has 4440 lines and 173 functions. It holds the `settings`
  struct, every `With...` option, `defaultSettings`, the checks of the
  settings, the construction of each service and adapter, `assemble`,
  `mountAPI` and the request middleware.
- 29 of the last 200 PRs changed it. The hunks of those PRs were in:
  the import block (12 PRs), `type settings struct` (12), `defaultSettings`
  (12), `mountAPI` (8), `type Server struct` (7), `assemble` (7),
  `newNeutralHandlers` and its struct (5), and 10 other functions with 1 to 3
  PRs each.
- It caused 2 of the 20 train fix commits, both in train 39: the
  pick of #2263 put a field next to lines that `main` had changed, gofmt then
  aligned the whole struct literal again, and the formatted literal passed the
  `funlen` limit of `assemble`.
- Other files of `pkg/dustpan` already hold some areas (`components.go`,
  `drift.go`, `auditchain.go`, `auditsinks.go`).

### Proposed layout

- One file for each area: `pkg/dustpan/area_<name>.go`, for example
  `area_jobs.go`, `area_drift.go`, `area_registry.go`. The file holds the
  `With...` options of that area, a settings struct of the area
  (`jobSettings`), its defaults, its checks, and one function that builds the
  services and handlers of the area from the shared dependencies.
- `dustpan.go` keeps `NewServer`, the shared dependencies (namespace store,
  blob store, audit, fence, logger, authorizer) and one ordered list:

  ```go
  // areas returns every area of the server, in the order that NewServer
  // builds them. An area can use only the shared dependencies and the
  // areas above it in this list.
  func areas() []area {
  	return []area{
  		namespacesArea,
  		stacksArea,
  		workspacesArea,
  		jobsArea,
  		driftArea,
  		...
  	}
  }
  ```

- `settings` keeps one field for each area (`jobs jobSettings`), not one field
  for each store. A new store in an existing area changes only the area file.
- `mountAPI` reads the mount list from the same ordered list: each area
  returns its handlers and their mount kind (`/api/dustpan/v1/` with the
  maintenance gate, the installation API, or its own prefix). The list in
  `mountAPI` that names `a.inventory, a.context, a.profiles, ...` goes away.
- The `With...` options stay in package `dustpan` with the same names and
  signatures. The public SDK surface (ADR 35, ADR 272) does not change. Only
  the file that holds each option changes.

### Checks that keep the split honest

1. A missing area function does not compile.
2. A test builds the server with every area and checks that each route group
   of the route tables has an area in `areas()`. An area that is left out of
   the list then fails, and does not silently not start.
3. A test checks that each area uses only the shared dependencies and the
   areas above it in the list. The area function takes a typed input that
   holds only those values, so the compiler does most of this check.
4. The existing tests of `pkg/dustpan` (start-up, components, the API
   description gate) run without change.

### ADRs that this touches

- ADR 35 and ADR 272: `NewServer` and the options are SDK. The split moves
  them between files of the same package. No exported name changes. No
  change to either ADR.
- ADR 249: the components `api` and `worker` select what a process runs. Each
  area must say what it starts in each component. This moves the component
  rule into each area file. The rule itself does not change.
- ADR 211 (optional modules): an area is a natural unit for an optional
  module, but this RFC does not make areas optional. A later RFC can.

### Migration cost

- Large. About 3000 of the 4440 lines move, in about 25 area files. The move
  touches the internal tests of `pkg/dustpan` that call unexported
  constructors. Do it in steps of one or a few areas for each pull request,
  each one after a train. Each step is a pure move with the ordered list
  growing by the moved areas.
- 3 open PRs change `dustpan.go` (#2085, #2095, #2108). Each step that moves
  the area that a PR changes gives that PR one conflict.

### Drawbacks

- The order of construction is no longer visible in one function body. The
  `areas()` list shows it, but the code of each area is in its own file.
- An area interface adds a layer. The shared dependencies must be a typed
  struct, and a dependency between two areas (for example jobs on drift) must
  be explicit. Today a constructor reaches any local variable. This is more
  code, but it makes the hidden dependencies visible.
- The import block, `defaultSettings` and the `settings` struct are hot today
  because each store has its own line there. After the split, each area has
  one line. The `areas()` list and the `settings` struct still change once for
  each new area.
- The steps take several weeks of train windows. During the steps,
  `dustpan.go` holds some areas and the area files hold others.

## 3. Routes and authorization actions of each adapter

### Current state

The route tables are already split. Each adapter has its own
`internal/api/<adapter>/routes.go` and its own `routes_test.go`. In the last
200 PRs:

| File | PRs |
| --- | --- |
| `internal/api/jobs/routes_test.go` | 9 |
| `internal/api/server/routes_test.go` | 7 |
| `internal/api/pulumi/routes_test.go` | 6 |
| `internal/api/server/routes.go` | 5 |
| `internal/api/inventory/routes_test.go` and `routes.go` | 5 and 4 |
| every other adapter route file | 1 to 4 each |

Five adapters (server, Pulumi, TFE, audit, inventory) each keep their own
`publicPatterns()` list in their own `routes_test.go`. The action constants
are 124 lines in one `const` block of `pkg/identity/identity.go` (9 of the
last 200 PRs). No train fix commit touched a route file or `identity.go`.

### Proposal

- Do not split the route tables further. The gain is already in `main`.
- Keep the action constants in `pkg/identity/identity.go`. 9 PRs in 200 and no
  train fix commit do not pay for a move. Actions are a shared vocabulary
  (ADR 17, ADR 210 decision 3), and one block makes a second name for one
  right easy to see.
- Add one cross-adapter test in `pkg/dustpan` that reads the route table of
  each area from the ordered list of section 2 and checks the union: no route
  in two adapters, every non-public route declares an action, and every public
  route is on one explicit server-wide public list. This is a safety gain, not
  a conflict gain. ADR 17 asks for "a test [that] lists every route that the
  server registers", and the routes that need no sign-in "on an explicit list
  in the same test". Today five per-adapter lists do this together. The
  per-adapter lists stay as a defense-in-depth layer.

### Checks that keep it honest

The new test fails when an adapter is missing from the ordered list, because
`ServedRoutes` (section 4) and the server see a route that the test does not.

### ADRs that this touches

ADR 17 (the route test and its public list) and ADR 210. The proposal moves
closer to the text of ADR 17. No ADR change.

### Migration cost

Small. One test file. It depends on section 2 for the ordered list. Before
section 2, it can use the adapter list of `newAdapters`.

### Drawbacks

- The public list is in two places: each adapter, and the server-wide list. A
  new public route changes two files. Public routes are rare (they need a
  review as security code), so the cost is low.
- No conflict gain.

## 4. `e2e/coverage.go`, the API description and the generated clients

### 4a. `e2e/coverage.go`

#### Current state

- 3569 lines. It holds `ServedRoutes`, which calls one route builder for each
  adapter (17 builders) and concatenates them, the stub stores that each
  builder needs, and `CoverageAllowlist`, which concatenates 92 allowlist
  functions.
- 28 of the last 200 PRs changed it. The hunks were in the import block (10
  PRs), `CoverageAllowlist` (9), unnamed stub methods (9), `ServedRoutes` (6)
  and the route builders and allowlists of jobs, server, inventory, drift,
  triggers and trust rules (1 to 4 each).
- `e2e/coverage_signin.go` already holds the sign-in area in its own file.

#### Proposed layout

- `e2e/coverage_<area>.go` for each area: the route builder, the stub stores
  of that builder and the allowlist function of the area.
- `e2e/coverage.go` keeps `RouteRef`, `ServedRoutes` and `CoverageAllowlist`.
  Both are already explicit ordered lists of calls. They stay the one list.

#### Checks

- A missing builder or allowlist function does not compile.
- Add a test that each adapter of the server route tables appears in
  `ServedRoutes`. `TestServedRoutesIncludeHTTPBackend` already does this for
  one adapter. With this test, a builder that is left out of the list fails,
  and does not silently drop an adapter from the coverage check.

#### ADRs

No ADR covers the file layout of the e2e suite. ADR 12 ("every endpoint lands
with its e2e step first") does not change.

#### Migration cost

Small. One test-only pull request, a pure move. 3 open PRs change the file
(#2095, #2108, #2320).

#### Drawbacks

- The stub stores of two builders can share a type. A shared stub stays in
  `coverage.go`, which keeps a small hot part.
- `ServedRoutes` and `CoverageAllowlist` still change once for each new area.

### 4b. The API description (`internal/api/apidesc/openapi.json`)

#### Current state

The description is generated from the route tables by
`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`.
The server serves the checked-in bytes (`TestOpenAPIRouteServesTheCheckedInBytes`).
31 of the last 200 PRs changed it. 7 of the 20 train fix commits touched it,
all for the version bump that ADR 323 decision 2 removed.

#### Proposal

Do not split it. It is one document that the server serves and that the
client generator reads. Fragments for each area would need a merge step and a
second source of truth. ADR 323 decision 3 already says that the merge session
of each train generates it again, and that a conflict in it is resolved by a
new run of its generator. With the version bump gone, its remaining conflicts
are in generated text only.

### 4c. The generated clients and the golden files

#### Current state

`clients/typescript/src/*` (27 to 36 PRs for each file) and the golden files
of `internal/clientgen` are generated from the description. ADR 323 decision 3
says the train generates them again. `make clients-check` in `make check`
fails a pull request whose committed client is stale.

#### Options

1. **Do nothing (recommended).** The files are derived output. A conflict is
   resolved by a new run of the generator (`CLAUDE.md`, ADR 323). The cost is
   one command for the author.
2. **PRs do not commit the clients.** Only the train commits `clients/` and
   the golden files. This removes the conflicts. But it changes the
   definition of done in `CLAUDE.md` (`make check` passes on each PR), a
   reviewer no longer sees the client change in the PR, and the console
   builds against a client that `main` does not hold until the train runs.
   This needs an ADR change.

#### Recommendation

Option 1. The generated files cost a command, not a decision. The
reviewed source is the route table, and its split is in section 2 and in the
adapters.

## 5. `GLOSSARY.md`

### Current state

- 1798 lines, 483 terms, in 17 sections (shared across tools, federation,
  identity, Pulumi, OpenTofu, secrets, jobs and runners, gates and approvals,
  and others).
- 23 of the last 200 PRs changed it. No train fix commit touched it. The
  hunks were spread over many terms: no term had more than 2 PRs. It had no
  branch conflict on the 441 merges.
- `make docs-lint` reads `GLOSSARY.md` by name. The `domain-modeling` skill
  expects one `GLOSSARY.md` for a single-context repository
  (`docs/agents/domain.md`). `CLAUDE.md` tells each contributor to "use the
  terms in `GLOSSARY.md`".

### Proposal

Keep one file. The rule is "one word per concept". One file is the only
place where a reader and a reviewer see that a new term is not a second word
for a term of another area. Split files make a near-synonym across areas easy
to add and hard to see. The conflicts are few and small, because the edits
are spread over the file.

To reduce the remaining conflicts at no cost: inside each section, a new term
goes in its alphabetical place, not at the end of the section. Two new terms
then land on different lines.

### ADRs that this touches

ADR 14 and `docs/agents/domain.md` (single context). No change.

### Drawbacks

The file stays a hot file for each PR that adds a term. That is the intent:
a new term must be seen next to the others.

## Ranking by gain for each unit of cost

| Rank | Section | Gain | Cost | Main drawback |
| --- | --- | --- | --- | --- |
| 1 | 4a `e2e/coverage.go` by area | 28 of 200 PRs, 5 PRs with a branch conflict; the hunks are in stubs and imports that move out | Small: one test-only move, 3 open PRs | `ServedRoutes` and `CoverageAllowlist` still change for each new area |
| 2 | 1 `queries.sql` by area | The hottest hand-written files: 38 and 35 of 200 PRs, 8 PRs with a branch conflict in each, and the 576 KB generated file splits the same way | Medium: one scripted move of about 1405 queries, 4 open PRs, a wording change to ADR 274 decision 7 | `models.go` stays one hot generated file, and a shared `core.sql` may stay hot |
| 3 | 2 `dustpan.go` by area | 29 of 200 PRs, 4 PRs with a branch conflict, and 2 of 20 train fix commits | Large: about 3000 lines in several steps, 3 open PRs | The construction order is in a list, not in one function body; an area interface adds a layer |
| 4 | 3 one cross-adapter route test | A safety gain toward ADR 17; no conflict gain | Small: one test | The public list is in two places |
| - | 5 `GLOSSARY.md` | Do not split: no real gain, and it weakens "one word per concept" | None | - |
| - | 4b, 4c the description and the clients | Do not split: generated output, regenerated in the train (ADR 323) | None | - |
| - | 3 split `identity.go` actions | Do not split: 9 of 200 PRs, no train fix commit | - | - |

## Open questions

1. Section 1: does the split go before or after the move of the stores to
   `internal/store/` (RFC 103 step 8, ADR 274 decision 2)? Before is less
   work for this split. After is less work for the move.
2. Section 1: which queries are shared core (`core.sql`), and does ADR 274
   decision 7 let an area see `core.sql` as it lets an area see the shared
   core queries today?
3. Section 2: is the area list one list for construction and for mounts, or
   two lists? One list is simpler to read. Two lists let the mount order
   differ from the construction order, which `mountAPI` needs today for the
   more specific prefixes.
4. Section 2: does each step of the move need an architecture review, or only
   the first step, which sets the `area` type?

## Checklist

- [x] Tolerates asynchronous replication between installations: each
  namespace writes only its own half, under its own fence, and no write waits
  for another installation (ADR 248 decisions 3, 6 and 9). This RFC changes
  only the file layout of code and SQL. No query, write path or fence
  changes.

## Outcome

Accepted on 3 November 2026. [ADR 326](../adr/0326-split-the-hot-files-by-area-in-small-steps.md)
records the decisions. They follow the architecture review of this RFC, not
every proposal above:

- Section 1: a placement rule first. A new query block goes in its place in
  the name order of `queries.sql`, never at the end. The split by area comes
  only if the data after one week shows the need, with the areas of ADR 274,
  after RFC 103 step 8, and with a new ADR that changes ADR 274 decision 7.
- Section 2: the smaller form. Each area's options, defaults, checks and
  constructor move to their own file. The assembly stays plain sequential
  calls in one function, and `mountAPI` stays. The `area` type and the
  `areas()` list are dropped: their order has a meaning that the compiler
  cannot check, and they spread the security wrappers of `mountAPI`.
- Section 3: the route test reads the root mux of the built server, not
  `e2e.ServedRoutes`.
- Section 4a: accepted, with `//go:build e2e` on each new file.
- Sections 4b, 4c and 5, and the action constants: not split.
