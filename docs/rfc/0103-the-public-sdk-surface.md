---
rfc: 103
title: The public SDK surface
status: decided
created: 2026-10-22
adr: 272
---

# RFC 103. The public SDK surface

Status: decided. [ADR 272](../adr/0272-the-public-sdk-surface.md) records the
decisions. The owner accepted this RFC on 22 October 2026, with answer B to
questions 1 and 2 and answer A to question 3. This RFC answers finding 1 of
the architecture review in issue #1400, and the zombie packages of finding 9.
It holds the owner decisions of 22 October 2026 as decided and does not reopen
them. It gives a rule that a test can apply to a package, the measured state
of `pkg/`, the target layout, the move plan, the CI check, and the three
questions that the owner answered.

## Problem

ADR 35 puts the SDK in `pkg/` and says that the SDK is the entry point, about
eight domain packages, the conformance suites and the built-in adapters. Since
then, every new package went into `pkg/`. Go semantic versioning makes each
exported name in an importable package a promise at 1.0. Today no one can
tell which exported names are a promise.

Measured on `main` at bbdde60 (2026-10-22):

- `go list ./pkg/...` lists **148 packages**. 13 of them are under a
  `pkg/store/.../internal/` path, so the Go toolchain already stops other
  modules from importing them. **135 packages are importable.** (The review
  counted 147 at 7b4ac09. One package came after it.)
- The 148 packages hold **6,800 exported top-level names** (types, functions,
  variables, constants). This count leaves out methods and fields. The
  review's 7,491 counts in a different way. Both numbers say the same thing.
- The exported API of `pkg/dustpan`, plus the store ports, reaches **561
  named types in 40 packages** (not counting suites). Those 40 packages hold
  3,159 exported names. The 97 packages that the closure does not reach hold
  3,565 exported names that no port and no option needs.
- **14 exported signatures in 6 packages name a type from `internal/`.** An
  embedder or a third-party store cannot name these types, so it cannot
  implement or call these APIs. The list is in "Measured state".
- **74 files in 31 packages import `internal/ident`** (59 non-test files). The
  review counted 27. `git grep -l` at 7b4ac09 gives 71, so the difference
  is in the method, not in the code. These imports are implementation only:
  `internal/ident` exports no type, and no exported signature names it. Under
  the rule below they are allowed.
- 8 packages have no production caller: `stepup`, `cost`, `chatops`,
  `incident`, `oncall`, `source`, `launcher`, `dependency`. With their suites
  they are 14 packages.

## What is decided

The owner decided these points on 22 October 2026. This RFC does not reopen
them.

1. Shrink the SDK now, before the first tag.
2. The SDK is `pkg/dustpan` (the entry point and its options), the port
   packages, and the conformance suites. Everything else moves to
   `internal/`.
3. A CI check fails when an exported `pkg/` signature names an `internal/`
   type.
4. SQLite stays a production adapter. More SQL dialects (for example MySQL)
   can come later. So the store ports and their conformance suites are SDK,
   and a third-party store must be able to implement them.
5. The 8 packages without a production caller are features that are not
   finished. The rule below decides for each one, the same as for every other
   package.
6. OpenDustpan has not shipped and has no users. Breaking changes cost
   nothing, and no deprecation path is necessary.

## The rule

A package under `pkg/` is SDK when one of these three tests passes. A package
that passes none moves to `internal/`.

- **R1. Entry point.** The package is `pkg/dustpan`.
- **R2. Port closure.** The package declares a type that the port closure
  reaches. The port closure starts at these roots:
  1. every exported name of `pkg/dustpan` (the options, `NewServer`,
     `Server`, and the types they name).
  2. every interface that a store must implement: today, the interfaces that
     `NewServer` asserts on the namespace store, and the interfaces that the
     built-in SQLite or PostgreSQL store implements, when each is declared under
     `pkg/`. When finding 2 of #1400
     replaces the type assertions with a declared store bundle, the bundle is
     this root and the list goes away.

  From each root, the closure follows the parameters and results of exported
  functions and methods, the exported fields of structs, the methods of
  interfaces, embedded types and type arguments, until it finds no new type.
- **R3. Conformance suite.** The package is the conformance suite of an
  interface that R2 reaches, so that an adapter outside this module can prove
  that it implements the port.

