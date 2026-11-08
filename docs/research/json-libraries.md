# Research: Go JSON libraries for Pulumi journal entries, checkpoints and OpenTofu state

Question: Which Go JSON libraries can decode and encode Pulumi journal
entries, Pulumi checkpoints and OpenTofu state, how fast are they on our
payloads, how do they differ from `encoding/json`, and what does each one
cost in security, provenance and maintenance?

Date: 2026-10-12

This note gives facts. It does not choose a library. RFC 64 holds the
recommendation, and the project owner decides.

## 1. Summary

Measured on one 4-core amd64 machine with Go 1.27.2, in 10 interleaved
passes on a quiet machine (1-minute load average 0.24 to 1.38). A change is reported
only when benchstat gives p < 0.05.

- In Go 1.27.2, `encoding/json/v2` and `encoding/json/jsontext` are on by
  default, and `encoding/json` runs on the v2 code.
  `github.com/go-json-experiment/json` is an alias of the standard library
  on Go 1.27.
- Typed decode of a checkpoint with 1,000 resources: `std` 29.88 ms,
  `stdv2` 17.90 ms (-40%), `easyjson` 12.46 ms (-58%), `sonic` 7.93 ms
  (-73%), `goccy` 7.56 ms (-75%), `segmentio` 38.66 ms (+29%). The order is
  the same for journal batches and OpenTofu state.
- The decode `main` does today (raw `deployment` bytes): `std` 5.75 ms at
  1,000 resources. `goccy` and `sonic` take about 60% less time and
  allocate twice the bytes.
- The #389 lookup: a `jsontext.Decoder` walk takes 44 to 51 ms on a 19 to
  20 MB document and allocates about 14 KiB. The `json.Decoder.Token` walk that
  #389 proposes takes 101 to 131 ms and allocates 22 to 34 MiB. The full
  decode of today takes 296 to 352 ms and 63 to 87 MiB.
- Differences from `encoding/json` on 54 cases: `goccy` 0, `segmentio` 2,
  `jsoniter` 3, `sonicstd` 3, `easyjson` 7, `sonic` 9, `stdv2` 11 (its
  defaults). `easyjson` and `sonic` accept invalid JSON in skipped members.
  `sonic` accepts a truncated top-level string and loses a byte.
- Hostile input: `segmentio` has no depth limit and its time grows about 4x
  per 2x of depth (80 KB: 42 s). Every library allocates 31 to 108 times
  the input size for one million empty arrays or objects in an interface
  value.
- Provenance: no known advisory for any candidate (OSV, Go vulnerability
  database). For all 7 modules, the go.sum hash, the sumdb hash and a hash
  rebuilt from the git tag are equal. `json-iterator/go` is archived.
  `goccy` has one main author. `sonic` builds its native code only below
  Go 1.28.
- Licenses: all permissive and compatible with Apache-2.0 (ADR 22).
- Only the standard library (`std`, `stdv2`) needs no new dependency.


## 2. Context and scope

### What `main` does today

At `main` commit `25586f72d307c9e655a90f6bd879c63120274322`:

- Every JSON path uses `encoding/json`. `go.mod` has no JSON library.
- A Pulumi checkpoint body decodes into a struct whose `deployment` field
  is `json.RawMessage` (`internal/api/pulumi/updates.go`). The resource count
  walks the deployment with `json.Decoder.Token` (`pkg/stack/resources.go`).
- The journal route exists on the spike branch `spike/pulumi-journaling`
  only. Its `pulumijournal.Entry` keeps `state`, `operation` and other
  large members as `json.RawMessage`.
- OpenTofu state decodes into an interface value with `UseNumber`
  (`pkg/workspace/tofu_state.go`, `decodeState`).
- Issue #389: the reveal call reads the whole state and builds a tree to
  find one value. The issue proposes a token walk instead.

### Owner rules that apply

- `CLAUDE.md`: prefer the standard library. Ask before you add a
  dependency.
- The owner's direction: no hand-written parser.
- ADR 7: pin dependencies exactly. Adopt a version only when it is at
  least two weeks old.
- ADR 22: the project is Apache-2.0. A dependency must allow that.
  `make trivy` fails on a forbidden, restricted or unknown license.
- Release platforms (`Makefile`, `RELEASE_PLATFORMS`): linux/amd64,
  linux/arm64, darwin/amd64, darwin/arm64, windows/amd64.

### What the earlier spike settled

The spike on `origin/spike/pulumi-json-libs` (commit
`72895df7bc15db78e6604ea9380b50161caa4928`, note `spike/json-libs.md`)
measured the checkpoint save path at 1,000 and 3,000 resources. Its
numbers, one sample each, are not repeated here as new measurements:

| impl (1,000 resources) | ns/op | B/op | allocs/op | invalid inputs accepted |
| --- | --- | --- | --- | --- |
| custom parser (branch) | 7,632,520 | 489 | 6 | 0 |
| `encoding/json` (old main decode) | 18,635,071 | 14,060,527 | 31 | 0 |
| `encoding/json/v2` | 17,395,809 | 15,834,434 | 19,043 | 0 |
| buger/jsonparser | 18,259,581 | 2,852,364 | 1,006 | 0 |
| valyala/fastjson | 9,591,563 | 2,956,812 | 285 | 2 |
| tidwall/gjson, one parse | 8,326,989 | 8,506,590 | 13 | 16 |

What it settled:

- No library it tested was both faster than the custom parser and correct
  on its corpus, once the cost of a full syntax check counts.
- gjson and fastjson accept invalid JSON. A `gjson.ValidBytes` pass closes
  the gap but removes the speed lead.
- jsonparser, gjson and fastjson import `unsafe`. fastjson forbids a
  shared `Parser` across goroutines.

What it did not cover, and this note adds:

- Typed decoding of whole payloads, encoding, journal batches, OpenTofu
  state, the #389 lookup, and payloads of 10 and 10,000 resources.
- The candidates of this note: code generators, goccy/go-json, sonic,
  json-iterator, segmentio/encoding and the token decoders.
- Repeated samples with a significance test, fuzzing, hostile input,
  provenance and maintenance cost.

The spike's custom parser is out of scope here, because the owner's
direction is no hand-written parser. jsonparser, gjson and fastjson are
not measured again.

## 3. Method

### Machine and versions

- CPU: `Intel(R) Xeon(R) Processor @ 2.10GHz` (`/proc/cpuinfo`), 4 cores
  as `nproc` reports them, AVX2 and AVX-512F present. 15 GiB RAM. Linux
  6.18.44. A shared virtual machine.
- Go: go1.27.2 linux/amd64 (`GOTOOLCHAIN=go1.27.2`).
- Benchmark code: branch `spike/json-libs-2` (local only, not pushed),
  directory `bench/`. Code at commit
  `18645112946ff5aced563dda1606df6640fd56f9`. Raw results in
  `bench/results/` at commit `464f0bcf5170afebbd294a113e6bb3a13bdecd61`.
  `bench/` is a separate Go module (`jsonbench`), so no dependency touches
  `main` or this note's branch.
- Payload shapes: pulumi/pulumi at
  `2255563bcc6bd347d1d585120b919b8a30e42d52` (`sdk/go/common/apitype`).
- benchstat: `golang.org/x/perf/cmd/benchstat` at
  v0.0.0-20261009192801-be2c69fb417e (`@latest` on 2026-10-12, in the
  throwaway module only).

### Payloads

The generator is `bench/gen.go`. It is new. The earlier spike's
`benchDeployment` repeats one 1,200-byte padding string in every resource,
and the e2e journal program on `spike/pulumi-journaling` needs real CLI
runs (more than 15 minutes for 10,000 resources, by its own comment). The
new generator builds typed values in the shapes of the Pulumi CLI v3.268.0
and OpenTofu 1.10, and marshals them with `encoding/json` as both tools do:

- Pulumi resources rotate through six shapes: an S3 bucket, an IAM role
  with its policy document as an escaped JSON string, an EC2 instance with
  base64 user data, a random password with two secret objects
  (`4dabf18193072939515e22adb298388d`), a Kubernetes Deployment with nested
  objects several levels deep and a `last-applied-configuration` JSON string, and a
  component. Values hold non-ASCII text, integers, floats, booleans and
  nulls.
- A checkpoint is the `PATCH .../checkpoint` body: `{"version":3,
  "deployment":{...}}` with a manifest, a secrets provider and n resources.
  Compact, as the CLI sends it.
- A journal batch is the `PATCH .../journalentries` body: n entries that
  alternate a begin entry (kind 0, operation `creating` with inputs) and a
  success entry (kind 1, full state). The CLI sends at most 100 entries per
  request.
- An OpenTofu state is format version 4 with n resources in six shapes
  (`aws_instance`, `aws_iam_role`, `random_password` with
  `sensitive_attributes`, `aws_s3_bucket`, `aws_db_instance`, a data source),
  `private` blobs, module addresses, and three `count` instances on every
  tenth resource. Indented with two spaces, as OpenTofu writes it.

| Payload | Bytes | SHA-256 (first 16 hex) |
| --- | --- | --- |
| journal-10 | 14,744 | d86f2716da19584b |
| journal-100 | 146,375 | d4e24ce10c240314 |
| ckpt-10 | 17,564 | 1cd281f2fba85fa3 |
| ckpt-1000 | 1,889,838 | 23eddff60ca38641 |
| ckpt-10000 | 18,982,547 | 5645ca28bd87e986 |
| tofu-10 | 22,843 | 89b55428eaa3cc1f |
| tofu-1000 | 2,028,128 | 9b3e9c9c00c7bbe5 |
| tofu-10000 | 20,290,277 | 8d922c7801ef83c5 |

`envelope-N` rows decode the `ckpt-N` bytes into the struct that `main`
uses today: `version`, `features` and `deployment` as `json.RawMessage`.
`tofu-N-any` rows decode into an interface value, as `main` does today
(without `UseNumber`, so that the call is the same for every library).
The checkpoint at 10,000 resources (18.1 MiB) is below the 32 MiB checkpoint
limit.

Before the benchmarks, `TestPayloads` checks that every library decodes
every payload to a value equal to the `encoding/json` result. One
difference showed: segmentio decodes `"bucket":null` inside
`propertyDependencies` (a `map[string][]string`) to an empty slice, not
nil (section 7).

### Benchmark protocol

- The project lead paused the other agents and loops for the final passes.
  Setup, fuzzing, the probes and a trial pass ran before that, with other
  jobs on the machine. The trial pass numbers are not used.
