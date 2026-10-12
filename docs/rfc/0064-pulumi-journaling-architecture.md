---
rfc: 64
title: Architecture of Pulumi journaling support
status: draft
created: 2026-10-12
---

# RFC 64. Architecture of Pulumi journaling support

Status: draft.

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
  requirements R1 to R36 (section 7).
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

**What a binary user gains.** Nothing. The binary links one version of the
module. A fix in the journal module reaches an operator only in a new
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

## 2. No hand-written parser

### Wire types: three options

The JSON library question has its own research note:
`docs/research/json-libraries.md`, written on the branch
`swarm/pulumi-json-research`. That note holds the benchmarks, the
correctness and denial-of-service checks, and the provenance of third-party
JSON libraries and code generators (easyjson, goccy/go-json, sonic,
json-iterator, segmentio, go-json-experiment). This RFC takes its choice of
decoder from that note. Headline result of the JSON libraries research note:
_to be filled in when that note is done_.

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
| `govulncheck` (v1.8.0) | 4 called vulnerabilities in `golang.org/x/net@v0.59.0` (GO-2026-6617, GO-2026-6612, GO-2026-6611, GO-2026-6603, fixed in v0.60.0), 1 more in an imported package (GO-2026-6610), 1 in a required module (GO-2026-5932, `golang.org/x/crypto/openpgp`, no fix) | `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...` |

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
- This RFC found no text in the toolchain that puts v2 under the Go 1
  compatibility promise. The build tag suggests that it is still an
  experiment. An API change in a later Go release could break code that
  uses it.

`jsontext.Decoder` can read a token, or read one value as raw bytes
(`ReadValue`), with no tree. That is the streaming tool that #389 asks for,
in the standard library. `encoding/json` v1 `Decoder.Token()` also streams,
with more allocations (the envelope spike measured stdv2 at 19,043
allocations for 1,000 resources against 31 for a v1 decode into
`json.RawMessage`; `spike/json-libs.md`). The JSON libraries research note
decides between them.

Cost: about 230 lines of types to keep in step with
`sdk/go/common/apitype/journal.go@v3.268.0` (230 lines), by hand, at each
CLI change that touches the entry.

#### Option 2C. Types generated from the upstream source

A small generator in this repository reads
`sdk/go/common/apitype/journal.go` at a pinned upstream commit with
`go/parser`, and writes Go types for `JournalEntry`, `JournalEntries` and
the kind constants. Rules:

- Fields of types from `core.go` (`ResourceV3`, `OperationV2`,
  `DeploymentV3`, `SecretsProvidersV1`, `SnippetV1`) become raw JSON, so the
  output has no generic tree and imports nothing from Pulumi.
- The input is pinned by commit and by the SHA-256 of the file. The
  generator reads the file from the Go module zip of `sdk/v3` at that
  version, which `go.sum`-style checks can verify, or from a vendored copy
  in `testdata/` with its hash.
- `make generate` runs it. `make lint` runs it again and fails on a
  difference, as `sqlc diff` does today.
- The generated file carries the Apache-2.0 header of the input file and a
  line that names the commit, because it is a derived work of that file.

Cost: about 200 lines of generator, plus the cost of 2B for the types that
the generator cannot know (the link fields of a resource). Gain: a CLI
change in `journal.go` shows as a diff in a generated file, which the
upstream watch (section 3, layer B) can open as an issue in this
repository, with the diff.

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

### Which layers act before a user meets a break

| Layer | When it fires | Before users? |
| --- | --- | --- |
| B on `master` | at the upstream commit | yes, days to weeks before a release |
| A with a master CLI | the night after the commit | yes |
| B on tags, A with the newest release | up to one day after a release | no, but D protects users in that time |
| C | on each pull request in this project | yes, for changes in our replay code; for a new CLI, only after the release is recorded |
| D | at each start-update | yes: an unverified CLI never journals |

Only D keeps every user safe. A, B and C make the time short until a
binary can verify a new CLI.

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
  for journal blobs, and their place in backups and in the orphan sweep.
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
7. Order and retries: apply by sequence ID or by arrival (R16), how to
   find a retried batch (R12), what to do with batches after a refused one
   (R35).
8. The version gate: where the newest verified version lives, and who
   raises it.
9. The CI layers A to C, and who reads the issues of layer B.
10. Whether ADR 7's two-week rule applies to a module of this project
    (only for 1A) and to `sdk/v3` (only for 2A).

## Options

| Option | What | Cost | Gives |
| --- | --- | --- | --- |
| Do nothing | keep ADR 37, decision 9 | none | no journaling. Against the premise |
| 1A + 2A | separate module that imports `sdk/v3` | 164 modules, 6 known vulnerabilities, generic trees, 5 targets and 6 CI steps more, 3 steps for each change | types that follow upstream with a version bump |
| 1A + 2B or 2C | separate module, own or generated types, ported replay | 5 targets and 6 CI steps more, owner tags, maybe 2 weeks for each fix | embedders can bump the journal code alone, if its API stays stable |
| 1B + 2B | `internal/pulumijournal`, own types, ported replay | about 230 lines of types by hand, about 970 lines of port | no new dependency, one pull request for each change |
| 1B + 2C | as 1B + 2B, with generated entry types | a generator of about 200 lines and a diff check | an upstream change shows as a diff in a generated file |

Each option except "do nothing" also needs: the setting, the version gate,
strict reject, stored entries with a streaming build of the checkpoint, and
CI layers A (newest release), B and C. Layer A with a master CLI is
optional, at about 150 runner minutes each month.

## Proposal

1B + 2C, with the decoder from the JSON libraries research note: a package
`internal/pulumijournal` in the main module, entry types generated from the
pinned upstream `apitype/journal.go`, resource states kept as raw JSON, and
a port of the replayer with attribution. Add the opt-out setting
`--disable-pulumi-journaling`, the version gate on `User-Agent` with strict
reject, entries stored as write-once blobs with one streaming build of the
checkpoint, and CI layers A (newest release, nightly), B and C.

Reasons:

- A separate module gives a binary user nothing, and no embedder exists.
  It costs 5 `make check` targets, 6 CI steps and an owner tag for each
  journal change. A depguard rule keeps the package free of imports of the
  rest of the module, so a move to 1A later is a move of files.
- `sdk/v3` brings 164 modules and builds generic trees, which #389 forbids.
- Generated types make an upstream change visible as a diff, with no new
  dependency.
- The version gate is the only layer that keeps every user of a new CLI
  safe, because the CLI has no fallback.

Decision: for the owner.

## Open questions

1. Does ADR 7's two-week rule apply to a module of this project? It decides
   the cost of 1A.
2. Where does the newest verified CLI version live: a constant in the
   binary, or a value that an operator can set? A value adds a second
   setting, which the premise does not ask for.
3. Does the server answer version 2 from the start, with state migration
   replay (R29), or version 1 only? With version 1, a CLI of v3.264.0 or
   later refuses state migrations (R5).
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
8. The headline result of the JSON libraries research note
   (`docs/research/json-libraries.md`), and which decoder it picks.

## Outcome

Empty until the RFC is accepted or dropped.