Two more rules apply to each package that is SDK:

- **No leak.** No exported signature names a type from `internal/` or from a
  `.../internal/...` path. A type that the closure reaches is SDK by R2, so
  if it lives in `internal/`, it moves to `pkg/` or the port stops naming it.
- **Split.** If a package passes R2 but also declares exported types that the
  closure does not reach (for example a `Service` with domain logic), it is
  split. The ports, the types that the closure reaches, their methods, the
  sentinel errors and the error types stay in `pkg/<name>`. The rest moves to
  `internal/`. Question 2 asks the owner to confirm the split.

The rule is mechanical. The CI check below applies it, so a reviewer does not
have to.

### What the rule gives for the 8 packages without a caller

None of `stepup`, `cost`, `chatops`, `incident`, `oncall`, `source`,
`launcher` and `dependency` passes R1, R2 or R3: `NewServer` does not take
their ports, and no store implements them. So all 8 packages and their 6
suites move to `internal/`. When one of these features gets its first caller
and `NewServer` takes its port, R2 passes and its port moves back to `pkg/`
in that pull request. RFC 102 proposes ports in `pkg/stepup`. Under this rule
they wait in `internal/stepup` until the feature is wired.

## Measured state

### How it was measured

- Packages, importers and line counts: `go list -f` over `./...`, and a count
  of the lines of the non-test files of each package.
- Leaks and the port closure: a prototype that loads `./pkg/...` with
  `golang.org/x/tools/go/packages` in a scratch module outside the
  repository, and walks the types as R2 says. The roots for the store ports
  were the 34 interfaces that `pkg/dustpan` asserts on the namespace store,
  plus every exported interface in `pkg/` that `*sqlite.Store`,
  `*postgres.Store` or `*fs.Store` implements: **121 interfaces**, of which
  101 are in domain packages and 20 in suites. The 34 asserted interfaces
  add no package that the implemented interfaces do not already reach.
- The CI check in this RFC uses the standard library only, and does the same
  walk (see "The CI check").

### Leaks of internal types into exported signatures (14)

| Package | Exported name | Internal type |
| --- | --- | --- |
| `pkg/dustpan` | `Server.Metrics` | `internal/metrics.ServerMetrics` |
| `pkg/stack` | `Checkpoint.Lease` | `internal/token.Digest` |
| `pkg/stack` | `EventBatch.Lease` | `internal/token.Digest` |
| `pkg/stack` | `JournalBatch.Lease` | `internal/token.Digest` |
| `pkg/stack` | `JournalRow.Lease` | `internal/token.Digest` |
| `pkg/stack` | `Lease.Digest` | `internal/token.Digest` |
| `pkg/stack` | `Started.Token` | `internal/token.Token` |
| `pkg/stack` | `Updates.SaveCheckpointDelta` | `internal/token.Token` |
| `pkg/workspace` | `PendingState.Upload` | `internal/token.Token` |
| `pkg/workspace` | `Service.Upload` | `internal/token.Token` |
| `pkg/workspace` | `StateVersion.Upload` | `internal/token.Digest` |
| `pkg/store/postgres` | `Store.AddFailure` | `internal/authgate.FailureCount` |
| `pkg/store/sqlite` | `Store.AddFailure` | `internal/authgate.FailureCount` |
| `pkg/store/storetest` | `SignInFailureHarness.Open` | `internal/authgate.FailureCounter` |

Three causes, three fixes:

- `token.Digest` and `token.Token` are in store ports (`stack.Lease`,
  `workspace.StateVersion`), so a store must name them. R2 makes them SDK.
  Move `internal/token` to `pkg/token`. It is 2 types and 4 functions with no
  dependency.
- `authgate.FailureCounter` is a store port (the sign-in failure windows) that
  lives in an internal package. `NewServer` asserts it on the namespace
  store. A third-party store cannot implement it. Move the interface and
  `FailureCount` to `pkg/session`, next to the other sign-in ports.
- `Server.Metrics` hands the binary two callbacks
  (`cmd/opendustpan/main.go:1065` and `:1068`). Replace it with two narrow
  options or two methods that take and return standard types, and keep
  `internal/metrics` internal.