- Two test binaries: `bench.test` (Go 1.27.2 defaults) and
  `bench-legacy.test` (`GOEXPERIMENT=nojsonv2`, only `std` rows, renamed
  `stdlegacy`). Both were built from the code of the spike commit above.
  `gofmt -w` ran after the build and changed only formatting.
- Interleaving (`bench/quiet.sh`): one pass runs each of the 17 candidates
  once, `-count 1 -benchmem`, default `-benchtime` (1 s). The order rotates
  by one position per pass. 10 passes give 10 samples per benchmark. A
  slow drift of the machine then affects every candidate in about the same
  way.
- benchstat `-col /lib` compares each column with `std`. This note
  reports a change only when benchstat gives p < 0.05. Otherwise the cell
  says "~", which means no significant difference.
- Load average (`/proc/loadavg`) before and after each pass:

| Pass | Time before (UTC) | 1-min load before | 1-min load after | 5-min load after |
| --- | --- | --- | --- | --- |
| 1 | 12:31:00 | 0.24 | 1.06 | 1.26 |
| 2 | 12:36:32 | 1.06 | 1.11 | 1.19 |
| 3 | 12:42:01 | 1.11 | 1.22 | 1.21 |
| 4 | 12:47:34 | 1.22 | 1.12 | 1.10 |
| 5 | 12:53:07 | 1.12 | 1.10 | 1.08 |
| 6 | 12:58:40 | 1.10 | 1.38 | 1.15 |
| 7 | 13:04:12 | 1.38 | 1.02 | 1.12 |
| 8 | 13:09:45 | 1.02 | 1.22 | 1.18 |
| 9 | 13:15:15 | 1.22 | 1.05 | 1.17 |
| 10 | 13:20:48 | 1.05 | 1.02 | 1.09 |

No pass had a load average above 1.5. The benchmark process itself keeps
about one core busy, so a load near 1.0 is the floor for this run. The
15-minute average before pass 1 was 2.90, from the work before the pause.

The A/A control (`stdv2` against `gjexp`, the same code) gave p < 0.05 in
1 of 22 time rows (decode tofu-10, -6.92%, p=0.043). That is near the 5%
false-positive rate the test allows. The benchstat confidence intervals
of single rows reach plus or minus 4% to 26%. Read a change under about
10% with that in mind.



## 4. Candidates

Versions follow ADR 7: each tested version was at least 14 days old on
2026-10-12. Dates are the module proxy `Time` (`go list -m -json
MODULE@VERSION`).

| Name in tables | Module and version | Release date | Kind |
| --- | --- | --- | --- |
| `std` | `encoding/json`, Go 1.27.2 | 2026-10-02 (toolchain) | reflection |
| `stdlegacy` | `encoding/json` built with `GOEXPERIMENT=nojsonv2` | same | reflection, the pre-v2 code |
| `stdv2` | `encoding/json/v2` and `encoding/json/jsontext`, Go 1.27.2 | same | reflection |
| `gjexp` | `github.com/go-json-experiment/json` v0.0.0-20260820222146-c27c302e5fc3 | 2026-08-20 | the out-of-tree copy of v2 |
| `easyjson` | `github.com/mailru/easyjson` v0.9.2 | 2026-03-14 | code generator |
| `goccy` | `github.com/goccy/go-json` v0.11.1 | 2026-09-27 | reflection, `unsafe`, assembly |
| `sonic`, `sonicstd` | `github.com/bytedance/sonic` v1.15.4 (`ConfigDefault`, `ConfigStd`) | 2026-09-10 | JIT and SIMD on amd64 |
| `jsoniter` | `github.com/json-iterator/go` v1.1.12 (`ConfigCompatibleWithStandardLibrary`) | 2021-09-11 | reflection, `unsafe` |
| `segmentio` | `github.com/segmentio/encoding` v0.5.4 (`json` package) | 2026-03-10 | reflection, `unsafe`, assembly |

Facts about the standard library in Go 1.27.2:

- `encoding/json/v2` and `encoding/json/jsontext` need no
  `GOEXPERIMENT`. `internal/buildcfg/exp.go` sets `JSONv2: true` in the
  baseline. The go-json-experiment README says v2 became "a stable API in
  Go 1.27".
- With that baseline, `encoding/json` is built on v2. `Unmarshal` calls
  `jsonv2.Unmarshal(data, v, DefaultOptionsV1())`
  (`src/encoding/json/v2_decode.go:111`). The pre-v2 code is still in the
  tree behind `!goexperiment.jsonv2`. The `stdlegacy` rows measure it.
- `github.com/go-json-experiment/json` has `alias.go` with
  `//go:build goexperiment.jsonv2 && go1.25`. On Go 1.27 it aliases the
  standard library. So `gjexp` and `stdv2` run the same code. Their rows
  are an A/A control for noise.

goccy v0.11.2 (2026-09-30) was 12 days old, so this note tests v0.11.1.
v0.11.0, v0.11.1 and v0.11.2 came out within 3 days. `git log
v0.10.6..v0.11.2` has 60 commits, most of them decoder and encoder
rewrites.

### Searched and not measured

| Module | Last release or commit | Reason |
| --- | --- | --- |
| `github.com/pquerna/ffjson` | last commit 2019-09-30 (`aa0246cd15f7`), no tag | No commit in 7 years. Scorecard Maintained 0. Dead. |
| `github.com/CosmWasm/tinyjson` (an easyjson fork) | v0.9.0, 2021-08-22 | No commit since. Dead. |
| `github.com/go-faster/jx` | v1.2.0, 2025-11-18 | A token reader and writer, not a struct generator. Its users write the decode code for each type by hand (ogen generates that code from OpenAPI, not from Go types). That is a hand-written parser in effect. |
| `github.com/wI2L/jettison` | v0.7.4, 2022-03-21 | Encoder only. No release for 4 years. |
| `github.com/francoispqt/gojay` | v1.2.13, 2019-06-11 | Hand-written decode methods for each type. No release for 7 years. |
| `github.com/minio/simdjson-go` | v0.4.5, 2023-03-11 | A tree reader with its own API, not a drop-in decoder for Go types. No release for 3 years. |

No maintained generator other than easyjson was found.


## 5. Speed and memory

Benchmark names: `BenchmarkDecode` and `BenchmarkEncode` in
`bench/bench_test.go`. Raw results: `bench/results/quiet-results.txt` on
the spike branch. Each cell is the median of 10 samples. The parenthesis
holds the benchstat change against `std` when p < 0.05, or "~" for no
significant difference.

### Decode, time per operation

| payload | std | stdlegacy | stdv2 | gjexp | easyjson | goccy | sonic | sonicstd | jsoniter | segmentio |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| journal-10 | 232.5 µs | 230.9 µs (~) | 135.0 µs (-41.94%) | 137.0 µs (-41.07%) | 98.5 µs (-57.64%) | 58.5 µs (-74.84%) | 62.3 µs (-73.22%) | 85.2 µs (-63.36%) | 126.4 µs (-45.65%) | 278.6 µs (+19.84%) |
| journal-100 | 2.24 ms | 2.22 ms (~) | 1.32 ms (-40.84%) | 1.35 ms (-39.50%) | 921.4 µs (-58.84%) | 543.1 µs (-75.74%) | 594.4 µs (-73.45%) | 808.9 µs (-63.87%) | 1.19 ms (-46.71%) | 2.63 ms (+17.49%) |
| ckpt-10 | 253.0 µs | 269.5 µs (~) | 155.0 µs (-38.73%) | 158.3 µs (-37.45%) | 107.3 µs (-57.60%) | 61.5 µs (-75.71%) | 70.4 µs (-72.16%) | 96.1 µs (-62.02%) | 139.9 µs (-44.69%) | 326.4 µs (+28.99%) |
| ckpt-1000 | 29.88 ms | 28.70 ms (~) | 17.90 ms (-40.09%) | 18.55 ms (-37.93%) | 12.46 ms (-58.30%) | 7.56 ms (-74.70%) | 7.93 ms (-73.45%) | 10.77 ms (-63.96%) | 16.06 ms (-46.24%) | 38.66 ms (+29.37%) |
| ckpt-10000 | 339.88 ms | 333.96 ms (~) | 190.87 ms (-43.84%) | 194.31 ms (-42.83%) | 145.96 ms (-57.06%) | 84.01 ms (-75.28%) | 94.29 ms (-72.26%) | 127.17 ms (-62.58%) | 187.25 ms (-44.91%) | 439.99 ms (+29.46%) |
| envelope-10 | 53.4 µs | 157.1 µs (+194.33%) | 37.4 µs (-30.00%) | 37.4 µs (-29.97%) | 74.3 µs (+39.24%) | 20.3 µs (-61.98%) | 19.7 µs (-63.04%) | 20.7 µs (-61.24%) | 60.6 µs (+13.46%) | 98.3 µs (+84.07%) |
| envelope-1000 | 5.75 ms | 16.40 ms (+185.12%) | 3.82 ms (-33.49%) | 4.01 ms (-30.33%) | 7.93 ms (+37.82%) | 2.33 ms (-59.46%) | 2.29 ms (-60.24%) | 2.44 ms (-57.65%) | 6.57 ms (~) | 10.31 ms (+79.23%) |
| envelope-10000 | 66.31 ms | 178.55 ms (+169.26%) | 43.60 ms (-34.25%) | 43.81 ms (-33.93%) | 88.06 ms (+32.79%) | 28.98 ms (-56.30%) | 29.52 ms (-55.48%) | 31.56 ms (-52.40%) | 72.59 ms (~) | 113.92 ms (+71.79%) |
| tofu-10 | 288.1 µs | 391.5 µs (+35.89%) | 190.7 µs (-33.81%) | 177.5 µs (-38.39%) | 133.4 µs (-53.70%) | 68.1 µs (-76.38%) | 96.4 µs (-66.54%) | 122.0 µs (-57.66%) | 146.1 µs (-49.29%) | 323.6 µs (+12.33%) |
| tofu-10-any | 327.3 µs | 283.3 µs (-13.43%) | 151.6 µs (-53.69%) | 151.7 µs (-53.66%) | n/a | 68.4 µs (-79.09%) | 89.7 µs (-72.60%) | 119.3 µs (-63.54%) | 157.3 µs (-51.94%) | 995.6 µs (+204.21%) |
| tofu-1000 | 24.62 ms | 29.18 ms (+18.53%) | 15.49 ms (-37.08%) | 16.31 ms (-33.74%) | 11.64 ms (-52.72%) | 6.17 ms (-74.95%) | 8.17 ms (-66.82%) | 10.84 ms (-55.98%) | 13.28 ms (-46.04%) | 26.87 ms (+9.16%) |
| tofu-1000-any | 29.47 ms | 24.42 ms (-17.12%) | 14.88 ms (-49.50%) | 14.21 ms (-51.77%) | n/a | 6.22 ms (-78.89%) | 8.50 ms (-71.16%) | 11.34 ms (-61.51%) | 14.60 ms (-50.47%) | 85.90 ms (+191.49%) |
| tofu-10000 | 260.40 ms | 310.11 ms (+19.09%) | 165.37 ms (-36.49%) | 168.38 ms (-35.34%) | 118.59 ms (-54.46%) | 74.19 ms (-71.51%) | 91.11 ms (-65.01%) | 120.04 ms (-53.90%) | 137.67 ms (-47.13%) | 291.32 ms (+11.88%) |
| tofu-10000-any | 293.07 ms | 261.15 ms (-10.89%) | 141.15 ms (-51.84%) | 148.57 ms (-49.30%) | n/a | 68.01 ms (-76.79%) | 97.63 ms (-66.69%) | 121.43 ms (-58.57%) | 154.14 ms (-47.41%) | 852.37 ms (+190.85%) |

