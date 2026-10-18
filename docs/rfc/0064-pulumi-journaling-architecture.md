---
rfc: 64
title: Architecture of Pulumi journaling support
status: accepted
adr: 43
created: 2026-10-12
---

# RFC 64. Architecture of Pulumi journaling support

Status: accepted as ADR 43.

## Problem

### The premise

The project owner gave this direction for this RFC. This is a paraphrase,
not a quote:

- Journaling is on by default. One configuration option turns it off.
- That option is a normal setting in `internal/cli/settings.go`. It is not a
  feature-flag system.
- The owner wants a deep architectural review of journaling support that
  adds no tech debt, for example a hand-written JSON parser.
- The owner wants a way to find a break in the Pulumi API quickly.
- If the journal format is not stable, the code could live in a separate Go
  module, for example `opendustpan.org/opendustpan/x/pulumijournal`, with its
  own versions. Then an SDK user could upgrade that dependency without an
  upgrade of the whole installation.

This RFC takes the premise as given. It does not ask again if journaling is
worth it. RFC 62 (#350, under review) gives the measured benefit: at 3,000
resources, journaling took 44 s for create and 61 s for change, against
440 s and 792 s with delta uploads.

### The evidence

- **RFC 62 (#350, under review)**, on the branch `swarm/pulumi`:
  benefit, correctness matrix, cost of an import of `pkg/v3`, the storage
  model of the prototype, and the protocol risk.
- **The research note in #390** (`docs/research/pulumi-journaling.md` on the
  branch `swarm/pulumi-journal-research`): 51 upstream commits that touch
  journaling, 37 client-only, 7 additive, 3 semantic and 4 wire-break
  (section 3). It also gives the CLI version support (section 4), the
  protocol (section 5), the measured error behavior (section 6) and the
  requirements R1 to R37 (section 7).
- **The spike code** on `spike/pulumi-journaling` at `8ba6721`:
  `internal/pulumijournal/journal.go` (762 lines) and `links.go` (205 lines),
  a port of the upstream replayer, and `internal/api/pulumi/journal.go`
  (370 lines), the route.
- **The envelope spike** on `spike/pulumi-envelope` at `1958912`:
  `internal/api/pulumi/envelope.go`, a hand-written parser of 663 lines for
  the checkpoint envelope.
- **The JSON library comparison** on `spike/pulumi-json-libs` at `72895df`
  (`spike/json-libs.md`).
- **The pulumi/pulumi clone** (blobless, `master` at `2255563`, tags to
  `v3.268.0`). A path with `@<tag>` is the file at that tag.
- **Issue #389**: a reveal reads the whole state into memory. For OpenTofu,
  `io.ReadAll` plus a string copy uses about 1 GiB for a 512 MiB state. For
  Pulumi, `json.Unmarshal` decodes into `map[string]any`. The fix is a
  streaming `json.Decoder` with `Token()` that decodes only what it needs.
  The lesson for this RFC: never build a whole generic tree or a full copy of
  state in memory.

### The facts that shape the design

- **No fallback.** After a journal error, the CLI does not send a
  checkpoint in the same update. It does not retry 400 or 409. It sends a
  500 four times in total, 1 s, 2 s and 4 s apart, and then fails the
  update (research note in #390, section 6). So a server error during a
  journaled update fails the user's `pulumi up`. With checkpoints, a failed
  delta falls back to a full save (ADR 37, decision 7).
- **The format moves under version 1.** Kinds 8 and 9 and the flag
  `requiresByteString` arrived under the negotiated version 1 (research note
  in #390, section 3, rows #23564, #23650 and #23872). A reader that ignores
  them builds a different state. R14 says: refuse an unknown kind, do not
  ignore it.
- **The pace did not drop.** 9 of the 14 rows that are not client-only are
  from 2026-06 to 2026-09 (research note in #390, section 3, "Pace").
- **The CLI decides at start.** The CLI reads `journalVersion` from the
  start-update answer and picks the snapshot manager once for the update
  (`pkg/backend/httpstate/backend.go@v3.268.0:2136-2141`). It journals only
  for an answer of 1 or 2 (`backend.go@v3.268.0:1938-1943`).
- **Upstream plans the escape hatch.** The comment at
  `backend.go@v3.268.0:1939-1942` says that Pulumi can answer a version
  above 2 to change the API, "while the client will continue working with
  the non-journaling snapshotter". So an answer that the CLI does not accept
  is a supported path to checkpoints (R2).

## 1. Module boundary

### What goes where

The journal code has two parts with different rates of change:

| Part | Changes when | Candidate home |
| --- | --- | --- |
| Wire types of an entry, decode and validation (R14 to R17) | the CLI adds a kind, a field or a version | the journal package |
| Replay: entries on a base deployment to a deployment (R18 to R29) | the CLI changes a replay rule | the journal package |
| A test corpus: recorded entries, base and expected export per CLI release | each CLI release | the journal package (`testdata/`) |
| Storage of entries and of the built checkpoint, the fence, the lease check | an ADR of this project | `pkg/stack`, `pkg/store/...`, `pkg/blob` |
| The route, the negotiation, the status codes | the API contract | `internal/api/pulumi` |

The journal package must not import `pkg/stack`, `pkg/blob`, `pkg/store`,
`net/http` or any part of the fence. It takes bytes and readers, and gives
bytes, writers and errors. `pkg/stack` calls it through a small interface
that `pkg/stack` declares (CLAUDE.md, "Define interfaces in the package that
consumes them").

### Option 1A. A separate module `opendustpan.org/opendustpan/x/pulumijournal`

The main module path is `opendustpan.org/opendustpan` (`go.mod` at
`48ca134`). A nested module in `x/pulumijournal/` with its own `go.mod`
gets the path `opendustpan.org/opendustpan/x/pulumijournal`.

This is the owner's own suggestion. It gives three real gains:

- **The instability is visible.** The `x/` segment of the import path and
  the `v0` version tell each reader and each tool that the API can change.
  Go users know this convention from `golang.org/x/exp`. A package in the
  main module says the same thing only in a comment.
- **Embedders can upgrade it alone (ADR 35).** An SDK embedder can take a
  journal fix with one line in its own `go.mod`, without a new version of
  the whole server. The conditions are below.
- **The main module stays clean.** The wire types of a format that the
  project does not control stay out of the API of the main module. When
  `pkg/` gets a stability promise (for example at the first release, ADR
  21), the unstable types are already outside it.

The rest of this subsection gives the mechanics and the costs.

**No import of the main module.** The main module requires the new module.
If the new module also required the main module, each version of one would
name a version of the other. Go permits a cycle in the module graph, but a
release then needs two tags in a fixed order, and `go mod tidy` in each
module pulls the other. A depguard rule in the new module must forbid
`opendustpan.org/opendustpan/` except its own path.

**The `internal/` rule, measured.** The task brief for this RFC said that
`internal/` packages of the main module cannot be imported from another
module. A test with Go 1.27.2 showed a narrower rule. The rule is about
import paths, not about modules:

- A nested module `ex.org/m/x/sub` built with no error while it imported
  `ex.org/m/internal/foo`, with `go.work` and also with `GOWORK=off` and a
  `replace`. Its path is inside the tree of `ex.org/m`.
- A module `ex.org/other` failed with "use of internal package
  ex.org/m/internal/foo not allowed".
- The root module failed to import `ex.org/m/x/sub/internal/q` with the
  same error.

So the new module could import `opendustpan.org/opendustpan/internal/...`
and the compiler would not stop it. The cycle rule above forbids it anyway.
The main module cannot import `x/pulumijournal/internal/...`. Everything
that `pkg/stack` calls must be in an exported package of the new module, and
that package is then a public API with a version.

**Patterns.** `./...` in the root does not include a nested module, with or
without `go.work` (measured: `go list ./...` in the test root listed only the
root packages). Each tool that runs on `./...` runs once more in
`x/pulumijournal/`.

**Versions and tags.** A tag of a nested module has the directory as
prefix: `x/pulumijournal/v0.1.0`. The Go proxy maps that tag to
`opendustpan.org/opendustpan/x/pulumijournal@v0.1.0`. The vanity server for
`opendustpan.org` must answer the `go-import` meta tag for the new path, with
the repository root as the prefix. In this project only the owner pushes
tags. So each change of the journal module that the main module needs is:

1. a pull request in `x/pulumijournal`,
2. an owner tag `x/pulumijournal/v0.N.M`,
3. a pull request in the main module that bumps `go.mod`.

A pseudo-version (`v0.0.0-<time>-<sha>`) of a commit on `main` avoids step
2, but then the version does not tell an embedder what changed. ADR 7 says:
"new versions are adopted only once they are at least two weeks old". The
ADR does not name an exception for a module of this project. If the rule
applies, step 3 waits two weeks after each tag. The owner must say if it
applies.

A module zip holds only the files under the module directory. The root
`LICENSE` is not in the zip of `x/pulumijournal`. The new module needs its
own `LICENSE` file, and `make licenses` checks it, because
`scripts/third-party-licenses.sh` lists every module that is not `Main`
(lines 80 to 82).

**What an SDK embedder gains (ADR 35).** An embedder imports
`pkg/dustpan`, not the journal package. With 1A, the embedder can add
`require opendustpan.org/opendustpan/x/pulumijournal v0.N.M` to its own
`go.mod`. Minimal version selection then builds `pkg/stack` against that
newer version. This works only if the new version keeps the API that the
pinned `pkg/stack` calls. In `v0.x`, Go makes no such promise, so the
project must keep that API stable by its own rule, or the embedder's build
fails. No embedder exists yet: ADR 35 says that no release of OpenDustpan
exists, so no embedder depends on the import paths.

**What a binary user gains.** No faster fix. The binary links one version
of the module. A fix in the journal module reaches an operator only in a new
`opendustpan` binary. These changes could alter that:

- **A patch release for each journal fix.** Cost: an owner tag of the
  module, an owner tag of the binary, the release work of ADR 21 and ADR 27
  for each, and an upgrade for each operator. This is possible with 1A or
  with 1B. It does not need a separate module.
- **Plugin loading** (the Go `plugin` package). Cost: the plugin and the
  binary must use the same toolchain and the same version of each shared
  module. The `plugin` package needs cgo and does not support Windows. The
  release builds use `CGO_ENABLED=0` (`scripts/third-party-licenses.sh`
  line 80) and ship `windows/amd64` (Makefile, `RELEASE_PLATFORMS`).
- **An out-of-process replayer** (a subprocess, or a WebAssembly module).
  Cost: a second artifact with its own release, a new protocol between the
  server and the replayer, and a new attack surface.

The version gate of section 3 makes a missing fix less urgent. A CLI that
the binary does not know falls back to checkpoints, so a user loses speed,
not state.

**Repository cost, counted from the Makefile and `ci.yml` at `48ca134`.**

| Gate | Change for a second module |
| --- | --- |
| `make lint` | `go mod tidy -diff` and `golangci-lint run ./...` once more in `x/pulumijournal` |
| `make nilaway` | a second run. `-include-pkgs=opendustpan.org/opendustpan` matches the new path as a prefix, but `./...` does not reach it |
| `make vuln` | a second `govulncheck ./...` run |
| `make test` | a second `go test -race ./...` run, with its own `leak_test.go` |
| `make licenses` | no target change. The new module needs its own `LICENSE` |
| `make trivy` | `go mod download` once more in the new module for the license scan |
| `make semgrep`, `make trufflehog`, `make docs-lint` | no change: they scan the tree or the history |
| `sqlc diff` | not needed: the module has no SQL |
| `go.work` | new file, or a `replace` in the root `go.mod`. A `replace` is ignored when another module requires the main module, so an embedder gets the tagged version, not the code that CI tested |

So 5 of the 12 targets of `make check` change (lint, nilaway, vuln, test,
trivy). In the `go` job of `ci.yml`, 6 steps run once more or take a
`working-directory`: "go.mod and go.sum are tidy", "Verify module
checksums", the golangci-lint action, "Nil-panic analysis", "Known
vulnerabilities" and "Test". A release needs one more step that checks that
the root `go.mod` names a tagged version of the new module, not a
pseudo-version.

### Option 1B. A package in the main module

Two homes are possible:

- `internal/pulumijournal`, as in the spike. Embedders cannot import it.
  The project can change its API in any commit.
- `pkg/stack/journal`. Embedders can import it. ADR 35 makes `pkg/` the
  SDK, so the package would need a written note that its API is not stable
  before the first release.

`internal/pulumijournal` fits better. No embedder needs to call the replayer
directly. `pkg/stack` can import it, because `pkg/stack` is inside the tree
of the main module. depguard already forbids `net/http` and the store in
domain packages. One more rule keeps the journal package free of imports of
`pkg/...` and `internal/api/...`, so a later move to 1A is a move of files.

### Comparison

| | 1A. Separate module | 1B. `internal/pulumijournal` |
| --- | --- | --- |
| Instability visible to readers and tools | yes: `x/` path and `v0` version | only in a package comment |
| Unstable wire types outside the main module's API | yes | yes, while the package is under `internal/`; a later public API would have to wrap them |
| Binary user gets a fix | new binary | new binary |
| Embedder gets a fix | bump one line in `go.mod`, if the API stayed the same | bump the main module |
| A journal change | 3 steps, one owner tag, maybe 2 weeks of wait (ADR 7) | 1 pull request |
| `make check` targets that change | 5 of 12 | 0 |
| CI steps that change | 6 in the `go` job, 1 for releases | 0 |
| API discipline | exported, versioned, kept stable in `v0.x` by rule | free to change |
| Dependency graph of the binary | the same: a dependency of the new module is in the build list of the binary | the same |
| Later move to the other option | a move back needs a major version or a retract | a move of files, if depguard keeps the imports clean |

Note the row "Dependency graph of the binary". If the journal module
imports `github.com/pulumi/pulumi/sdk/v3` (section 2), the binary links it
in both options. A separate module does not keep a dependency out of
`govulncheck`, `make licenses` or Trivy for the binary.

### Why this RFC recommends 1B now, and what would change that

This is a recommendation. The owner decides.

1B now, because today the gains of 1A have no user and its costs arrive at
once:

- No SDK embedder exists (ADR 35), so nobody uses the separate upgrade
  path yet.
- Under `internal/`, the wire types are already out of the public API of
  the main module. So 1B gets the "clean main module" gain too, as long as
  no `pkg/` API exposes them.
- The journal format changed in 9 of the last 18 Pulumi releases in a way
  that the watch of section 3 would report. With 1A, each such change
  needs three steps and an owner tag, and maybe two weeks (ADR 7).

The depguard rule of 1B keeps the move to 1A a move of files. Take 1A when
one of these triggers occurs:

- **The first SDK embedder that needs journaling**, and that cannot wait for
  a server release to get a journal fix.
- **A need to ship journal fixes faster than server releases**, for
  example when the project starts a release cadence for the server that is
  slower than the Pulumi release cadence (about one release each 5.4 days,
  section 3).
- **A public API in `pkg/` that must expose journal types**, for example a
  replay call for embedders. Then the `x/` path and the `v0` version mark
  those types as unstable, which `internal/` cannot do.

## 2. No hand-written parser

### Wire types: three options

The JSON library question has its own research note:
`docs/research/json-libraries.md`, written on the branch
`swarm/pulumi-json-research`. That note holds the benchmarks, the
correctness and denial-of-service checks, and the provenance of third-party
JSON libraries and code generators (easyjson, goccy/go-json, sonic,
json-iterator, segmentio, go-json-experiment). This RFC takes its choice of
decoder from that note (commit `ac0df91` on that branch).

**Headline result of the JSON libraries research note** (Go 1.27.2, one
4-core amd64 machine, 10 interleaved samples, a change counts only when
benchstat gives p < 0.05; its sections 1 and 11):

| Candidate | Typed decode, checkpoint of 1,000 resources | New dependency | Notes from the note |
| --- | --- | --- | --- |
| `encoding/json` (std) | 29.88 ms | no | runs on the v2 code in Go 1.27.2 |
| `encoding/json/v2` (stdv2) | 17.90 ms (-40%) | no | 11 of 54 cases differ from std, from the v2 defaults |
| easyjson | 12.46 ms (-58%) | yes, plus `josharian/intern` | a code generator; the generated code imports `github.com/mailru/easyjson` at run time; 1 commit in 12 months |
| goccy/go-json | 7.56 ms (-75%) | yes | `unsafe` and assembly; one person wrote 98 of 101 commits in 12 months; v0.11.1 was 15 days old on 2026-10-12 |
| sonic | 7.93 ms (-73%) | yes, plus 5 modules on amd64 | JIT and assembly; 9 of 54 cases differ, it accepts a truncated top-level string and loses a byte; native code only below Go 1.28 |
| segmentio | 38.66 ms (+29%) | yes | no depth limit |

The order is the same for journal batches and OpenTofu state. For the #389
lookup, a `jsontext.Decoder` walk takes 44 to 51 ms on a 19 to 20 MB
document and allocates about 14 KiB. The `json.Decoder.Token` walk that
#389 proposes takes 101 to 131 ms and allocates 22 to 34 MiB. The full
decode of today takes 296 to 352 ms and 63 to 87 MiB. The note found no
known advisory for any candidate, and all licenses are compatible with
ADR 22.

**The decoder that this RFC recommends** is `encoding/json/v2` with
`encoding/json/jsontext`, from the Go 1.27.2 standard library:

- It is on by default in Go 1.27.2 (see option 2B below).
- It decodes the typed checkpoint about 40% faster than `encoding/json` at
  1,000 resources (p < 0.05).
- It adds no dependency, so it needs no request to the owner under the
  dependency rule of `CLAUDE.md`.
- A `jsontext` walk does the #389 lookup on about 20 MB in about 14 KiB of
  allocation. The same tool reads one resource of a base checkpoint at a
  time for the streaming build of this section.

Why the faster libraries do not fit now:

- **goccy/go-json and sonic** are about 2.3 times faster than stdv2 on the
  typed decode, but each is a new dependency with `unsafe` and assembly.
  sonic differs from `encoding/json` on 9 of 54 cases, among them a
  truncated string that it accepts with a lost byte, and its native code
  builds only below Go 1.28, so a Go upgrade changes its behavior. goccy has
  one main maintainer, and the tested version was 15 days old on
  2026-10-12, just past the two weeks of ADR 7.
- **easyjson** is a generator, so it is not a hand-written parser, but its
  generated code needs the `easyjson` module at run time, plus
  `josharian/intern`. It adds a dependency and a second generator next to
  the one of option 2C.

The decision stays with the owner. If the owner accepts a dependency later,
the note gives the facts for each library.

The comparison in `spike/json-libs.md` on `spike/pulumi-json-libs` is about
the checkpoint envelope, not about journal entries. It measured the custom
parser of the envelope spike against `encoding/json/v2` (stdv2) and three
libraries. It recommended the custom parser. This RFC does not take that
parser for journal entries: the owner asked for no hand-written parser.

#### Option 2A. Import `github.com/pulumi/pulumi/sdk/v3` for `apitype`

Measured on 2026-10-12 in a scratch module under `.tmp/` (deleted after the
measurement). The module had one `main.go` that imports
`github.com/pulumi/pulumi/sdk/v3/go/common/apitype` and prints
`apitype.LatestJournalVersion`. Go 1.27.2. The tag `sdk/v3.268.0` is the
commit `128d3f2` of `v3.268.0`.

| Measure | Value | Command |
| --- | --- | --- |
| Modules in the build list | 164 | `go list -m all \| wc -l` |
| Edges in the module graph | 403 | `go mod graph \| wc -l` |
| Modules linked into the binary | 34 | `go version -m <binary>` |
| Packages built (all / not standard library) | 438 / 216 | `go list -deps ./...` |
| `go.sum` lines | 247 | `wc -l go.sum` |
| Module cache after `go mod tidy` | 558 MB (154 MB of zips in `cache/download`) | `du -sh`, without the Go toolchain download |
| Module cache of `github.com/pulumi/...` | 45 MB | `du -sh` |
| Binary size | 19,086,484 bytes | `go build` |
| `govulncheck` v1.8.0, run on 2026-10-12 | 6 in total, in the three classes that govulncheck reports: 4 called (symbol results) in `golang.org/x/net@v0.59.0` (GO-2026-6617, GO-2026-6612, GO-2026-6611, GO-2026-6603, fixed in v0.60.0), 1 in an imported package that the code does not call (GO-2026-6610, `x/net`), 1 in a required module only (GO-2026-5932, `golang.org/x/crypto/openpgp`, no fix) | `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...` |

For comparison, the main module today has 39 modules in its build list and
100 edges (`go list -m all`, `go mod graph` at `48ca134`).

The path to the network code: `apitype` imports `common/resource`, which
imports `common/util/logging`, which imports `google.golang.org/grpc`, which
imports `golang.org/x/net/trace` (`go mod why -m`). `apitype` also imports
`common/esc/schema`, which imports `github.com/pulumi/pulumi-cloud-sdk/go`.
So one package of types links gRPC, OpenTelemetry and the Pulumi Cloud SDK.

- **Age (ADR 7).** `v3.268.0` has the date 2026-10-07, 5 days before this
  RFC. It meets the two-week rule on 2026-10-21. The newest tag that met the
  rule on 2026-10-12 was `v3.265.0` (2026-09-25).
- **License.** `sdk/v3` is Apache-2.0 (`LICENSE` in the module cache). So
  is `github.com/pulumi/pulumi-cloud-sdk/go`. The other modules seen in the
  cache were MIT. The repository has no `NOTICE` file at `v3.268.0`.
- **Memory.** `apitype.ResourceV3` keeps `inputs` and `outputs` as
  `map[string]any` (`sdk/go/common/apitype/core.go@v3.268.0:461-464`).
  `JournalEntry.State` is a `*ResourceV3` and `NewSnapshot` is a whole
  `*DeploymentV3` (`apitype/journal.go@v3.268.0`). A decode with these types
  builds the generic tree that #389 forbids, and `encoding/json` turns each
  number into a `float64`.
- **Decision.** CLAUDE.md says "Ask before adding a dependency". Adding
  `sdk/v3` is the owner's decision.

#### Option 2B. Own types with the standard library

Types of this project for the entry and the request, with the fields of
section 5.3 of the research note in #390. Resource states, operations and
snapshots stay `json.RawMessage` (or `jsontext.Value`), as in the spike
(`internal/pulumijournal/journal.go@8ba6721`, type `Entry`). Only the fields
that replay reads get a Go type: the indices, the IDs, the kind, the
version, and the link fields of a resource (`urn`, `parent`, `provider`,
`dependencies`, `propertyDependencies`, `deletedWith`, `replaceWith`,
`viewOf`, `aliases`), as `links` does in the spike
(`internal/pulumijournal/links.go@8ba6721`).

`encoding/json/v2` with Go 1.27.2, measured:

- `go doc encoding/json/v2` and `go doc encoding/json/jsontext` answer with
  no `GOEXPERIMENT`.
- A program that imports `encoding/json/v2` builds and runs with no
  `GOEXPERIMENT`.
- The source files still carry `//go:build goexperiment.jsonv2`, and
  `internal/buildcfg/exp.go` puts `JSONv2: true` in the baseline. So the
  experiment is on by default in 1.27.2, and `GOEXPERIMENT=nojsonv2` turns
  it off. With the experiment on, `encoding/json` (v1) runs on the v2
  implementation (`encoding/json/decode.go` carries
  `//go:build !goexperiment.jsonv2`).
- The toolchain source has no text that puts v2 under the Go 1
  compatibility promise, and the build tag is still an experiment tag. The
  JSON libraries research note quotes the go-json-experiment README: v2
  became "a stable API in Go 1.27". If that is not so, an API change in a
  later Go release could break code that uses it.

`jsontext.Decoder` can read a token, or read one value as raw bytes
(`ReadValue`), with no tree. That is the streaming tool that #389 asks for,
in the standard library. `encoding/json` v1 `Decoder.Token()` also streams,
with more allocations (the envelope spike measured stdv2 at 19,043
allocations for 1,000 resources against 31 for a v1 decode into
`json.RawMessage`; `spike/json-libs.md`). The JSON libraries research note
measured the lookup of #389 both ways (see above): the `jsontext` walk
allocated about 14 KiB, the `json.Decoder.Token` walk 22 to 34 MiB.

Cost: about 230 lines of types to keep in step with
`sdk/go/common/apitype/journal.go@v3.268.0` (230 lines), by hand, at each
CLI change that touches the entry.

#### Option 2C. Types generated from the upstream source

A small generator lives in this repository (for example
`internal/pulumijournal/internal/gen`). It reads
`sdk/go/common/apitype/journal.go` at a pinned upstream commit with
`go/parser`, and writes Go types for `JournalEntry`, `JournalEntries` and
the kind constants. Rules:

- **The pin.** The first pin is the commit
  `128d3f2ec94e147915a6a928fdf7449d561d560e`, which is the release tag
  `v3.268.0` and also the tag `sdk/v3.268.0`. The input file has the SHA-256
  `088753b1c35890644e63168c05e35fe2c88751f6317418a6db65fdf8455464cc` at
  that commit. The file is the same at `2255563`, the commit that the spike
  port names (research note in #390, section 2). The generator keeps the
  commit, the tag and the hash in one small file next to it.
- **The input.** The generator reads a copy of the file in `testdata/`
  and refuses to run when the SHA-256 of the copy is not the pinned hash.
  So `make generate` needs no network, and a person who changes the copy by
  hand gets an error.
- **The output.** Fields of types from `core.go` (`ResourceV3`,
  `OperationV2`, `DeploymentV3`, `SecretsProvidersV1`, `SnippetV1`) become
  raw JSON, so the output has no generic tree and imports nothing from
  Pulumi. The generated file carries the Apache-2.0 header of the input
  file and a line that names the commit and the tag, because it is a
  derived work of that file.
- **In `make generate`.** The target runs the generator after `sqlc
  generate`.
- **In CI.** `make lint` runs the generator into a temporary directory and
  fails on a difference with the file in the tree, as `sqlc diff` does
  today. A hand edit of the generated file fails CI.

**How the pin moves.**

1. The upstream watch (section 3, layer B) finds a change in
   `apitype/journal.go` (or in another file of its list) between the pin and
   the newest release tag. It opens an issue in this repository, with the
   diff.
2. A person reads the diff and marks it client-only, additive, semantic or
   wire-break, as the research note in #390 does.
3. The person copies the file at the new tag into `testdata/`, changes the
   pinned commit, tag and hash, and runs `make generate`. The diff of the
   generated file shows each new field and kind.
4. The person changes the replay code when the diff needs it, and records
   the new release in the corpus (section 3, layer C).
5. The corpus test must pass, and the e2e run of that CLI with journaling
   on and off must give the same normalized export, before the pull request
   merges. Only then does the newest verified CLI version of the version
   gate (section 3, layer D) move to that release.

Cost: about 200 lines of generator, plus the cost of 2B for the types that
the generator cannot know (the link fields of a resource). Gain: a CLI
change in `journal.go` shows as a diff in a generated file, in a pull
request with a known procedure.

### Replay: port or import

- **Port with attribution.** The spike ports `pkg/backend/journal.go`
  (921 lines at `v3.268.0`) into 967 lines. Version 2 needs
  `pkg/backend/journal_state_migration.go` too (212 lines). pulumi/pulumi is
  Apache-2.0 and has no `NOTICE` file at `v3.268.0`, so section 4(d) of the
  license does not apply. Sections 4(b) and 4(c) do: each ported file keeps
  the upstream copyright line ("Copyright 2016, Pulumi Corporation." in
  `journal.go`), says that it was changed, and names the commit. The
  project disables `goheader` because "LICENSE at the root is enough"
  (`.golangci.yml`). The ported files are an exception, and a test can check
  that each file in the package has the header. ADR 22 (Apache-2.0) makes
  the licenses compatible.
- **Import `pkg/v3`.** RFC 62 (#350, under review), question 3, measured
  `github.com/pulumi/pulumi/pkg/v3/backend` at v3.250.0: 473 modules, 2,008
  module graph edges, about 24 MB of downloads, about 107 MB unpacked, and
  16 vulnerabilities from 8 modules. The replayer also works on `apitype`
  types, so it has the memory problem of 2A.

A port is the only option that meets both the dependency rule and #389.
The spike port does not yet do `NormalizeURNReferences` (R27, #21578): a
search for "normaliz" in `internal/pulumijournal/` at `8ba6721` finds
nothing. Production needs it. Upstream, it reads only link fields
(`sdk/go/common/apitype/core.go@v3.268.0:196-270`), so a port can do it on
the link fields alone.

### Streaming and memory bounds

The spike builds a whole checkpoint after each batch and saves it through
`SaveCheckpoint` (RFC 62, question 4). At 3,000 resources, the export is
9,047,131 bytes and a create sends 382 batches (RFC 62, question 1). So one
create builds and gzips about 382 × 9 MB, about 3.5 GB of deployment text.
The spike holds the base as one `json.RawMessage` per resource plus the
built result: two copies of the state in memory.

A design that follows #389:

1. **Store the entries, not a checkpoint per batch.** Each accepted batch is
   a write-once blob, for example
   `pulumi/<stack ID>/<update ID>/journal/<first sequence ID>`, written
   before the fenced SQL row that names it (ADR 37, decisions 2 and 3). A
   2xx then means "stored durably" (R11). At 3,000 resources that is about
   450 KB in 382 small blobs for one create (RFC 62, question 1). A retry
   with the same sequence IDs finds the row and answers 2xx with no write
   (R12).
2. **Build the checkpoint once, as a stream.** Build it at `complete`, at
   `cancel`, and when a read needs the state of an update that has not ended
   (R33, R34). The replay state in memory is: the maps of indices and
   operation IDs (a few integers for each entry), the new resource states
   of this update (the bytes that the entries carried), and an index of the
   link fields of each resource (URNs, no inputs or outputs). The base
   checkpoint is read from its blob with a streaming decoder, one resource
   at a time, and each resource is written out as raw bytes, or with its
   link fields changed. The output goes through gzip and SHA-256 into
   `blob.Store.Put`, which takes an `io.Reader` (`pkg/blob/blob.go:73`). A
   refresh and URN normalization need the link index before the first
   resource is written, so the base is read two times.
3. **Bound each update.** A limit on the entries, the new state bytes and
   the link index of one update, with the existing
   `max-checkpoint-bytes` (32 MiB by default) as the limit of the result.
   The research note in #390 asks for an answer of 413 when a batch is too
   large (R10).

The cost in memory of the built checkpoint is then about the size of the
new resource states of the update, plus the link index, plus one resource of
the base, plus the gzip window. It does not grow with the size of the base
inputs and outputs. This RFC did not measure it. A benchmark in the new
package must measure it before an ADR fixes the limits.

**ADR 37 fit.** Write-once keys, blob before row, and the sweep stay. Delta
saves are not used in a journaled update: the CLI uses either the journal
or the checkpoint persister (`backend.go@v3.268.0:2136-2165`). A delta is
always on the last save of the same update (ADR 37, decision 7), so a
checkpoint that the server built does not change the delta rules of a later
update. Decision 4 (exact bytes) and decision 8 (no parse) cannot hold for a
journaled update. See section 5.

**ADR 41 fit.** ADR 41, decision 1, lets the server read the state that it
stores. Replay does not need the secrets: entries carry ciphertext and the
replayer copies it (R31). So replay needs no new action and writes no audit
entry. A masked read (ADR 41, decision 2) of a journaled update reads the
built checkpoint like any other.

## 3. Catching a break in the Pulumi API

The plan relies only on checks that this project runs itself. It reads the
public Pulumi source and runs released Pulumi CLIs. By a rule of the project
owner, it does not report to, or engage with, the Pulumi project: no issue,
pull request, discussion, comment or post there.

Pulumi released 18 minor versions from `v3.251.0` (after 2026-07-01) to
`v3.268.0` (2026-10-07), about one each 5.4 days (tag dates in the clone).
The e2e job of `ci.yml` already runs Pulumi "latest" on each pull request
and each push to `main` (`e2e/tool.go`, defaults `latest`, `3.250.0`,
`3.200.0`). On 2026-10-12, three runs of that job on `main` took 5.6, 5.2
and 5.4 minutes (`gh run view`).

### Layer A. Nightly e2e with the newest CLI, and with a CLI from master

- **Newest release.** A scheduled workflow runs the Pulumi part of the e2e
  suite with `E2E_PULUMI_VERSIONS=latest` and journaling on, each night.
  It catches a release on a day with no push to `main`. Cost: about 6
  runner minutes each night, about 180 each month. Maintenance: a red run
  needs a person.
- **CLI from master.** Measured on 2026-10-12 on a 4-core machine with
  15 GB of memory: `go build ./cmd/pulumi` in `pkg/` at `2255563`, with
  `-ldflags -X .../version.Version=v3.269.0-dev.0`, with Go 1.27.2 (the
  `pkg/go.mod` asks for Go 1.26.6). Module download: 20 s. Compile: 159 s
  until the disk was full, then 94 s more on the warm cache, so about 4.5
  minutes cold. Module cache: about 1.3 GB without the toolchain. Build
  cache: 1.7 GB. Binary: 167,600,556 bytes. `pulumi version` printed
  `v3.269.0-dev.0`. The source came from `git archive` of `pkg`, `sdk` and
  `proto` (250 MB). The e2e harness has no setting for a local CLI binary
  today, so this needs a harness change. Cost: about 5 more minutes and
  about 3 GB of disk each night, about 150 runner minutes each month.
  Maintenance: a master build can fail for reasons that have nothing to do
  with journaling.

### Layer B. Upstream watch

A scheduled workflow clones pulumi/pulumi with `--filter=blob:none`, and
diffs these files between the pinned upstream commit of the port and the
newest release tag. It opens an issue in this repository, with the diff,
when a file changed. It only reads the public source. It posts nothing to
pulumi/pulumi or to any channel of the Pulumi project.

The file list comes from the research note in #390 (section 2 and section
3, "Method"):

- strict set: `sdk/go/common/apitype/journal.go`,
  `pkg/backend/journal.go`, `pkg/backend/journal_state_migration.go`,
  `pkg/backend/httpstate/journal/snapshot.go`,
  `pkg/engine/journal_snapshot.go`,
  `pkg/engine/journal_state_migration.go`;
- wide set, only commits that match `[Jj]ournal` (`git log -G`):
  `pkg/backend/httpstate/backend.go`,
  `pkg/backend/httpstate/client/client.go`,
  `pkg/backend/httpstate/client/api.go`,
  `sdk/go/common/apitype/updates.go`, `sdk/go/common/apitype/core.go`,
  `pkg/resource/stack/deployment.go`,
  `sdk/go/common/util/httputil/http.go`, `sdk/go/common/env/env.go`.

Measured on the clone for the 18 releases from `v3.251.0` to `v3.268.0`,
each against the release before it: the strict set changed in 6 releases.
A strict change or a matching commit in the wide set came in 9 releases
(v3.254.0, v3.256.0, v3.257.0, v3.260.0, v3.261.0, v3.262.0, v3.264.0,
v3.266.0, v3.268.0). Any change in the wide set came in 12 releases, so a
plain diff of the wide set gives too many issues. A diff of the 15 files
took 0.28 s on the local clone.

Cost: about 2 runner minutes each day for the clone and the diff (the clone
time was not measured), about 60 each month. Maintenance: about one issue
each 11 days at the rate above. Each needs a person to read the diff and
mark it client-only, additive, semantic or wire-break, as the research note
does. The watch can also run on `master`, not only on tags. Then it warns
before a release, with more noise.

### Layer C. A recorded corpus with an equivalence test

For each CLI release in the e2e matrix, and for each new release that layer
A runs, the e2e recorder (`e2e/recorder.go`) keeps:

- the base deployment that the server served at the start of the update,
- each journal batch body,
- the `stack export` of the same program with journaling off
  (`PULUMI_DISABLE_JOURNALING=true`).

A unit test in the journal package replays the batches on the base and
compares the result with the export. "Equal" means equal after the
normalization of RFC 62, question 2: drop the manifest, the metadata, the
created and modified times, the provider-assigned IDs and the provider
references, and sort resources by URN. The test runs in `go test` with no
CLI, in seconds. The e2e suite runs the same program both ways, as ADR 37
already asks for delta uploads ("Both runs must give the same `stack
export`").

The corpus must cover each kind (0 to 9 for version 1, and 10 for version
2), a refresh that deletes, a crash, a failed step, `requiresByteString`
and snippets. RFC 62 lists the cases that the spike did not test: an update
in place, `pending_operations` and state migrations.

Cost: a few seconds in `make test`. Recording a new release costs one e2e
run. Maintenance: a new release adds files to `testdata/`. The size of the
corpus was not measured.

### Layer D. Behavior at run time on an unknown kind or field

The CLI has no fallback after a journal error (research note in #390,
section 6). So the choice at run time decides what the user sees.

| | Strict reject | Pass-through | Version gate |
| --- | --- | --- | --- |
| What the server does | answers 400 to an entry with an unknown kind, version or field (R14) | ignores what it does not know and applies the rest | answers `journalVersion` 0 at start to a CLI newer than the newest verified version |
| What the user sees | `pulumi up` fails at once, no retry (research note in #390, section 6) | `pulumi up` succeeds | `pulumi up` succeeds, with checkpoints |
| State | what the accepted batches built, with pending operations (R34) | can be wrong with no error: kinds 8 and 9 and `requiresByteString` show this (research note in #390, section 3) | correct, through the checkpoint path (R3) |
| Cost | none in code; a failed update for each user of a new CLI that sends something new | silent corruption | the server must know the CLI version; journaling speed is lost for new CLIs until a binary with a newer verified version ships |

Pass-through is not acceptable. Kinds 8 and 9 arrived under version 1 and
an older reader that ignores them loses data.

**Does the CLI send its version?** Yes, in `User-Agent`:

- `v3.268.0`: `UserAgent()` returns
  `pulumi-cli/1 (<version>; <GOOS>[; cmd=...][; agent=...])`
  (`pkg/backend/httpstate/client/api.go@v3.268.0:126-137`). The client sets
  it on each request (`api.go@v3.268.0:424`). Since #21517 (v3.217.0) the
  client also sets it on requests that are not Pulumi API requests.
- `v3.250.0`: the same format (`api.go@v3.250.0:124-135`), set at
  `api.go@v3.250.0:419`.
- `v3.200.0` and `v3.204.0` had the form `pulumi-cli/1 (<version>; <GOOS>)`
  with the comment "Add a User-Agent header to allow for the backend to make
  breaking API changes while preserving backwards compatibility"
  (`api.go@v3.200.0:280-282`).
- The version is `version.Version`, set by the linker
  (`sdk/go/common/version/version.go@v3.268.0`, `.goreleaser.yml` line 21).
  A release has the form `v3.268.0`. A build from source with the upstream
  Makefile gets `<next>-dev.0` (`scripts/pulumi-version.sh`). A plain
  `go build` without the flag gives an empty version:
  `pulumi-cli/1 (; linux)`.
- The `Accept` header (`application/vnd.pulumi+9`, `api.go@v3.268.0:57-90`)
  carries an API version, but it does not track the journal: it was 9 from
  v3.233 on.

**Is it reliable?** Not as a security control. A proxy can rewrite
`User-Agent`. A client that is not the CLI can send any value. The
Automation API runs the CLI binary, so it sends the CLI value. For a gate
that falls back to checkpoints, these weak points are safe:

- unknown or empty version: answer 0 (checkpoints);
- a version above the newest verified one: answer 0;
- a client that claims an old version but sends a new kind: strict reject
  catches it, with a failed update.

**Proposal for layer D:** the version gate plus strict reject. The gate
keeps users of a new CLI on checkpoints. Strict reject stops a client that
the gate let through but that sends something unknown.

### The cost of strict reject

Strict reject has a cost that this RFC states plainly. The CLI has no
fallback to checkpoints, and it does not retry a 400 or a 409 (research
note in #390, section 6). So when a CLI that the gate let through sends an
entry that the server does not know, the user's `pulumi up` fails at once.
The state holds what the accepted batches built, with pending operations
for the steps that did not finish (R34). With real cloud resources, a
failed update can leave resources that exist but are not in the state
(research note in #390, section 6). The next `pulumi refresh` or `pulumi up`
must repair that.

This can happen to a verified CLI too. A verified version can send
something that the corpus did not hold: a kind that only some programs
make (kinds 8 and 9 come only with extension parameterization or snippets,
and `requiresByteString` only with strings that are not UTF-8; research
note in #390, section 3), or a combination that no recorded program had.

Three layers make it rare:

- **The version gate (D)** keeps every CLI that is newer than the newest
  verified version on checkpoints. A new release cannot reach strict
  reject until a person has moved the pin and the verified version (section
  2, option 2C).
- **The nightly run (A)** drives each new release with journaling on and
  off, the night after the release, before a person verifies it.
- **The corpus (C)** holds each kind, each flag and each case that RFC 62
  names, for each verified release. A program that uses a rare feature must
  be in the corpus before its release is verified.

They do not make it impossible. The corpus holds only the programs that
the project recorded.

**What an operator does when it happens.**

1. Set `DUSTPAN_DISABLE_PULUMI_JOURNALING=true` (or the flag) and restart
   the server. From then on, each new update gets `journalVersion` 0 and
   saves checkpoints, with the same CLI.
2. The update that failed has already ended: the CLI sent `complete` with
   `failed` (research note in #390, section 6). The user runs `pulumi up`
   again. That update uses checkpoints.
3. An update that is running in journal mode at the moment of the restart
   stays in journal mode, because the CLI chose its persister at start
   (section 4). The server keeps its negotiated version on the update row,
   and accepts its batches after the restart. During the restart, the CLI
   gets a transport error on the batch in flight. The CLI retries a
   transport error up to 4 attempts, 1 s, 2 s and 4 s apart (research note
   in #390, section 5.6, from the source; this case was not measured). A
   restart that takes more than about 7 s can therefore fail that update
   too. The user then runs `pulumi up` again, with checkpoints.
4. The operator reports the failing program and CLI version in this
   repository, so that a person can add it to the corpus and fix the
   replay.

### How a stuck update ends (R37)

R37 in the research note in #390 says: a `complete` with `succeeded` on an
update with a refused batch or a sequence gap must not end as `succeeded`.
The CLI can send `succeeded` after a refused batch when that batch held
only elided entries: it ignores that refusal and exits 0 (R37, section 5.8).

The research note measured what a refused `complete` does (section 6,
"Results: errors on the `complete` call"): the CLI exits 1, the update
stays `in-progress`, and the next `pulumi up` fails with 409 "another
update of this stack is in progress". Open question 19 of the note asks if
`pulumi cancel` or a lease expiry then ends the update. No run tested it.

In this project, ADR 20 already gives the answer for the lease part:

- An expired lease does not free the stack. Only `pulumi cancel` does
  (ADR 20, decision 1).
- Anyone with the right to update the stack may cancel, and the cancel
  route needs no lease (ADR 20, decision 2). The CLI finds the update
  through `activeUpdate` in the stack response (ADR 20, decision 4).

So for an update with a refused batch or a gap, the server has two choices
on `complete` with `succeeded`:

| | Refuse `complete` (409) | Accept `complete`, record `failed` |
| --- | --- | --- |
| CLI | exits 1 with the server message | exits 0. The user sees success |
| Update | stays `in-progress`, the stack stays locked | ends as `failed`, the stack is free |
| Way out | `pulumi cancel`, by anyone with the update right | none needed |
| State | what the accepted batches built (R34) | the same |

This RFC prefers the refusal: it tells the user that something failed. The
server must then make sure that the update does end:

- The 409 message names the one command that helps: `pulumi cancel`.
- The cancel route ends an update in journal mode like any other update,
  and builds its checkpoint from the stored entries first (R33, R34).
- An e2e step proves it: a refused batch of elided entries, then
  `complete` with `succeeded` refused, then `pulumi cancel`, then a
  `pulumi up` that succeeds. This step answers open question 19 of the
  research note for this server.
- RFC 18 may add a force release for an operator. Until then,
  `pulumi cancel` is the only way out, as for any update after ADR 20.

### Which layers act before a user meets a break

| Layer | When it fires | Before users? |
| --- | --- | --- |
| B on `master` | at the upstream commit | yes, days to weeks before a release |
| A with a master CLI | the night after the commit | yes |
| B on tags, A with the newest release | up to one day after a release | no, but D protects users in that time |
| C | on each pull request in this project | yes, for changes in our replay code; for a new CLI, only after the release is recorded |
| D | at each start-update | yes: an unverified CLI never journals |

D keeps every user of an unverified CLI on checkpoints. For a verified
CLI, A and C make a failed update rare, but not impossible (see "The cost
of strict reject"). A, B and C also make the time short until a binary can
verify a new CLI.

## 4. The configuration option

`internal/cli/settings.go` declares each setting once, with flag, variable,
default, help and reference text. `boolSetting` always has the default
`false` ("no opt-in without an explicit flag or variable"). A setting that
is on by default is therefore an opt-out bool:

```go
boolSetting(
    "disable-pulumi-journaling", "DUSTPAN_DISABLE_PULUMI_JOURNALING",
    "answer journalVersion 0 at the start of a Pulumi update, so the CLI saves checkpoints "+
        "(DUSTPAN_DISABLE_PULUMI_JOURNALING=true)",
    "(none: journaling on). Set `true` to make the Pulumi CLI save checkpoints",
),
```

The name follows the CLI's own `PULUMI_DISABLE_JOURNALING`. An option that
was considered: `intSetting("pulumi-journal-version", ..., "1", ...)` with
`0` for off, as `checkpoint-delta-cutoff` does. It also lets an operator
hold version 1 when version 2 ships. It is a second meaning in one value, so
this RFC prefers the bool and leaves the version to the gate.

**When the option is on (`true`):** start-update and `begin-update`
answer `journalVersion` 0. The CLI sends checkpoints (R2, R3), with delta
uploads when the server advertises them. The journal route stays.

**When an operator changes the option and restarts:**

- The CLI chooses its persister once, from the answer to start-update
  (`backend.go@v3.268.0:1871`, `1934`, `2136-2141`). No code path in the
  CLI changes it during the update. So a CLI that started journaled sends
  journal batches until it ends, also after the restart.
- The server must store the negotiated version on the update row at start,
  in the fenced transaction that starts the update. A restart must not lose
  it. The spike keeps sessions in memory only (RFC 62, question 4), so a
  restart there loses them.
- The server must accept journal batches for an update whose row says that
  it journals, whatever the option says now. The option only changes the
  answer to new start-update requests. If the server refused, the CLI would
  get a 4xx, fail at once with no retry, and the user would lose the update
  (research note in #390, section 6).
- The server must keep the replay code in each binary, with the option on or
  off. A journaled update that crashed must still build its checkpoint on
  the next read (R33, R34).
- A downgrade to a binary without the journal route answers 404 to a batch.
  The CLI does not retry a 404 (4xx other than 429). The upgrade check
  (ADR 33) can warn when an update is running in journal mode.

## 5. Conflicts with accepted ADRs

This RFC changes no ADR. A new ADR must decide:

- **ADR 37, decision 9** ("`StartUpdate` answers `journalVersion` 0"): it
  must change. The new rule: the server answers 1 (or 2) by default, 0 when
  the option is set, 0 for a CLI above the verified version, and 0 for a
  preview.
- **ADR 37, decision 4** (exact bytes): for a journaled update, the server
  builds the deployment bytes from the entries. RFC 62 (#350, under review),
  question 6, reaches the same result. Checkpoint and delta saves keep
  exact bytes.
- **ADR 37, decision 8** (the server does not read state): ADR 41 allows
  reads, but its list "What this ADR replaces" does not name ADR 37,
  decision 8. The new ADR must name it.
- **ADR 37, decisions 2, 3 and 11**: the key shape and the order of writes
  for journal blobs, and their place in backups and in the orphan cleanup.
- **ADR 20, decision 5** ("The server advertises no delta capability and
  no journaling"): ADR 37 replaced the delta part. The new ADR replaces the
  journaling part.

The new ADR must also decide:

1. The option name and its default (section 4).
2. The answer: version 1 only, or version 2 too. Version 2 needs state
   migration replay first (R29; RFC 62, question 5).
3. The module boundary (section 1).
4. The wire types (2A, 2B or 2C) and the decoder, from the JSON libraries
   research note.
5. The replay source: a port with attribution, with the header rule for
   ported files.
6. Storage: entries as blobs, when the server builds the checkpoint, and
   the limits for one update.
7. The answer to `complete` with `succeeded` after a refused batch or a
   sequence gap (R37): refuse with 409 and rely on `pulumi cancel`, or
   accept and record `failed`.
8. Order and retries: apply by sequence ID or by arrival (R16), how to
   find a retried batch (R12), what to do with batches after a refused one
   (R35).
9. The version gate: where the newest verified version lives, and who
   raises it.
10. The CI layers A to C, and who reads the issues of layer B.
11. Whether ADR 7's two-week rule applies to a module of this project
    (only for 1A) and to `sdk/v3` (only for 2A).

## Options

| Option | What | Cost | Gives |
| --- | --- | --- | --- |
| Do nothing | keep ADR 37, decision 9 | none | no journaling. Against the premise |
| 1A + 2A | separate module that imports `sdk/v3` | 164 modules, 6 vulnerabilities from govulncheck v1.8.0 on 2026-10-12 (4 called, 1 imported, 1 required), generic trees, 5 targets and 6 CI steps more, 3 steps for each change | types that follow upstream with a version bump |
| 1A + 2B or 2C | separate module, own or generated types, ported replay | 5 targets and 6 CI steps more, owner tags, maybe 2 weeks for each fix | embedders can bump the journal code alone, if its API stays stable |
| 1B + 2B | `internal/pulumijournal`, own types, ported replay | about 230 lines of types by hand, about 970 lines of port | no new dependency, one pull request for each change |
| 1B + 2C | as 1B + 2B, with generated entry types | a generator of about 200 lines and a diff check | an upstream change shows as a diff in a generated file |

Each option except "do nothing" also needs: the setting, the version gate,
strict reject, stored entries with a streaming build of the checkpoint, and
CI layers A (newest release), B and C. Layer A with a master CLI is
optional, at about 150 runner minutes each month.

## Proposal

1B + 2C, with `encoding/json/v2` and `encoding/json/jsontext` as the
decoder (from the facts of the JSON libraries research note): a package
`internal/pulumijournal` in the main module, entry types generated from the
pinned upstream `apitype/journal.go`, resource states kept as raw JSON, and
a port of the replayer with attribution. Add the opt-out setting
`--disable-pulumi-journaling`, the version gate on `User-Agent` with strict
reject, entries stored as write-once blobs with one streaming build of the
checkpoint, and CI layers A (newest release, nightly), B and C.

Reasons:

- 1A, the owner's suggestion, makes the instability visible in the path
  and the version, lets embedders upgrade it alone, and keeps unstable wire
  types out of the main module. Today no embedder exists, `internal/`
  already keeps the types out of the public API, and 1A costs 5 `make
  check` targets, 6 CI steps and an owner tag for each journal change. A
  depguard rule keeps the package free of imports of the rest of the
  module, so a move to 1A later is a move of files. The triggers for that
  move are in section 1: the first SDK embedder that needs journaling, a
  need to ship journal fixes faster than server releases, or a public API
  that must expose journal types.
- `sdk/v3` brings 164 modules and builds generic trees, which #389 forbids.
- Generated types make an upstream change visible as a diff, with no new
  dependency.
- The version gate is the only layer that keeps every user of a new CLI
  safe, because the CLI has no fallback. Strict reject still fails the
  update of a verified CLI that sends something unexpected. The nightly run
  and the corpus make that rare, and the disable setting with a restart is
  the operator's way out (section 3).
- On `complete` with `succeeded` after a refused batch, the server answers
  409 with a message that names `pulumi cancel`, and the cancel route ends
  the update (R37, ADR 20).

This is a recommendation. Decision: for the owner.

## Open questions

1. Does ADR 7's two-week rule apply to a module of this project? It decides
   the cost of 1A.
2. Where does the newest verified CLI version live: a constant in the
   binary, or a value that an operator can set? A value adds a second
   setting, which the premise does not ask for. With a constant, an
   operator who wants journaling with a newer CLI must upgrade the server.
   Until then, that CLI saves checkpoints: it works, but without the speed
   of journaling. The owner weighs that cost against a second setting.
3. Does the server answer version 2 from the start, with state migration
   replay (R29), or version 1 only? R5 of the research note in #390 says: a
   CLI of v3.264.0 or later offers 2 and accepts 1. When the server answers
   1, the CLI journals with version 1 and sends no kind 10. Its resource
   monitor then does not advertise the state-migration feature, and it
   refuses a resource registration that carries state migrations with
   "state migrations are not supported by this deployment" (research note
   in #390, section 4, v3.268.0). With full checkpoints, or with version 2,
   migrations are allowed. So a program that does not use state migrations
   sees no difference. A program whose SDK sends a migration anyway gets an
   error on that registration, which in the usual case fails the update. No
   run measured this case.
4. When does the server build the checkpoint for a running update that a
   reader asks for: on each read, or once and then kept until the next
   batch?
5. Entries as blobs or as SQL rows? Blobs follow ADR 37. Rows are small
   (about 1.2 KB for each batch at 3,000 resources) and need no sweep.
6. What does the CLI do after more than 20 minutes on a held journal
   request (research note in #390, open question 15)? It sets the timeout
   for a stream build of a large checkpoint at `complete`.
7. The measured memory of the streaming build at 3,000 and 10,000
   resources. This RFC did not measure it.
8. Does the owner accept `encoding/json/v2` and `jsontext` as the decoder?
   The JSON libraries research note (`docs/research/json-libraries.md`)
   measured it at -40% against `encoding/json` on the typed checkpoint
   decode, with no dependency. goccy/go-json and sonic were faster
   (-75% and -73%), but each needs a new dependency, which only the owner
   can accept.

## Outcome

Accepted on 2026-10-12: option 1B with option 2C, as proposed. The open
questions are decided in [ADR 43](../adr/0043-pulumi-journaling-support.md).