`pkg/stack` also imports `internal/pulumijournal`, and 31 packages import
`internal/ident`. No exported signature names a type from them, so they are
not leaks. An SDK package can import `internal/` for its implementation.

### The verdict for each package (148)

Legend:

- **SDK**: stays in `pkg/` whole.
- **SDK: suite**: a conformance suite that R3 keeps.
- **split**: the ports and the reached types stay in `pkg/<name>`, the rest
  moves to `internal/`.
- **internal**: moves to `internal/` whole.
- **internal: no caller**: one of the 8 packages without a production caller.
- **internal: adapter (Q1)**: a built-in adapter. Question 1 decides it.
- **internal already**: under a `pkg/store/.../internal/` path today. It moves
  with the store adapters.

"Lines" is non-test lines. "Importers" is the count of packages in the module
that import it from non-test code.

| Verdict | Packages |
| --- | ---: |
| SDK (entry point and whole packages) | 8 |
| SDK: suite | 32 |
| split | 30 |
| internal | 32 |
| internal: no caller | 14 |
| internal: adapter (Q1) | 19 |
| internal already | 13 |
| **Total** | **148** |

So the SDK keeps 70 packages: 8 whole, 32 suites, and the `pkg/` half of 30
split packages. 78 packages leave `pkg/`.


## The target layout

```
pkg/
  dustpan/        the entry point: NewServer, the options, the store bundle (finding 2)
  token/          lease and upload tokens and their digests (from internal/token)
  blob/ egress/ idempotency/ identity/ installation/ logsink/ registry/
                  whole SDK packages: ports, value types, errors
  <domain>/       for each of the 30 split packages: the ports, the types they
                  name, sentinel errors, error types
  <domain>/<domain>test/, store/storetest/
                  the 32 conformance suites
internal/
  <domain>svc/    the services of the split packages (for example internal/stacksvc)
  <feature>/      the 32 internal packages and the 14 packages without a caller
  store/          sqlite/, postgres/, schema/, internal/... (if Q1 is B)
  blob/, logsink/, notify/, forge/, gate/, registry/
                  the built-in adapters (if Q1 is B)
  tools/sdksurface/
                  the CI check
```

A service package gets the suffix `svc` (question 2). The callers of a split
package, for example `internal/api/pulumi`, use both the types
(`stack.Update`) and the service. With two packages of the same name, each
such file needs an import alias. The suffix makes the alias unnecessary.

The depguard `domain` rule in `.golangci.yml` then lists the `pkg/` domain
packages and the `internal/<domain>svc` packages. The gap that the review found
(`cron`, `idempotency`, `manuallock` and `profile` are not in the rule) closes
in the same change.

## The move plan

Each step is one pull request. A whole-package move is mechanical: `git mv`,
a rewrite of the import path in every `.go` file, and the same rewrite in
`.golangci.yml`, `sqlc.yaml`, the `Makefile` and `AGENTS.md`. A script does
it (`scripts/move-package.sh <from> <to>`, added in step 1). If the base
branch moves, the author runs the script again on the new base. There is no
hand-made conflict resolution in a move.

A split is not mechanical. Each split is its own pull request and is reviewed
like code.

The order goes from the packages with the fewest importers and no open pull
request, to the packages that the open architecture pull requests change.