### Decode, bytes allocated per operation

| payload | std | stdlegacy | stdv2 | gjexp | easyjson | goccy | sonic | sonicstd | jsoniter | segmentio |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| journal-10 | 59.1 KiB | 60.3 KiB (+1.95%) | 53.4 KiB (-9.64%) | 53.4 KiB (-9.64%) | 56.7 KiB (-4.07%) | 50.6 KiB (-14.33%) | 66.3 KiB (+12.19%) | 76.2 KiB (+28.86%) | 68.2 KiB (+15.38%) | 112.3 KiB (+89.90%) |
| journal-100 | 563.8 KiB | 570.3 KiB (+1.15%) | 512.8 KiB (-9.05%) | 512.8 KiB (-9.05%) | 540.2 KiB (-4.18%) | 489.1 KiB (-13.25%) | 626.4 KiB (+11.09%) | 724.1 KiB (+28.43%) | 646.5 KiB (+14.66%) | 1.2 MiB (+118.30%) |
| ckpt-10 | 68.1 KiB | 70.6 KiB (+3.77%) | 61.9 KiB (-9.10%) | 61.9 KiB (-9.10%) | 65.5 KiB (-3.81%) | 54.8 KiB (-19.50%) | 75.8 KiB (+11.41%) | 88.5 KiB (+30.08%) | 78.7 KiB (+15.64%) | 144.2 KiB (+111.82%) |
| ckpt-1000 | 8.2 MiB | 8.1 MiB (-0.94%) | 7.4 MiB (-9.29%) | 7.4 MiB (-9.29%) | 7.6 MiB (-7.10%) | 6.5 MiB (-20.16%) | 8.7 MiB (+6.18%) | 10.0 MiB (+22.23%) | 8.9 MiB (+8.72%) | 16.2 MiB (+97.89%) |
| ckpt-10000 | 85.6 MiB | 84.8 MiB (-0.93%) | 77.9 MiB (-9.00%) | 77.9 MiB (-9.00%) | 79.8 MiB (-6.82%) | 67.3 MiB (-21.40%) | 87.6 MiB (+2.25%) | 100.8 MiB (+17.67%) | 96.8 MiB (+13.03%) | 161.0 MiB (+87.97%) |
| envelope-10 | 18.1 KiB | 18.5 KiB (+2.47%) | 18.1 KiB (~) | 18.1 KiB (~) | 18.2 KiB (+0.69%) | 36.1 KiB (+99.66%) | 36.1 KiB (+99.98%) | 36.1 KiB (+100.02%) | 30.3 KiB (+67.91%) | 18.1 KiB (~) |
| envelope-1000 | 1.8 MiB | 1.8 MiB (+0.02%) | 1.8 MiB (~) | 1.8 MiB (~) | 1.8 MiB (+0.01%) | 3.6 MiB (+100.22%) | 3.6 MiB (+100.12%) | 3.6 MiB (+100.17%) | 3.3 MiB (+81.51%) | 1.8 MiB (-0.00%) |
| envelope-10000 | 18.1 MiB | 18.1 MiB (+0.00%) | 18.1 MiB (~) | 18.1 MiB (~) | 18.1 MiB (+0.00%) | 36.7 MiB (+102.70%) | 36.2 MiB (+100.07%) | 36.2 MiB (+100.06%) | 32.8 MiB (+81.36%) | 18.1 MiB (-0.00%) |
| tofu-10 | 64.1 KiB | 66.9 KiB (+4.36%) | 57.7 KiB (-9.99%) | 57.7 KiB (-9.99%) | 60.7 KiB (-5.31%) | 48.0 KiB (-25.14%) | 93.2 KiB (+45.40%) | 102.4 KiB (+59.68%) | 73.0 KiB (+13.84%) | 114.7 KiB (+78.91%) |
| tofu-10-any | 72.4 KiB | 68.4 KiB (-5.50%) | 64.6 KiB (-10.79%) | 64.6 KiB (-10.79%) | n/a | 52.7 KiB (-27.20%) | 102.6 KiB (+41.77%) | 113.1 KiB (+56.26%) | 77.2 KiB (+6.70%) | 80.3 KiB (+10.93%) |
| tofu-1000 | 5.6 MiB | 5.8 MiB (+2.60%) | 5.1 MiB (-9.99%) | 5.1 MiB (-9.99%) | 5.3 MiB (-7.02%) | 4.1 MiB (-26.74%) | 8.0 MiB (+41.15%) | 8.7 MiB (+54.67%) | 6.2 MiB (+10.30%) | 10.7 MiB (+89.83%) |
| tofu-1000-any | 6.3 MiB | 5.9 MiB (-6.77%) | 5.7 MiB (-10.06%) | 5.7 MiB (-10.06%) | n/a | 4.5 MiB (-28.69%) | 8.8 MiB (+39.43%) | 9.6 MiB (+53.40%) | 6.7 MiB (+6.05%) | 7.0 MiB (+10.74%) |
| tofu-10000 | 58.5 MiB | 60.1 MiB (+2.64%) | 52.9 MiB (-9.64%) | 52.9 MiB (-9.64%) | 54.7 MiB (-6.58%) | 42.8 MiB (-26.96%) | 80.0 MiB (+36.62%) | 87.7 MiB (+49.81%) | 66.5 MiB (+13.55%) | 106.6 MiB (+82.15%) |
| tofu-10000-any | 63.2 MiB | 58.9 MiB (-6.76%) | 56.9 MiB (-9.88%) | 56.9 MiB (-9.88%) | n/a | 45.2 MiB (-28.43%) | 87.8 MiB (+38.89%) | 96.6 MiB (+52.86%) | 67.0 MiB (+6.01%) | 69.5 MiB (+10.05%) |

### Decode, allocations per operation

| payload | std | stdlegacy | stdv2 | gjexp | easyjson | goccy | sonic | sonicstd | jsoniter | segmentio |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| journal-10 | 1,171 | 1,129 (-3.59%) | 800 (-31.68%) | 800 (-31.68%) | 982 (-16.14%) | 255 (-78.22%) | 460 (-60.72%) | 965 (-17.59%) | 1,576 (+34.59%) | 985 (-15.88%) |
| journal-100 | 11,352 | 11,002 (-3.08%) | 7,901 (-30.40%) | 7,901 (-30.40%) | 9,531 (-16.04%) | 2,427 (-78.62%) | 4,389 (-61.34%) | 9,367 (-17.49%) | 15,528 (+36.79%) | 9,678 (-14.75%) |
| ckpt-10 | 1,375 | 1,386 (+0.80%) | 923 (-32.87%) | 923 (-32.87%) | 1,153 (-16.15%) | 266 (-80.65%) | 520 (-62.18%) | 1,133 (-17.60%) | 1,835 (+33.45%) | 1,159 (-15.71%) |
| ckpt-1000 | 171,702 | 159,793 (-6.94%) | 117,120 (-31.79%) | 117,120 (-31.79%) | 137,285 (-20.04%) | 33,926 (-80.24%) | 63,391 (-63.08%) | 134,953 (-21.40%) | 214,196 (+24.75%) | 138,613 (-19.27%) |
| ckpt-10000 | 1,724,853 | 1,599,801 (-7.25%) | 1,173,054 (-31.99%) | 1,173,054 (-31.99%) | 1,374,796 (-20.29%) | 339,925 (-80.29%) | 634,896 (-63.19%) | 1,351,458 (-21.65%) | 2,144,709 (+24.34%) | 1,388,125 (-19.52%) |
| envelope-10 | 2 | 10 (+400.00%) | 2 (~) | 2 (~) | 3 (+50.00%) | 3 (+50.00%) | 4 (+100.00%) | 4 (+100.00%) | 514 (+25600.00%) | 2 (~) |
| envelope-1000 | 2 | 10 (+400.00%) | 2 (~) | 2 (~) | 3 (+50.00%) | 3 (+50.00%) | 4 (+100.00%) | 4 (+100.00%) | 56,944 (+2847100.00%) | 2 (~) |
| envelope-10000 | 2 | 10 (+400.00%) | 5 (+150.00%) | 5 (+150.00%) | 3 (+50.00%) | 3 (+50.00%) | 4 (+100.00%) | 4 (+100.00%) | 569,945 (+28497150.00%) | 2 (~) |
| tofu-10 | 1,355 | 1,419 (+4.72%) | 868 (-35.94%) | 868 (-35.94%) | 1,092 (-19.41%) | 241 (-82.21%) | 547 (-59.63%) | 1,117 (-17.56%) | 1,791 (+32.18%) | 1,093 (-19.34%) |
| tofu-10-any | 1,776 | 1,406 (-20.83%) | 1,229 (-30.80%) | 1,229 (-30.80%) | n/a | 311 (-82.49%) | 686 (-61.37%) | 1,371 (-22.80%) | 1,941 (+9.29%) | 1,394 (-21.51%) |
| tofu-1000 | 120,063 | 123,643 (+2.98%) | 76,618 (-36.19%) | 76,618 (-36.19%) | 94,135 (-21.60%) | 20,196 (-83.18%) | 47,454 (-60.48%) | 96,434 (-19.68%) | 156,378 (+30.25%) | 94,962 (-20.91%) |
| tofu-1000-any | 156,557 | 121,799 (-22.20%) | 109,321 (-30.17%) | 109,321 (-30.17%) | n/a | 26,514 (-83.06%) | 59,902 (-61.74%) | 118,973 (-24.01%) | 168,732 (+7.78%) | 121,520 (-22.38%) |
| tofu-10000 | 1,198,985 | 1,234,723 (+2.98%) | 764,649 (-36.23%) | 764,647 (-36.23%) | 939,929 (-21.61%) | 201,601 (-83.19%) | 473,845 (-60.48%) | 962,925 (-19.69%) | 1,561,677 (+30.25%) | 948,257 (-20.91%) |
| tofu-10000-any | 1,563,445 | 1,216,250 (-22.21%) | 1,091,256 (-30.20%) | 1,091,257 (-30.20%) | n/a | 264,726 (-83.07%) | 598,193 (-61.74%) | 1,188,050 (-24.01%) | 1,684,985 (+7.77%) | 1,213,492 (-22.38%) |