| Step | Pull request | Packages | Waits for |
| --- | --- | --- | --- |
| 0 | The ADR for this RFC | none | the owner |
| 1 | The CI check, with a baseline of today's findings, and the move script | none | step 0 |
| 2 | Move the 8 packages without a caller and their 6 suites to `internal/` | 14 | step 1 |
| 3 | Move the other packages that R2 does not reach: `agent`, `calendar`, `catalog`, `cron`, `freeze`, `policy`, `quota`, `scopeaudit`, `unmanaged`, `unitimport`, `egress/egresshttp`, `registry/service`, `registry/releasecheck`, with their suites. `runner/protocol` stays in `pkg/`: a nested module imports it (decision 4 of ADR 272). Before the move, the interface `registry/service.Namespaces` goes to `pkg/registry`. The built-in store implements it | 23 | step 1 |
| 4 | Move `trigger` and `trigger/triggertest`, or keep its port if #1392 wires it into `NewServer` | 2 | #1392 |
| 5 | Move the test helpers: `blob/blobfault`, `store/storefault`, `registry/pgpfixture`, `graph/graphtest`, `history/historytest`, `breakglass/breakglasstest`, `notify/chat/chattest` | 7 | step 1 |
| 6 | Fix the leaks: move `internal/token` to `pkg/token`, move the failure counter port to `pkg/session`, replace `Server.Metrics` | 0 moved, 14 leaks fixed | #1435, #1454, #1455, #1456, #1458, #1459 (`pkg/stack`), #1452, #1457 (`pkg/workspace`) |
| 7 | Move the built-in adapters (if Q1 is B): `blob/fs`, `blob/s3`, `blob/cache`, `logsink/*`, `notify/chat`, `notify/chat/slack`, `notify/sinks`, `notify/webhook`, `forge/github`, `gate/cedargo`, `gate/jobgate`, `registry/pgpverify`. First move the interface `jobgate.Snapshotter`, which the built-in store implements, to `pkg/gate` | 16 | #1439 (`pkg/notify/chat`, `pkg/notify/webhook`) |
| 8 | Move the store adapters (if Q1 is B): `store/sqlite`, `store/postgres`, `store/schema` and the 13 internal store packages to `internal/store/` | 16 | #1450, #1428, #1431 (store files), #1437, #1478 (CI paths) |
| 9 to 38 | One split for each of the 30 packages, smallest first (see below) | 30 | finding 2 (the store bundle) for the store ports, and the PRs named below |
| 39 | Delete the baseline. Update the layout in `AGENTS.md`, `.golangci.yml` and `GLOSSARY.md` | none | all steps |

The order of the splits:

1. Small, with no open pull request: `namespace`, `collection`,
   `savedview`, `ipallow`, `manuallock`, `preferences`, `tempadmin`,
   `maintenance`, `drift`, `graph`, `history`, `breakglass`, `durable`,
   `inventory`.
2. Medium: `access`, `accesstoken`, `principal`, `session`, `profile`,
   `objectcontext`, `forge`, `runner`, `audit`, `backup`, `gate`, `job`.
3. After the open pull requests that change them:
   - `notify` after #1439.
   - `secret` after #1443.
   - `workspace` after #1452 and #1457.
   - `stack` after #1435, #1454, #1455, #1456, #1458 and #1459. #1459 adds
     the suite `pkg/stack/replayertest` for the journal replayer port. Only
     `internal/pulumijournal` implements that port and `NewServer` does not
     take it, so under R3 the suite goes to `internal/` with the stack split,
     unless the owner wants the replayer to be replaceable.

### Conflicts with the open architecture pull requests (#1435 to #1461)

Measured with `git diff --name-only` against the merge base of each branch,
without docs and tests.

| Pull request | Changes | Conflicts with |
| --- | --- | --- |
| #1435 checkpoint writer | `pkg/stack` | steps 6 and the `stack` split |
| #1436 Pulumi lease parse | `internal/api/pulumi` | import lines only (steps 6, 8, `stack` split) |
| #1437 PostgreSQL test database | CI | step 8 (paths in CI) |
| #1438 Pulumi audit entries | `internal/api/pulumi` | import lines only |
| #1439 outbound request rules | `pkg/notify/chat`, `pkg/notify/webhook`, `pkg/notify/internal/post` | step 7 and the `notify` split |
| #1441 OpenTofu audit rules | `internal/api/tfe`, `tofuhttp`, `tofuunit` | import lines only |
| #1442 profile user agent | `internal/api/profile` | import lines only |
| #1443 data keys | `pkg/secret` | the `secret` split |
| #1444, #1445, #1446, #1447 error module, route kit | `internal/api/*` | import lines only |
| #1448, #1449 TFE token and resolver | `internal/api/tfe`, `tofuhttp`, `tofuunit` | import lines only |
| #1450 stack rename while an update runs | `pkg/store/*`, `storetest` | step 8 |
| #1451 profile channel | `internal/api/profile` | import lines only |
| #1452 workspace lock error | `internal/api/tfe`, `tofuhttp`, `pkg/workspace` | step 6 and the `workspace` split |
| #1453 deleted-units cursor | `internal/api/inventory` | import lines only |
| #1454 stack state at | `internal/api/pulumi`, `pkg/stack` | step 6 and the `stack` split |
| #1455 deployment reader | `internal/api/pulumi`, `pkg/stack` | step 6 and the `stack` split |
| #1456 secrets provider on export | `internal/api/pulumi`, `pkg/stack` | step 6 and the `stack` split |
| #1457 workspace reveal | `internal/api/tfe`, `tofuhttp`, `pkg/workspace` | step 6 and the `workspace` split |
| #1458 journal decode once | `internal/pulumijournal`, `pkg/stack` | step 6 and the `stack` split |
| #1459 replayer suite | `internal/pulumijournal`, `pkg/stack`, `pkg/stack/replayertest` | step 6 and the `stack` split |
| #1460, #1461 route row, registry API | `internal/api/*` | import lines only |

#1440 is not open. "Import lines only" means that a move changes only the
import block of these files. Git can show that as a conflict in the import
block. The author of the later pull request runs `goimports` after the merge.
Steps 2, 3 and 5 conflict with none of these pull requests and can start
when the ADR is accepted.

## The CI check

A Go command in `internal/tools/sdksurface`, standard library only. It runs
as `make sdk-surface`, which `make check` and the lint job in
`.github/workflows/ci.yml` call.

How it works:

1. It runs `go list -export -deps -json ./pkg/...` and reads the path of the
   export data of each package. `go/importer.ForCompiler(fset, "gc", lookup)`
   loads the types from that export data. The build cache makes this fast
   after the first build.
2. **Leak check.** For each importable `pkg/` package, it walks each exported
   name: the type of a variable or constant, the signature of a function, the
   underlying type and the exported methods of a type, the exported and
   embedded fields of a struct, the methods of an interface, and type
   arguments. A named type or alias from a path that starts with
   `opendustpan.org/opendustpan/internal/` or that holds `/internal/` is a
   finding: `pkg/stack: Lease.Digest names internal/token.Digest`.
3. **Surface check.** It computes the port closure of R2 from its roots, and
   reports:
   - a non-suite `pkg/` package that the closure does not reach: `pkg/cron:
     no type is reachable from pkg/dustpan or a store port. Move it to
     internal/.`
   - a suite package (`pkg/<x>/<y>test`) whose parent package is not SDK.
   - an exported type in an SDK package, other than `pkg/dustpan`, that the
     closure does not reach and that does not implement `error`: `pkg/stack:
     Service is not reachable. Move it to internal/.`
4. **Baseline.** `internal/tools/sdksurface/baseline.txt` holds the findings
   of today, one per line, sorted. The check fails on a finding that is not in
   the baseline, and on a baseline line that no longer occurs. So the baseline
   only gets shorter, and each move pull request deletes its lines. Step 39
   deletes the file.

The command has its own tests, with a test module in `testdata/` that has one
hostile case for each walk path: an alias of an internal type, an internal
type as a type argument, an embedded internal struct, an internal type in a
method of an exported type, and an unexported field of an internal type (not a
finding).

The prototype that measured this RFC used `golang.org/x/tools/go/packages`.
The check does not, because `CLAUDE.md` asks for the standard library first.
`golang.org/x/tools` is already in `go.sum` as an indirect dependency. If the
`go/importer` path proves too slow in CI, the author asks the owner before
making it a direct dependency.

## Questions for the owner

### 1. Are the built-in adapters SDK?

ADR 35 lists `pkg/store/sqlite`, `pkg/store/postgres`, `pkg/blob/fs` and
`pkg/blob/s3` as SDK. The owner's list in "What is decided" does not. Today
`*sqlite.Store` has 272 exported methods and `*postgres.Store` has 278. All
of them become a promise at 1.0 if the adapters stay importable.

- **A. Keep the built-in adapters in `pkg/`.** An embedder imports
  `pkg/store/sqlite` and passes it to `NewServer`. Cost: about 550 exported
  methods and every adapter type are frozen at 1.0. Each refactor of a store
  method is a breaking change.
- **B. Move them to `internal/`, and add constructors to `pkg/dustpan`.** For
  example `dustpan.OpenSQLite(ctx, path)` and `dustpan.OpenPostgres(ctx, dsn)`
  return the store bundle of finding 2, and `dustpan.FileBlobs(dir)` and
  `dustpan.S3Blobs(cfg)` return a `blob.Store`. An embedder gets the same
  adapters through a few functions, and the adapter types are not promises.