### Encode, time per operation

`envelope` rows do not exist for encode. `sonic` and `easyjson` encode
maps in a different key order from call to call (section 7), so their
output is not byte-equal to the others.

| payload | std | stdlegacy | stdv2 | gjexp | easyjson | goccy | sonic | sonicstd | jsoniter | segmentio |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| journal-10 | 99.6 µs | 105.3 µs (~) | 92.3 µs (~) | 96.2 µs (~) | 100.6 µs (~) | 45.2 µs (-54.62%) | 42.6 µs (-57.19%) | 48.4 µs (-51.44%) | 147.5 µs (+48.06%) | 59.6 µs (-40.18%) |
| journal-100 | 1.08 ms | 1.08 ms (~) | 931.1 µs (-14.18%) | 982.4 µs (~) | 1.11 ms (~) | 439.5 µs (-59.48%) | 414.8 µs (-61.77%) | 439.8 µs (-59.46%) | 1.54 ms (+41.83%) | 604.0 µs (-44.32%) |
| ckpt-10 | 149.1 µs | 131.8 µs (-11.64%) | 108.3 µs (-27.36%) | 118.3 µs (-20.67%) | 143.2 µs (~) | 50.4 µs (-66.21%) | 45.2 µs (-69.69%) | 52.6 µs (-64.75%) | 189.0 µs (+26.73%) | 68.5 µs (-54.05%) |
| ckpt-1000 | 15.39 ms | 16.10 ms (~) | 12.54 ms (-18.54%) | 13.43 ms (-12.72%) | 16.99 ms (~) | 6.24 ms (-59.45%) | 8.00 ms (-48.00%) | 9.93 ms (-35.46%) | 24.24 ms (+57.51%) | 8.37 ms (-45.61%) |
| ckpt-10000 | 182.84 ms | 191.53 ms (~) | 156.73 ms (-14.28%) | 159.15 ms (~) | 189.51 ms (~) | 93.02 ms (-49.12%) | 109.76 ms (-39.97%) | 119.75 ms (-34.51%) | 315.45 ms (+72.53%) | 115.68 ms (-36.73%) |
| tofu-10 | 146.1 µs | 125.7 µs (-13.93%) | 114.4 µs (-21.70%) | 111.2 µs (-23.87%) | 128.9 µs (-11.76%) | 49.9 µs (-65.86%) | 35.3 µs (-75.84%) | 49.1 µs (-66.41%) | 160.4 µs (~) | 70.7 µs (-51.60%) |
| tofu-1000 | 14.99 ms | 11.84 ms (-21.05%) | 10.81 ms (-27.92%) | 10.79 ms (-28.00%) | 12.42 ms (-17.18%) | 4.91 ms (-67.23%) | 4.98 ms (-66.77%) | 5.99 ms (-60.02%) | 15.60 ms (~) | 6.86 ms (-54.22%) |
| tofu-10000 | 166.87 ms | 139.47 ms (-16.42%) | 120.13 ms (-28.01%) | 125.31 ms (-24.91%) | 123.66 ms (-25.89%) | 63.83 ms (-61.75%) | 72.82 ms (-56.36%) | 91.67 ms (-45.06%) | 189.52 ms (+13.57%) | 87.38 ms (-47.63%) |

### Encode, bytes allocated per operation

| payload | std | stdlegacy | stdv2 | gjexp | easyjson | goccy | sonic | sonicstd | jsoniter | segmentio |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| journal-10 | 18.9 KiB | 36.4 KiB (+92.52%) | 17.5 KiB (-7.53%) | 17.5 KiB (-7.52%) | 29.8 KiB (+57.25%) | 16.0 KiB (-15.38%) | 24.8 KiB (+31.24%) | 24.9 KiB (+31.36%) | 71.7 KiB (+278.80%) | 16.0 KiB (-15.44%) |
| journal-100 | 177.1 KiB | 345.1 KiB (+94.83%) | 177.3 KiB (+0.07%) | 177.3 KiB (+0.10%) | 273.1 KiB (+54.18%) | 144.1 KiB (-18.65%) | 224.5 KiB (+26.71%) | 224.9 KiB (+26.95%) | 696.6 KiB (+293.23%) | 145.1 KiB (-18.12%) |
| ckpt-10 | 23.0 KiB | 43.6 KiB (+89.68%) | 20.5 KiB (-10.76%) | 20.5 KiB (-10.79%) | 36.4 KiB (+58.40%) | 18.0 KiB (-21.59%) | 26.5 KiB (+15.51%) | 26.5 KiB (+15.54%) | 83.6 KiB (+263.89%) | 18.1 KiB (-21.41%) |
| ckpt-1000 | 2.4 MiB | 4.8 MiB (+99.96%) | 2.2 MiB (-8.25%) | 2.2 MiB (-8.28%) | 3.8 MiB (+54.95%) | 1.8 MiB (-23.93%) | 10.9 MiB (+349.53%) | 13.9 MiB (+474.22%) | 12.0 MiB (+393.58%) | 1.9 MiB (-22.20%) |
| ckpt-10000 | 39.3 MiB | 58.7 MiB (+49.35%) | 35.1 MiB (-10.65%) | 35.1 MiB (-10.65%) | 40.4 MiB (+2.86%) | 24.3 MiB (-38.16%) | 103.1 MiB (+162.31%) | 112.7 MiB (+186.73%) | 168.0 MiB (+327.41%) | 28.5 MiB (-27.44%) |
| tofu-10 | 21.9 KiB | 40.4 KiB (+84.72%) | 17.9 KiB (-18.13%) | 17.9 KiB (-18.08%) | 26.8 KiB (+22.54%) | 14.0 KiB (-35.95%) | 18.7 KiB (-14.51%) | 18.7 KiB (-14.38%) | 66.9 KiB (+205.89%) | 14.3 KiB (-34.74%) |
| tofu-1000 | 1.9 MiB | 3.4 MiB (+77.03%) | 1.6 MiB (-17.04%) | 1.6 MiB (-17.04%) | 2.2 MiB (+14.00%) | 1.2 MiB (-38.65%) | 6.4 MiB (+230.76%) | 8.5 MiB (+335.81%) | 6.3 MiB (+226.80%) | 1.2 MiB (-37.81%) |
| tofu-10000 | 28.3 MiB | 37.9 MiB (+34.02%) | 22.4 MiB (-20.84%) | 22.8 MiB (-19.31%) | 23.3 MiB (-17.57%) | 13.8 MiB (-51.38%) | 65.3 MiB (+130.89%) | 83.7 MiB (+195.84%) | 97.9 MiB (+246.22%) | 16.5 MiB (-41.64%) |

### Encode, allocations per operation

| payload | std | stdlegacy | stdv2 | gjexp | easyjson | goccy | sonic | sonicstd | jsoniter | segmentio |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| journal-10 | 208 | 663 (+218.75%) | 117 (-43.75%) | 117 (-43.75%) | 354 (+70.19%) | 1 (-99.52%) | 94 (-54.81%) | 94 (-54.81%) | 1,055 (+407.21%) | 1 (-99.52%) |
| journal-100 | 2,242 | 6,450 (+187.69%) | 1,251 (-44.20%) | 1,251 (-44.20%) | 3,712 (+65.57%) | 1 (-99.96%) | 857 (-61.78%) | 857 (-61.78%) | 10,041 (+347.86%) | 33 (-98.53%) |
| ckpt-10 | 350 | 801 (+128.86%) | 193 (-44.86%) | 193 (-44.86%) | 565 (+61.43%) | 1 (-99.71%) | 92 (-73.71%) | 92 (-73.71%) | 1,159 (+231.14%) | 3 (-99.14%) |
| ckpt-1000 | 34,836 | 97,491 (+179.86%) | 19,168 (-44.98%) | 19,168 (-44.98%) | 58,049 (+66.64%) | 1 (-100.00%) | 12,328 (-64.61%) | 12,334 (-64.59%) | 143,894 (+313.07%) | 333 (-99.04%) |
| ckpt-10000 | 348,351 | 976,500 (+180.32%) | 191,677 (-44.98%) | 191,677 (-44.98%) | 580,693 (+66.70%) | 11 (-100.00%) | 123,347 (-64.59%) | 123,358 (-64.59%) | 1,441,445 (+313.79%) | 3,341 (-99.04%) |
| tofu-10 | 517 | 780 (+50.87%) | 266 (-48.55%) | 266 (-48.55%) | 593 (+14.70%) | 1 (-99.81%) | 51 (-90.14%) | 51 (-90.14%) | 783 (+51.45%) | 9 (-98.26%) |
| tofu-1000 | 47,542 | 67,374 (+41.71%) | 24,587 (-48.28%) | 24,587 (-48.28%) | 51,489 (+8.30%) | 1 (-100.00%) | 4,100 (-91.38%) | 4,104 (-91.37%) | 66,589 (+40.06%) | 9 (-99.98%) |
| tofu-10000 | 475,054 | 672,779 (+41.62%) | 245,693 (-48.28%) | 245,694 (-48.28%) | 514,182 (+8.24%) | 5 (-100.00%) | 40,717 (-91.43%) | 40,724 (-91.43%) | 664,830 (+39.95%) | 14 (-100.00%) |

### Peak RSS of one decode of the largest payloads

One decode in a fresh process (`probe decode`), median of 3 runs, `VmHWM`
in KiB. The process holds the input bytes too: a process that only reads
the file peaks at 33,676 KiB (ckpt-10000) and 34,896 KiB (tofu-10000).
These ran before the quiet pass. RSS does not depend on CPU load in the
way time does, but it can move with garbage collector timing.