- **C. Keep only the store adapters in `pkg/`, and move the others.** A
  middle way. It keeps the 550 methods.

Recommendation: **B.** The decision "a third-party store must be able to
implement the ports" needs the ports and the suites in the SDK, not the
built-in adapters. With B the SDK names the store contract once (the bundle),
and the SQL code can change without a major version. Steps 7 and 8 depend on
this answer.

### 2. Split the domain packages, or keep them whole?

30 packages hold ports and also a service (for example `stack.Service`,
`stack.Updates`, `gate.ConfigService`). Embedders do not call the services:
`NewServer` builds them.

- **A. Keep each package whole in `pkg/`.** No splits. The SDK keeps about
  3,159 exported names in 40 packages. Each service refactor is a breaking
  change after 1.0. ADR 2's rule "a domain package defines the interfaces that
  it consumes" stays as it is.
- **B. Split.** The ports, the reached types and the errors stay in
  `pkg/<name>`. The service moves to `internal/<name>svc`. ADR 2's rule
  changes to "a port lives in the SDK package of its domain, and the service
  that consumes it lives in `internal/<name>svc`". The ADR for this RFC says
  so. Cost: 30 pull requests that are not mechanical, and an import of two
  packages in the callers of each service.
- **C. Split only the 6 largest** (`stack`, `workspace`, `job`, `gate`,
  `backup`, `access`: 42,000 non-test lines together), and keep the others
  whole.

Recommendation: **B**, with the `svc` suffix. It is the only option where the
rule of the CI check ("every exported type in an SDK package is reachable") is
true without exceptions, so the check can stay strict after step 39. C is
the fallback if the owner wants fewer pull requests now. The check then needs
a list of exempt packages.

### 3. Keep a snapshot of the SDK API in the repository?

The CI check stops leaks and stray packages. It does not show a reviewer when
a pull request changes an SDK signature.

- **A. Now.** The check also writes `internal/tools/sdksurface/api.txt`, one
  line per exported SDK name with its signature, like the `api/` files of the
  Go project. CI fails when the file is not up to date. Each SDK change then
  shows in the diff of a pull request.
- **B. At 1.0.** Before 1.0 breaking changes are free (ADR 73), so the
  snapshot adds review noise for no protection.
- **C. Never.** Rely on `gorelease` or `apidiff` at release time.

Recommendation: **A.** The SDK shrinks to a small set of names after these
steps, so the file stays short. It shows the cost of a change at review time,
not at 1.0, when it is too late to change it cheaply.

## Outcome

The owner accepted this RFC on 22 October 2026. [ADR 272](../adr/0272-the-public-sdk-surface.md)
records the decisions:

- Question 1: B. The built-in adapters move to `internal/`. `pkg/dustpan` gets
  constructors for them.
- Question 2: B. All 30 packages that hold ports and a service are split. The
  service moves to `internal/<name>svc`.
- Question 3: A. The repository holds a snapshot of the SDK API, and CI checks
  it now.

The steps 7 and 8 of the move plan are no longer conditional on question 1.

## Appendix: the verdict for each package

| Package | Lines | Importers | Verdict |
| --- | ---: | ---: | --- |
| `access` | 5460 | 9 | split |
| `access/accesstest` | 5537 | 0 | SDK: suite |
| `accesstoken` | 2234 | 12 | split |
| `accesstoken/accesstokentest` | 1651 | 0 | SDK: suite |
| `agent` | 3458 | 1 | internal |
| `agent/agenttest` | 2994 | 0 | internal |
| `audit` | 4174 | 100 | split |
| `audit/audittest` | 3197 | 0 | SDK: suite |
| `backup` | 7198 | 8 | split |
| `blob` | 322 | 28 | SDK |
| `blob/blobfault` | 223 | 0 | internal |
| `blob/blobtest` | 849 | 0 | SDK: suite |
| `blob/cache` | 386 | 1 | internal: adapter (Q1) |
| `blob/fs` | 342 | 4 | internal: adapter (Q1) |
| `blob/s3` | 1571 | 1 | internal: adapter (Q1) |
| `breakglass` | 823 | 2 | split |
| `breakglass/breakglasstest` | 827 | 0 | internal |
| `calendar` | 1046 | 5 | internal |
| `calendar/calendartest` | 1140 | 0 | internal |
| `catalog` | 3758 | 1 | internal |
| `catalog/catalogtest` | 1343 | 0 | internal |
| `chatops` | 703 | 1 | internal: no caller |
| `chatops/chatopstest` | 971 | 0 | internal: no caller |
| `collection` | 381 | 18 | split |
| `collection/collectiontest` | 842 | 0 | SDK: suite |
| `cost` | 1087 | 1 | internal: no caller |
| `cost/costtest` | 775 | 0 | internal: no caller |
| `cron` | 498 | 2 | internal |
| `dependency` | 209 | 1 | internal: no caller |
| `dependency/dependencytest` | 305 | 0 | internal: no caller |
| `drift` | 636 | 8 | split |
| `drift/drifttest` | 1069 | 1 | SDK: suite |
| `durable` | 1521 | 21 | split |
| `durable/durabletest` | 2820 | 0 | SDK: suite |
| `dustpan` | 10784 | 3 | SDK: entry point |
| `egress` | 455 | 11 | SDK |
| `egress/egresshttp` | 165 | 3 | internal |
| `forge` | 2221 | 9 | split |
| `forge/connectiontest` | 1382 | 0 | SDK: suite |
| `forge/forgetest` | 658 | 0 | SDK: suite |
| `forge/github` | 694 | 1 | internal: adapter (Q1) |
| `freeze` | 781 | 1 | internal |
| `freeze/freezetest` | 1222 | 0 | internal |
| `gate` | 4668 | 13 | split |
| `gate/cedargo` | 965 | 1 | internal: adapter (Q1) |
| `gate/gatetest` | 4688 | 1 | SDK: suite |
| `gate/jobgate` | 202 | 1 | internal: adapter (Q1) |
| `graph` | 1121 | 16 | split |
| `graph/graphtest` | 249 | 0 | internal |
| `history` | 853 | 17 | split |
| `history/historytest` | 1326 | 0 | internal |
| `idempotency` | 309 | 15 | SDK |
| `idempotency/idempotencytest` | 830 | 0 | SDK: suite |
| `identity` | 1250 | 64 | SDK |
| `identity/identitytest` | 865 | 0 | SDK: suite |
| `incident` | 569 | 1 | internal: no caller |
| `incident/incidenttest` | 1012 | 0 | internal: no caller |
| `installation` | 129 | 8 | SDK |
| `inventory` | 1362 | 19 | split |
| `inventory/inventorytest` | 1073 | 0 | SDK: suite |
| `ipallow` | 497 | 8 | split |
| `job` | 6778 | 26 | split |
| `job/jobtest` | 7195 | 0 | SDK: suite |
| `launcher` | 714 | 0 | internal: no caller |
| `logsink` | 347 | 9 | SDK |
| `logsink/file` | 141 | 1 | internal: adapter (Q1) |
| `logsink/logsinktest` | 221 | 0 | SDK: suite |
| `logsink/objectstore` | 269 | 1 | internal: adapter (Q1) |
| `logsink/slogsink` | 362 | 1 | internal: adapter (Q1) |
| `logsink/syslog` | 481 | 1 | internal: adapter (Q1) |
| `logsink/webhook` | 250 | 1 | internal: adapter (Q1) |
| `maintenance` | 994 | 20 | split |
| `manuallock` | 622 | 13 | split |
| `manuallock/manuallocktest` | 1903 | 0 | SDK: suite |
| `namespace` | 395 | 119 | split |
| `notify` | 2367 | 12 | split |
| `notify/chat` | 791 | 3 | internal: adapter (Q1) |
| `notify/chat/chattest` | 559 | 0 | internal |
| `notify/chat/slack` | 335 | 1 | internal: adapter (Q1) |
| `notify/notifytest` | 3521 | 1 | SDK: suite |
| `notify/sinks` | 912 | 2 | internal: adapter (Q1) |
| `notify/webhook` | 286 | 1 | internal: adapter (Q1) |
| `objectcontext` | 2204 | 7 | split |
| `objectcontext/objectcontexttest` | 802 | 0 | SDK: suite |
| `oncall` | 1001 | 1 | internal: no caller |
| `oncall/oncalltest` | 1042 | 0 | internal: no caller |
| `policy` | 703 | 3 | internal |
| `policy/policytest` | 268 | 0 | internal |
| `preferences` | 1162 | 6 | split |
| `preferences/preferencestest` | 1055 | 0 | SDK: suite |
| `principal` | 1907 | 24 | split |
| `principal/principaltest` | 1967 | 1 | SDK: suite |
| `profile` | 2070 | 7 | split |
| `profile/profiletest` | 882 | 0 | SDK: suite |
| `quota` | 427 | 3 | internal |
| `quota/quotatest` | 580 | 0 | internal |
| `registry` | 754 | 11 | SDK |
| `registry/pgpfixture` | 201 | 0 | internal |
| `registry/pgpverify` | 374 | 1 | internal: adapter (Q1) |
| `registry/registrytest` | 1272 | 0 | SDK: suite |
| `registry/releasecheck` | 234 | 2 | internal |
| `registry/service` | 1177 | 2 | internal |
| `runner` | 2091 | 8 | split |
| `runner/protocol` | 479 | 2 | SDK: imported by a nested module (ADR 272) |
| `runner/runnertest` | 1536 | 0 | SDK: suite |
| `savedview` | 578 | 7 | split |
| `savedview/savedviewtest` | 1135 | 0 | SDK: suite |
| `scopeaudit` | 199 | 10 | internal |
| `secret` | 2384 | 23 | split |
| `secret/secrettest` | 2619 | 0 | SDK: suite |
| `session` | 2289 | 12 | split |
| `session/sessiontest` | 1969 | 0 | SDK: suite |
| `source` | 711 | 0 | internal: no caller |
| `stack` | 7248 | 15 | split |
| `stack/stackhistorytest` | 270 | 0 | SDK: suite |
| `stack/stacktest` | 54 | 0 | SDK: suite |
| `stepup` | 555 | 1 | internal: no caller |
| `stepup/stepuptest` | 200 | 0 | internal: no caller |
| `store/internal/baseline` | 0 | 0 | internal already, moves with the store (Q1) |
| `store/internal/codec` | 2633 | 2 | internal already, moves with the store (Q1) |
| `store/internal/devstrip` | 903 | 2 | internal already, moves with the store (Q1) |
| `store/internal/driftstore` | 250 | 2 | internal already, moves with the store (Q1) |
| `store/internal/gatestore` | 2189 | 2 | internal already, moves with the store (Q1) |
| `store/internal/githoststore` | 115 | 2 | internal already, moves with the store (Q1) |
| `store/internal/jobstore` | 2795 | 3 | internal already, moves with the store (Q1) |
| `store/internal/migrate` | 315 | 2 | internal already, moves with the store (Q1) |
| `store/internal/registrystore` | 188 | 2 | internal already, moves with the store (Q1) |
| `store/internal/sinkstore` | 187 | 2 | internal already, moves with the store (Q1) |
| `store/internal/viewstore` | 113 | 2 | internal already, moves with the store (Q1) |
| `store/postgres` | 26310 | 3 | internal: adapter (Q1) |
| `store/postgres/internal/db` | 17994 | 1 | internal already, moves with the store (Q1) |
| `store/schema` | 516 | 7 | internal: adapter (Q1) |
| `store/sqlite` | 28355 | 2 | internal: adapter (Q1) |
| `store/sqlite/internal/db` | 17692 | 1 | internal already, moves with the store (Q1) |
| `store/storefault` | 1001 | 0 | internal |
| `store/storetest` | 31272 | 0 | SDK: suite |
| `tempadmin` | 919 | 9 | split |
| `tempadmin/tempadmintest` | 750 | 0 | SDK: suite |
| `trigger` | 595 | 1 | internal |
| `trigger/triggertest` | 1332 | 0 | internal |
| `unitimport` | 1013 | 6 | internal |
| `unitimport/unitimporttest` | 1765 | 0 | internal |
| `unitimport/upload` | 670 | 0 | internal |
| `unmanaged` | 1920 | 1 | internal |
| `unmanaged/unmanagedtest` | 1960 | 0 | internal |
| `workspace` | 10366 | 16 | split |
| `workspace/workspacehistorytest` | 677 | 0 | SDK: suite |
| `workspace/workspacetest` | 54 | 1 | SDK: suite |