| Library | ckpt-10000, typed | tofu-10000, typed | tofu-10000, interface value |
| --- | --- | --- | --- |
| `std` | 112,012 | 90,108 | 90,116 |
| `stdv2` | 107,316 | 84,968 | 91,952 |
| `gjexp` | 107,288 | 84,924 | 91,836 |
| `easyjson` | 111,772 | 87,676 | n/a |
| `goccy` | 129,684 | 104,076 | 103,172 |
| `sonic` | 129,764 | 115,012 | 123,348 |
| `sonicstd` | 139,740 | 122,964 | 133,152 |
| `jsoniter` | 122,020 | 96,616 | 97,616 |
| `segmentio` | 201,596 | 146,004 | 102,676 |

### What the numbers show

- `std` in Go 1.27.2 and `stdlegacy` (the pre-v2 code) show no
  significant difference on typed checkpoint and journal decode. `std` is
  faster on typed OpenTofu state (`stdlegacy` +18% to +36%) and on
  `envelope` (`stdlegacy` +169% to +194%). `stdlegacy` is faster on
  interface-value decode (-11% to -17%).
- `stdv2` decodes 34% to 54% faster than `std` on every typed and
  interface-value payload, with 30% to 36% fewer allocations. Both run the
  same code with other options, so the difference may come from the v1
  options (for example case-insensitive names). This note did not profile
  it.
- `goccy` takes 71% to 79% less time than `std` on every typed and
  interface-value decode, and `sonic` 65% to 74% less. `goccy` also
  allocates the least bytes and objects of all decoders.
- On `envelope` (what `main` decodes today) `goccy`, `sonic` and
  `sonicstd` take 52% to 63% less time than `std`, but allocate about twice
  the bytes. `easyjson` is 33% to 39% slower than `std` there. `jsoniter`
  is 13% slower at 10 resources and shows no significant difference at
  1,000 and 10,000.
- `segmentio` is slower than `std` on every decode payload: 9% to 30% on
  typed payloads, 72% to 84% on `envelope`, and about 3x on interface
  values.
- Encode: `goccy`, `sonic` and `segmentio` take 37% to 76% less time than
  `std`. `jsoniter` is slower than `std` (13% to 73%) or shows no
  significant difference. `easyjson` shows no significant difference from
  `std` on the checkpoint and journal payloads, and takes 12% to 26% less
  time on OpenTofu state.

## 6. One value at one path (issue #389)

The lookup reads one value near the end of a large document and keeps
nothing else:

- OpenTofu: `resources[9994].instances[0].attributes.password` in
  tofu-10000 (20,290,277 bytes).
- Pulumi: `deployment.resources[9999].outputs.result` (a secret object) in
  ckpt-10000 (18,982,547 bytes).

Extractors (`bench/stream.go`):

| Name | How | Reads a stream |
| --- | --- | --- |
| `std-full` | `json.Unmarshal` of the whole document into an interface value, then a walk. This is what `main` does today. | no |
| `std-token` | `json.Decoder.Token` walk, `Decode` of the value at the path. The #389 proposal. | yes |
| `goccy-token` | the same walk with `goccy.Decoder` | yes |
| `stdv2-jsontext` | `jsontext.Decoder`: `ReadToken`, `SkipValue` for the other members, `ReadValue` at the path | yes |
| `gjexp-jsontext` | the same code with the go-json-experiment import (an alias on Go 1.27) | yes |
| `jsoniter-iter` | `jsoniter.Iterator` from the reader (4 KiB buffer): `ReadObject`, `Skip` | yes |
| `sonic-search` | `ast.NewSearcher(...).GetByPath`, on the whole input as one string (no copy) | no |

### Time and allocation

`BenchmarkExtract`, 10 interleaved samples, change against `std-full`.

| payload | std-full | std-token | goccy-token | stdv2-jsontext | gjexp-jsontext | jsoniter-iter | sonic-search |
| --- | --- | --- | --- | --- | --- | --- | --- |
| tofu-10000 | 295.71 ms | 101.20 ms (-65.78%) | 130.72 ms (-55.80%) | 44.21 ms (-85.05%) | 47.46 ms (-83.95%) | 63.48 ms (-78.53%) | 2.17 ms (-99.27%) |
| ckpt-10000 | 351.96 ms | 131.49 ms (-62.64%) | 141.60 ms (-59.77%) | 48.23 ms (-86.30%) | 50.86 ms (-85.55%) | 67.30 ms (-80.88%) | 2.10 ms (-99.40%) |

| payload | std-full | std-token | goccy-token | stdv2-jsontext | gjexp-jsontext | jsoniter-iter | sonic-search |
| --- | --- | --- | --- | --- | --- | --- | --- |
| tofu-10000 | 63.2 MiB | 22.2 MiB (-64.91%) | 39.8 MiB (-37.06%) | 13.1 KiB (-99.98%) | 13.2 KiB (-99.98%) | 9.8 MiB (-84.43%) | 170 B (-100.00%) |
| ckpt-10000 | 86.9 MiB | 33.5 MiB (-61.43%) | 48.7 MiB (-43.96%) | 14.7 KiB (-99.98%) | 14.7 KiB (-99.98%) | 16.1 MiB (-81.50%) | 1.5 KiB (-100.00%) |

| payload | std-full | std-token | goccy-token | stdv2-jsontext | gjexp-jsontext | jsoniter-iter | sonic-search |
| --- | --- | --- | --- | --- | --- | --- | --- |
| tofu-10000 | 1,563,458 | 1,280,132 (-18.12%) | 1,256,697 (-19.62%) | 81 (-99.99%) | 81 (-99.99%) | 461,791 (-70.46%) | 3 (-100.00%) |
| ckpt-10000 | 2,041,669 | 1,758,172 (-13.89%) | 1,728,143 (-15.36%) | 106 (-99.99%) | 106 (-99.99%) | 575,586 (-71.81%) | 7 (-100.00%) |

`stdv2-jsontext` and `gjexp-jsontext` allocate about 13 to 15 KiB and 81
to 106 times per lookup, whatever the document size. `std-token` (the
#389 proposal) still allocates 22 to 34 MiB, because `Decoder.Token`
returns every skipped string as a new Go string. `sonic-search` takes
about 2 ms, but it needs the whole document in memory and does not check
all skipped content (below).

### Peak RSS of one lookup in a fresh process

The streaming extractors read the file through `os.File` and never hold
it. `std-full` and `sonic-search` read the whole file first. Median of 3
runs, in KiB. For scale, a process that only reads the file has a peak RSS
of 33,676 KiB (ckpt) and 34,896 KiB (tofu).

| Extractor | tofu-10000 | ckpt-10000 |
| --- | --- | --- |
| `std-full` | 90,516 | 115,632 |
| `std-token` | 18,892 | 21,024 |
| `goccy-token` | 20,192 | 25,248 |
| `stdv2-jsontext` | 15,092 | 14,964 |
| `gjexp-jsontext` | 15,092 | 15,008 |
| `jsoniter-iter` | 18,768 | 18,704 |
| `sonic-search` | 35,292 | 34,032 |

### Checks of skipped content

`TestExtractInvalid` looks up `b.c` in small documents that are invalid
in another member. "v" means the extractor returned the value.

| Document | `std-full` | `std-token`, `goccy-token`, `stdv2-jsontext`, `gjexp-jsontext`, `jsoniter-iter` | `sonic-search` |
| --- | --- | --- | --- |
| valid | v | v | v |
| bad escape in a skipped member before the path | error | error | v |
| trailing comma in a skipped member before | error | error | v |
| bad number `01` in a skipped member before | error | error | v |
| unbalanced bracket in a skipped member before | error | error | error |
| invalid literal after the value | error | v | v |
| document truncated after the value | error | v | v |

A lookup that stops at the value does not read the rest of the document.
So every streaming extractor returns a value from a document that is
invalid after the path. `sonic-search` also skips members before the path
without a full check, although `NewSearcher` sets `ValidateJSON: true`
(`ast/search.go`). For #389 this matters only if stored content can be
invalid JSON. This note did not check whether every write path checks
the JSON before it stores it.


## 7. Correctness against `encoding/json`

### Method

- `TestCorrectnessTable` (`bench/correct_test.go`) runs 54 cases with every
  library and compares the outcome with `encoding/json` of Go 1.27.2. An
  outcome is "error", the decoded value printed with `%#v`, or the encoded
  bytes. For encoded bytes that differ, the test decodes both with
  `encoding/json`. Equal values count as a byte-only difference.
- The same table with `GOEXPERIMENT=nojsonv2` compares the pre-v2 code of
  `encoding/json` with the Go 1.27 code. One case differs, and only in
  bytes: Go 1.27 writes U+FFFD as a raw character for an invalid UTF-8
  byte, the pre-v2 code writes `\ufffd`.
- easyjson cannot decode into an interface value, so cases on interface
  values are "n/a" for it. Its typed cases use the generated copy of the
  types.
- Differential fuzzing: one `go test -fuzz` target per library
  (`bench/fuzz_test.go`), 2 minutes each, `-parallel 2`. The target
  decodes the input into an interface value with `encoding/json` and with
  the library. It fails on a different accept or reject, a different value,
  or a library encoding that does not decode back to the same value. It
  skips inputs in a class that the table already records for that
  library. Phase B also skips inputs that `encoding/json` rejects and the
  library accepts, to look for value differences behind them. The easyjson
  target decodes a typed journal entry instead.

### Cases with a difference

Cases where every library equals `encoding/json` are not in this table.
They are: integers beyond 2^53 into an interface value (all give the same
rounded float64, 9007199254740992), int64 limits, `1e3` into int64, float
round trips of `0.1`, `1.0000000000000002` and `5e-324`, `1e400`,
escaped NUL, unknown members ignored by default, `null` and a string for
an int field, trailing data, two values, trailing commas, leading zeros,
`\x` escapes, `NaN`, `nul`, a leading BOM, single quotes, empty input, and
five invalid inputs on a typed target (a leading zero and a trailing comma
in an unknown member, an invalid literal, a truncated object, trailing
data).

| Case | `encoding/json` | `stdv2`, `gjexp` | `easyjson` | `goccy` | `sonic` | `sonicstd` | `jsoniter` | `segmentio` |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `-0` round trip through an interface value | `-0` | same | n/a | same | `0` | `0` | same | same |
| duplicate key, interface value | last wins | error | n/a | same | same | same | same | same |
| duplicate key, struct | last wins | error | same | same | same | same | same | same |
| invalid UTF-8 byte into a string | replaced with U+FFFD | error | byte kept | same | byte kept | same | byte kept | same |
| invalid UTF-8 byte into `json.RawMessage` | byte kept | error | same | same | same | replaced with U+FFFD | same | same |
| lone surrogate escape `\ud800` into a string | U+FFFD | error | same | same | same | same | same | same |
| lone surrogate escape into `json.RawMessage` | escape kept | error | same | same | same | same | same | same |
| raw control byte (newline) in a string | error | same | n/a | same | accepted | same | same | same |
| bad escape `\x` in an unknown member, typed | error | same | accepted | same | accepted | same | same | same |
| raw tab in an unknown member, typed | error | same | accepted | same | accepted | same | same | same |
| leading zero `01` as an unknown member's value, typed | error | same | accepted | same | same | same | same | same |
| raw control byte 0x10 after an escape | error | same | n/a | same | accepted | same | accepted | same |
| top-level string without a closing quote, 33 bytes | error | same | n/a | same | accepted, last byte lost | same | same | same |
| NUL byte after the value | error | same | n/a | same | same | same | accepted | same |
| strict mode, unknown member | error | same | no run-time option | same | same | same | same | same |
| member name in another case (`KIND` for `kind`) | matched | not matched | not matched | same | same | same | same | same |
| 10,000 nested arrays | accepted | same | n/a | same | error (limit 4,096) | error | same | same |
| 10,001 nested arrays | error | same | n/a | same | same | same | same | accepted (no limit) |
| `null` in a `map[string][]string`, re-encoded | `null` | same | same | same | same | same | same | `[]` |
| encode a string with an invalid UTF-8 byte | U+FFFD | error | same value | same | same value | same value | same value | same value |
| encode `<`, `>`, `&`, U+2028 | escaped | same value, not escaped | same | same | same value, not escaped | same | same | same |
| encode a map twice, same key order | sorted | order varies | order varies | same | order varies | same | same | same |
| encode `omitempty` on a false bool | omitted | written | same | same | same | same | same | same |
| encode `1e-7` | `1e-7` | same | same | same | same | same | same value, `1e-07` | same |

Counts of value differences (not byte-only) on the 54 cases:

| Library | Differences | Byte-only encode differences |
| --- | --- | --- |
| `stdv2`, `gjexp` | 11 (10 causes: the `omitempty` change shows in two cases) | 1 |
| `easyjson` | 7 | 1 |
| `goccy` | 0 | 0 |
| `sonic` | 9 | 2 |
| `sonicstd` | 3 | 1 |
| `jsoniter` | 3 | 2 |
| `segmentio` | 2 | 1 |

Notes:

- `stdv2` differences are the v2 defaults: duplicate names and invalid
  UTF-8 rejected, case-sensitive names, `omitempty` defined on JSON
  values, no HTML escaping, map order not sorted (`Deterministic(false)`).
  `encoding/json` in Go 1.27 is the same code with `DefaultOptionsV1()`.
- The Pulumi CLI does not send invalid UTF-8. Since v3.254.0 it encodes a
  string with invalid UTF-8 as an object with the byte string signature
  `803fd3297a5875dc03ca845dda5d2a98` and the bytes in base64 (pulumi/pulumi
  `e00f356fea`, `sdk/go/common/apitype/property-values.json`). The entry
  then has `requiresByteString`. So the wire JSON is valid UTF-8, and no
  library needs to keep a raw invalid byte for the journal. A
  `json.RawMessage` keeps such a byte with every library except
  `sonicstd` (replaced) and `stdv2` (rejected).
- Map key order matters if the server hashes or compares encoded bytes.
  `easyjson`, `sonic` and `stdv2` with default options give a different
  order from call to call.

### Fuzzing

| Target | Phase A (all classes) | Phase B (lenient accepts skipped) |
| --- | --- | --- |
| `stdv2` | no new difference, 567,350 execs | no new difference, 743,344 execs |
| `gjexp` | no new difference, 404,754 execs | no new difference, 892,335 execs |
| `goccy` | no new difference, 418,379 execs | no new difference, 1,104,329 execs |
| `sonic` | run 1 stopped at a seed (raw newline in a string, then recorded). Run 2 found `"` plus 32 zeros accepted. | no new difference, 629,699 execs |
| `sonicstd` | no new difference, 660,014 execs | no new difference, 1,257,113 execs |
| `jsoniter` | run 1 found `0` plus a NUL byte accepted. Run 2 found a raw 0x10 byte after an escape accepted. | no new difference, 713,361 execs |
| `segmentio` | no new difference, 707,982 execs | no new difference, 404,069 execs |
| `easyjson` (typed) | not run: the table already shows lenient accepts | no new difference, 310,921 execs |

Each fuzz finding is now a row in the table above. The sonic one is a
top-level string whose length after the quote is 32 or 64 bytes: a probe
over lengths 0 to 69 with four prefixes (`"`, `["`, `{"a":"`,
`{"a":["`) found only those two inputs. `sonicstd` rejects them. The
fuzz runs shared the machine with other jobs (load average 1.4 to 8.5 at
the beginning of each run, in `results/fuzz-summary.txt`). The skip rules make some
classes invisible to the fuzzer: segmentio inputs with `null`, sonic
inputs with any control byte or `-0`, and inputs with invalid UTF-8 or
surrogate escapes for every library.



## 8. Security and hostile input

Each hostile input ran in a fresh process (`probe hostile LIB CASE`, see
appendix A), decoded into an interface value. `peak RSS` is `VmHWM` from
`/proc/self/status` at the end of the process (`/usr/bin/time` is not
installed on this machine). `allocated` is `/gc/heap/allocs:bytes` from
`runtime/metrics`. These runs shared the machine with other agents (load
average 4 to 9), and the segmentio timing runs also with the fuzz runs.
So the times in this section are rough. The byte counts do not depend on
load.

### Nesting depth

| Input | `std`, `stdv2`, `gjexp`, `goccy`, `jsoniter` | `sonic`, `sonicstd` | `segmentio` |
| --- | --- | --- | --- |
| 10,000 nested arrays (20 KB) | accepted | rejected: limit 4,096 | accepted, 2.3 s |
| 10,001 nested arrays | rejected: limit 10,000 | rejected | accepted |
| 20,000 nested arrays (40 KB) | rejected | rejected | accepted, 9.6 s |
| 40,000 nested arrays (80 KB) | rejected | rejected | accepted, 41.7 s |
| 100,000 nested arrays (200 KB) | rejected | rejected | not finished after 120 s (killed by `timeout`) |
| 1,000,000 nested arrays or objects | rejected, no crash | rejected, no crash | not finished after 120 s |

segmentio has no depth limit. Its time grows about four times for each
doubling of depth, so a request body of a few hundred KB can hold a CPU
core for minutes. Every other library stops at its limit with an error.

### Small input, large allocation

Input: 3,000,004 bytes. `[[],[],...]` is one million empty arrays.
`[{},{},...]` is one million empty objects.

| Library | empty arrays: allocated, peak RSS | empty objects: allocated, peak RSS |
| --- | --- | --- |
| `std` | 141 MiB (49x), 97 MiB | 179 MiB (63x), 118 MiB |
| `stdv2`, `gjexp` | 118 MiB (41x), 96 MiB | 141 MiB (49x), 105 MiB |
| `goccy` | 113 MiB (40x), 91 MiB | 159 MiB (56x), 114 MiB |
| `sonic`, `sonicstd` | 310 MiB (108x), 197 MiB | 89 MiB (31x), 88 MiB |
| `jsoniter` | 133 MiB (47x), 97 MiB | 156 MiB (55x), 115 MiB |
| `segmentio` | 227 MiB (79x), 151 MiB | 97 MiB (34x), 91 MiB |

Every library allocates 31 to 108 times the input size when it decodes into
an interface value. A typed target skips unknown members and does not
allocate for them. `main` decodes OpenTofu state into an interface value,
and `workspace.DefaultMaxStateBytes` is 512 MiB. The Pulumi checkpoint
limit is 32 MiB (`stack.DefaultMaxCheckpointBytes`). No library removes
this risk. Only a size limit or a typed target bounds it.

### Input in error messages

- `sonic`: the error text holds a part of the input, for example `"Syntax
  error at index 4096: recursion exceeded max depth\n\n\t[[[[[[..."`.
- `jsoniter`: the error text holds up to 50 bytes before and after the
  error position (`ReportError` in `iter.go`: "bigger context").
- `stdv2`, `gjexp`: the error names the JSON Pointer of the position, for
  example `within "/0/0/0/..."`. That holds object member names, not values.
- `std`, `goccy`: the depth error holds no input. Syntax errors in `std`
  name one character.

An error text that holds input can put a secret into a log line, if the
caller logs the error.

### `unsafe`, assembly, cgo and run-time code generation

Counts are non-test `.go` files that import `unsafe` or use
`go:linkname`, and `.s` files, in the module at the tested version. They
cover the whole module, not only the linked packages.

| Module | `unsafe` files | `go:linkname` files | `.s` files | cgo |
| --- | --- | --- | --- | --- |
| standard library | used inside the toolchain | | | no |
| `go-json-experiment/json` | 0 | 0 | 0 | no |
| `mailru/easyjson` | 2 | 0 | 0 | no |
| `goccy/go-json` | 68 | 0 | 6 | no |
| `bytedance/sonic` | 69 | 9 | 32 | no |
| `bytedance/sonic/loader` | 8 | 5 | 1 | no |
| `json-iterator/go` | 22 | 0 | 0 | no |
| `modern-go/reflect2` | 20 | 5 | 9 | no |
| `segmentio/encoding` | 34 | 3 | 0 | no |
| `segmentio/asm` | 4 | 0 | 19 | no |

sonic, by platform and Go version (build constraints in `sonic.go` and
`compat.go` of v1.15.4):

- The native code builds for `(amd64 && go1.17 && !go1.28) || (arm64 &&
  go1.20 && !go1.28)`. All five release platforms are amd64 or arm64.
- On amd64, sonic generates machine code at run time (a JIT built on
  `golang-asm`). `sonic/loader` maps it with `PROT_READ | PROT_EXEC`
  (`mmap_unix.go`) and registers function data with the Go runtime.
- On every other platform, and on Go 1.28 or later, `compat.go` builds
  instead, with `apiKind = UseStdJSON`. The API stays, and the work goes
  to `encoding/json`. A Go 1.28 upgrade changes sonic's behavior and speed
  until a sonic release lifts the `!go1.28` bound.


## 9. Provenance

Method:

- Git history: a blobless clone of each repository
  (`git clone --filter=blob:none`) on 2026-10-12. "Authors, 12 months" counts
  distinct author names from 2025-10-12, without bots. One person with two
  names counts twice, so the number can be too high.
- Scorecard: `https://api.securityscorecards.dev/projects/github.com/OWNER/REPO`
  on 2026-10-12. The network allowed it. The check dates are 2026-10-05 or
  2026-10-12.
- Advisories: the OSV API (`POST https://api.osv.dev/v1/query`, which
  includes the GitHub Advisory Database for Go) and the Go index
  `https://vuln.go.dev/index/modules.json`. The GitHub REST API for these
  repositories was not reachable from this session (HTTP 403, the session
  is bound to the OpenDustpan repository).
- govulncheck: `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...` in
  the benchmark module, which imports every candidate.
- Checksums: for each tested version, three values must be equal. (1) The
  `h1:` line in the benchmark module's `go.sum`, which the go command took
  from the module proxy. (2) The line from
  `https://sum.golang.org/lookup/MODULE@VERSION`. (3) A hash of a module zip
  that `golang.org/x/mod/zip.CreateFromVCS` (x/mod v0.41.0) built from the
  git clone at the tag or commit, hashed with `dirhash.HashZip`. The tool is
  `cmd/modsum` on the spike branch.
- Tags: `git cat-file -t refs/tags/TAG` gives `commit` for a lightweight
  tag and `tag` for an annotated one. A `gpgsig` header on the tagged
  commit shows a signed commit.

| | `std`, `stdv2` | `gjexp` | `easyjson` | `goccy` | `sonic` | `jsoniter` | `segmentio` |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Owner | Go project | go-json-experiment (Joe Tsai and others of the Go team) | mailru (Mail.Ru / VK) | goccy (one person) | bytedance | json-iterator | segmentio (Segment) |
| License | BSD-3-Clause | BSD-3-Clause | MIT | MIT | Apache-2.0, plus files under Boost-1.0, MIT, BSD-3-Clause, Apache-2.0 in `licenses/` | MIT | MIT |
| Last commit on default branch | n/a | 2026-08-20 | 2026-03-14 | 2026-10-12 | 2026-09-26 | 2022-09-16 | 2026-03-11 |
| Commits, 12 months | n/a | 21 | 1 | 101 | 45 | 0 | 1 |
| Authors, 12 months | n/a | 1 | 1 | 4 (98 of 101 commits by one person) | 14 | 0 | 1 |
| Archived | no | no | no | no | no | yes (Scorecard: "project is archived") | no |
| Tested tag | Go release | none (pseudo-version) | lightweight | lightweight | lightweight | lightweight | lightweight |
| Tagged commit signed | n/a | yes, GitHub web-flow key | yes, GitHub web-flow key | yes, GitHub web-flow key | no | no | yes, GitHub web-flow key |
| Scorecard score | golang/go 6.2 | 3.8 | 3.5 | 4.9 | 6.3 | 3.3 | 4.2 |
| Scorecard Maintained | 10 | 2 | 0 | 10 | 10 | 0 | 0 |
| Scorecard Code-Review | 10 | 1 | 7 | 0 | 10 | 4 | 10 |
| Scorecard Fuzzing | 10 | 10 | 0 | 10 | 10 | 10 | 0 |
| Scorecard Signed-Releases | -1 (no releases found) | -1 | -1 | -1 | -1 | -1 | -1 |
| OSV advisories (all versions) | n/a | 0 | 0 | 0 | 0 | 0 | 0 |
| govulncheck | 0 | 0 | 0 | 0 | 0 (see note) | 0 | 0 |
| Checksum: go.sum = sumdb = rebuilt from tag | Go toolchain | equal | equal | equal | equal (also `sonic/loader` v0.5.2) | equal | equal |
| ADR 7 age of tested version on 2026-10-12 | n/a | 53 days | 212 days | 15 days | 32 days | 1,857 days | 216 days |

govulncheck found one advisory in a required module that the code does
not call: GO-2026-5024 in `golang.org/x/sys` v0.22.0 (windows only, first patched
version v0.44.0). sonic, `cloudwego/base64x` and `klauspost/cpuid` require that
version. `main` already requires `golang.org/x/sys` v0.48.0, so minimal
version selection would pick v0.48.0 in `main`.

Modules that each candidate links into a binary, from `go list -deps` of a
program that imports only that library, per release platform:

| Candidate | Linked modules (beyond the candidate) | License of each |
| --- | --- | --- |
| `std`, `stdv2` | none | |
| `gjexp` | none | |
| `easyjson` | `github.com/josharian/intern` v1.0.0 | MIT |
| `goccy` | none | |
| `sonic`, amd64 (linux, windows) | `bytedance/sonic/loader` v0.5.2, `bytedance/gopkg` v0.1.3, `cloudwego/base64x` v0.1.6, `klauspost/cpuid/v2` v2.2.9, `twitchyliquid64/golang-asm` v0.15.1, `golang.org/x/arch` v0.0.0-20210923205945-b76863e36670 | Apache-2.0, Apache-2.0, Apache-2.0, MIT, BSD-3-Clause, BSD-3-Clause |
| `sonic`, arm64 (linux, darwin) | `bytedance/gopkg` v0.1.3, `golang.org/x/arch` | Apache-2.0, BSD-3-Clause |
| `jsoniter` | `modern-go/reflect2` v1.0.2, `modern-go/concurrent` v0.0.0-20180228061459-e0a39a4cb421 | Apache-2.0, Apache-2.0 |
| `segmentio` | `segmentio/asm` v1.1.3, `golang.org/x/sys` | MIT, BSD-3-Clause |

Licenses come from the `LICENSE` (or `license.md`) file of each module in
the module cache. Every license above is permissive and allows an
Apache-2.0 project (ADR 22). sonic's `licenses/` directory holds
Boost-1.0 texts (`LICENSE-Drachennest`, `LICENSE-eisel_lemire`). Boost-1.0
is a permissive license. This note did not check how `make trivy` and
`scripts/third-party-licenses.sh` classify Boost-1.0 or whether the second
ships files from `licenses/`.

Dependency facts that bear on maintenance:

- `modern-go/reflect2` (needed by `jsoniter`): Scorecard Maintained 0, no
  commit in 12 months. It reads Go runtime internals with
  `go:linkname` (5 files). It worked on Go 1.27.2 in every test here.
- `twitchyliquid64/golang-asm` (needed by `sonic` on amd64): Scorecard
  Maintained 0.
- `segmentio/asm`: Scorecard Maintained 0.
- `josharian/intern`: Scorecard Maintained 0, 0 commits in 90 days.

### Generated code (easyjson)

- The generator is `go run github.com/mailru/easyjson/easyjson@v0.9.2
  -all types.go`. It writes `types_easyjson.go` next to the types.
- Reproducible: three runs on the 151-line `types.go` of the benchmark gave
  the same 2,414-line file (SHA-256
  `d747fc58fba14cff9c204daa6dc2ac8b2debc4ce04fde30ca2bd1d8a319791ac`). The go
  command builds the generator from the module at the checksum above, so
  the generator binary follows the module checksum.
- At run time the generated code imports `github.com/mailru/easyjson`,
  `easyjson/jlexer`, `easyjson/jwriter`, `easyjson/buffer` and
  `github.com/josharian/intern`. `jlexer/bytestostr.go` uses `unsafe`
  unless the build has the `easyjson_nounsafe` tag.
- The generated methods are `MarshalJSON` and `UnmarshalJSON` on the types.
  Every other library, `encoding/json` too, then calls them. The first
  benchmark run of this note measured easyjson in every column by mistake
  for that reason. The final module keeps the easyjson copy of the types
  in a separate package (`ej`).


## 10. Maintenance cost

### When the Pulumi journal adds a field

The Pulumi CLI added data under the same journal version before (the
journaling research note, `docs/research/pulumi-journaling.md` on
`swarm/pulumi-journal-research`: entry kinds 8 and 9 and
`requiresByteString` under version 1). What each choice does with a new
member in a typed struct:

| Choice | Default | Strict mode | Keep it for a later re-encode |
| --- | --- | --- | --- |
| `std` | ignored | `Decoder.DisallowUnknownFields` rejects it | no tag option. A `json.RawMessage` field for the whole `state` keeps it, as the spike's `pulumijournal.Entry` does. |
| `stdv2`, `gjexp` | ignored | `RejectUnknownMembers(true)` rejects it | yes: a field with the `embed` option of type `jsontext.Value` or `map[string]T` (an "embedded fallback", `encoding/json/v2/doc.go`) keeps the unknown members |
| `easyjson` | ignored | no run-time option. The generator flag `-disallow_unknown_fields` builds it into the code. | yes: embed `easyjson.UnknownFieldsProxy` (`unknown_fields.go`) |
| `goccy`, `segmentio` | ignored | `Decoder.DisallowUnknownFields` | `json.RawMessage` only |
| `sonic` | ignored | `Config{DisallowUnknownFields: true}` | `json.RawMessage` only |
| `jsoniter` | ignored | `Config{DisallowUnknownFields: true}` | `json.RawMessage` only |

All of them pass the strict-mode case of section 7, except easyjson,
which has no run-time switch. With every choice, a new member that the
server must act on needs a code change (a new field). With easyjson, that
change also needs a generator run.

### Work per choice

| Choice | New dependency (ask first) | Build step | Upgrade risk |
| --- | --- | --- | --- |
| `std`, `stdv2` | no | none | Moves with the Go toolchain. In Go 1.27.2, `encoding/json` runs on the v2 code. This note did not check which Go release made that the default. Section 7 found one byte-level difference from the pre-v2 code on the cases tried. |
| `gjexp` | yes | none | On Go 1.27 it is an alias of `stdv2`, so it adds a module and no code. |
| `easyjson` | yes, plus `josharian/intern` | a generator run in `make generate`, and a CI diff check like `sqlc diff` | 2,414 generated lines for 151 lines of types in this benchmark. One commit in 12 months upstream. |
| `goccy` | yes | none | One maintainer wrote 98 of 101 commits in 12 months. v0.11 rewrote the decoder and encoder in September 2026. |
| `sonic` | yes, plus 5 modules on amd64 | none | Native code only below Go 1.28. Different code on amd64 (JIT) and arm64. Six linked modules. |
| `jsoniter` | yes, plus 2 modules | none | Archived. `reflect2` reads runtime internals with `go:linkname`. A Go upgrade can break it. An archived repository takes no new commits until its owner unarchives it. |
| `segmentio` | yes, plus `segmentio/asm` | none | One commit in 12 months. Scorecard Maintained 0. |

For generated code, `golangci-lint`, `nilaway` and `semgrep` in `make
check` would read `types_easyjson.go` too. This note did not run them on
generated code.


## 11. Comparison

Speed columns: median of 10 interleaved samples, quiet machine (section
5). "vs std" is the benchstat change against `std` when p < 0.05, and "~"
when benchstat finds no significant difference. Differences: value
differences from section 7 (of 54 cases).

| Candidate | Decode ckpt-1000, typed (vs std) | B/op | Decode envelope-1000 (vs std) | Differences (of 54) | `unsafe` / asm / cgo / JIT | License | Tested version, date | Last commit | Linked extra modules |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `std` | 29.88 ms | 8.2 MiB | 5.75 ms | reference | toolchain only | BSD-3-Clause | Go 1.27.2, 2026-10-02 | n/a | 0 |
| `stdlegacy` | 28.70 ms (~) | 8.1 MiB | 16.40 ms (+185%) | 0 (1 byte-only) | toolchain only | BSD-3-Clause | Go 1.27.2 with `GOEXPERIMENT=nojsonv2` | n/a | 0 |
| `stdv2` | 17.90 ms (-40%) | 7.4 MiB | 3.82 ms (-33%) | 11 (v2 defaults) | toolchain only | BSD-3-Clause | Go 1.27.2 | n/a | 0 |
| `gjexp` | 18.55 ms (-38%) | 7.4 MiB | 4.01 ms (-30%) | 11 (same code as `stdv2`) | no / no / no / no | BSD-3-Clause | pseudo-version, 2026-08-20 | 2026-08-20 | 0 |
| `easyjson` | 12.46 ms (-58%) | 7.6 MiB | 7.93 ms (+38%) | 7 | yes (2 files) / no / no / no | MIT | v0.9.2, 2026-03-14 | 2026-03-14 | 1 |
| `goccy` | 7.56 ms (-75%) | 6.5 MiB | 2.33 ms (-59%) | 0 | yes (68) / yes (6) / no / no | MIT | v0.11.1, 2026-09-27 | 2026-10-12 | 0 |
| `sonic` | 7.93 ms (-73%) | 8.7 MiB | 2.29 ms (-60%) | 9 | yes (69) / yes (32) / no / yes on amd64 | Apache-2.0 (+ Boost-1.0, MIT, BSD-3-Clause files) | v1.15.4, 2026-09-10 | 2026-09-26 | 5 on amd64, 2 on arm64 |
| `sonicstd` | 10.77 ms (-64%) | 10.0 MiB | 2.44 ms (-58%) | 3 | as `sonic` | as `sonic` | as `sonic` | as `sonic` | as `sonic` |
| `jsoniter` | 16.06 ms (-46%) | 8.9 MiB | 6.57 ms (~) | 3 | yes (22, +20 in `reflect2`) / `reflect2`: 9 / no / no | MIT (+ Apache-2.0 deps) | v1.1.12, 2021-09-11 | 2022-09-16, archived | 2 |
| `segmentio` | 38.66 ms (+29%) | 16.2 MiB | 10.31 ms (+79%) | 2, plus no depth limit | yes (34) / `segmentio/asm`: 19 / no / no | MIT | v0.5.4, 2026-03-10 | 2026-03-11 | 2 |


### Fits and does not fit the project rules

The rules: prefer the standard library, ask before a new dependency, no
hand-written parser, ADR 7 age, ADR 22 license.

- `std` (`encoding/json`). Fits every rule: standard library, no
  dependency, no parser of our own. On Go 1.27 it runs on the v2 code. It
  is the slowest typed decoder here except `segmentio`.
- `stdv2` (`encoding/json/v2`, `jsontext`). Fits every rule: standard
  library and a stable API in Go 1.27. Decode takes 34% to 54% less time
  than `std`. Its defaults differ from `std` in 11 of 54 cases: it rejects
  duplicate names and invalid UTF-8, matches names with case, and encodes
  `omitempty` and maps in other ways. Options can select the v1 behavior,
  which is what `encoding/json` does. `jsontext.Decoder` gives the #389
  lookup in about 14 KiB and 81 to 106 allocations, with no tree.
- `gjexp` (go-json-experiment). Needs a new dependency and gives nothing
  on Go 1.27: it is an alias of `stdv2`. Does not fit "prefer the standard
  library".
- `easyjson`. A generator, so not a hand-written parser. It needs a new
  dependency, a generate step and a CI diff check. It accepts invalid JSON
  inside unknown members, has no run-time strict mode, and its methods
  take over `encoding/json` for the same types. Upstream had 1 commit in
  12 months. License and ADR 7 age fit.
- `goccy`. Needs a new dependency. 0 differences on 54 cases and no fuzz
  finding in 2 x 2 minutes. Fastest decoder and lowest allocations here.
  Uses `unsafe` in 68 files and assembly. One person wrote 98 of 101
  commits in 12 months. v0.11.1 is 15 days old (ADR 7 needs 14) and
  belongs to a rewrite released in September 2026. License fits.
- `sonic`. Needs a new dependency plus 5 linked modules on amd64. Generates
  machine code at run time on amd64 and maps it executable. Builds its
  native code only below Go 1.28. `ConfigDefault` accepts some invalid
  input (9 differences, among them a truncated top-level string with a
  lost byte), and errors quote the input. `ConfigStd` has 3 differences.
  Licenses fit, with Boost-1.0 files that the license tooling did not
  check.
- `jsoniter`. Archived, last release 2021-09-11. Depends on `reflect2`,
  which reads runtime internals. It meets the license and age rules. The
  repository is archived (read-only), so upstream takes no new change, also
  none for a new Go version.
- `segmentio`. Needs a new dependency. Has no depth limit, and decode time
  grows about four times for each doubling of depth (80 KB took 42 s).
  Slower than `std` on every decode payload. 1 commit in 12 months. Does
  not fit a server that takes JSON from clients.
- Generators other than easyjson: none maintained (section 4).
- A token walk of our own on top of `jsontext` or `json.Decoder` is the
  approach that #389 proposes. It reads tokens from a library decoder and
  does not parse bytes itself. Whether that counts as a "hand-written
  parser" in the owner's sense is for the owner to say.



## 12. Gaps

- The benchmarks ran on one shared 4-core amd64 virtual machine. There are
  no arm64, darwin or windows numbers. sonic runs different code on
  arm64, so its amd64 numbers do not carry over.
- The payloads are generated, not recorded from real CLI runs. They follow
  the type definitions, but real states may have other value mixes.
- The journal benchmark covers kinds 0 and 1 only. Kinds 5 (`newSnapshot`)
  and 10 (version 2) are larger and were not generated.
- Decoding into the real `apitype` types was not measured. The types here
  keep the JSON names and omit `time.Time` fields (strings instead).
- The times in section 8 (segmentio nesting, hostile input) ran while other
  jobs used the machine. The growth rate is clear, the seconds are rough.
- Fuzzing ran 2 minutes per target and phase. It finds shallow
  differences, not all of them.
- The GitHub REST API (advisories per repository, releases, maintainers'
  permissions) was not reachable from this session. OSV and the Go
  vulnerability database stood in.
- This note did not run `make check` linters (`golangci-lint`, `nilaway`,
  `semgrep`, `trivy`) on code that imports a candidate or on easyjson's
  generated code. How `make trivy` and `make licenses` treat sonic's
  Boost-1.0 files is not known.
- `UseNumber` decoding (what `main` uses for OpenTofu state) was not
  benchmarked.
- The race detector was not used in the benchmarks. The earlier spike
  shows that `-race` changes the order of some libraries.


## Appendix A. Commands

All commands run in the spike worktree on branch `spike/json-libs-2`, with
`export PATH=$HOME/go/bin:$PATH GOTOOLCHAIN=go1.27.2
TMPDIR=<worktree>/.tmp`. `.tmp/` is not committed.

Setup and payloads:

```
cd bench
go generate ./ej                     # easyjson code for the copy of the types
go build -o ../.tmp/probe ./cmd/probe
../.tmp/probe gen ../.tmp/payloads   # writes the payload files
go test -c -o ../.tmp/bench.test .
GOEXPERIMENT=nojsonv2 go test -c -o ../.tmp/bench-legacy.test .
go test -run 'TestPayloads|TestExtract' -v .
```

Benchmarks (interleaved, 10 passes, `-count 1` per run, order rotated per
pass):

```
cd .tmp && OUT=quiet PASSES=10 ./quiet.sh   # the script is bench/quiet.sh
./benchstat -col /lib -filter '.name:Decode' quiet/results.txt
./benchstat -col /lib -filter '.name:Encode' quiet/results.txt
./benchstat -col /lib -filter '.name:Extract' quiet/results.txt
./benchstat -col /lib -format csv -filter '.name:Decode' quiet/results.txt > d.csv
python3 -I ../bench/mktables.py d.csv
```

Peak RSS and hostile input (one process each, median of 3 for RSS):

```
./probe decode LIB ckpt|tofu|any payloads/FILE.json
./probe extract NAME payloads/tofu-10000.json resources 9994 instances 0 attributes password
./probe extract NAME payloads/ckpt-10000.json deployment resources 9999 outputs result
timeout 120 ./probe hostile LIB deep-1000000|deepobj-1000000|empties-1000000|maps-1000000
```

Correctness and fuzzing:

```
CORRECTNESS_OUT=../.tmp/correctness.md go test -run TestCorrectnessTable .
GOEXPERIMENT=nojsonv2 CORRECTNESS_OUT=../.tmp/correctness-legacy.md go test -run TestCorrectnessTable .
EXTRACT_OUT=../.tmp/extract-invalid.md go test -run TestExtractInvalid .
go test -run '^$' -fuzz '^FuzzGoccy$' -fuzztime 2m -parallel 2 .   # one per target
SKIP_LENIENT=1 go test -run '^$' -fuzz '^FuzzGoccy$' -fuzztime 2m -parallel 2 .
```

Provenance:

```
go list -m -json MODULE@VERSION                     # Time
curl -sS https://api.securityscorecards.dev/projects/github.com/OWNER/REPO
curl -sS -d '{"package":{"name":"MODULE","ecosystem":"Go"}}' https://api.osv.dev/v1/query
curl -sS https://vuln.go.dev/index/modules.json
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 -show verbose ./...
go mod graph
GOOS=OS GOARCH=ARCH go list -deps -f '{{with .Module}}{{.Path}}@{{.Version}}{{end}}' ./cmd/only/LIB
curl -sS https://sum.golang.org/lookup/MODULE@VERSION
../.tmp/modsum REPO_CLONE MODULE VERSION TAG_OR_COMMIT [SUBDIR]
git clone --filter=blob:none https://github.com/OWNER/REPO
git log --since=2025-10-12 --format='%aN' | sort | uniq -c
git cat-file -t refs/tags/TAG; git cat-file -p TAG^{commit} | grep gpgsig
```

