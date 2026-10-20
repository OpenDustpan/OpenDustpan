# Research: a trial of OpenAPI code generators for the Go, Python and TypeScript SDKs

Question: Which code generator makes the best client from the API description
for each SDK language, does one tool for all languages beat one tool for each
language, and which OpenAPI version do we target? (RFC 99 question 6, RFC 48
question 2, issues #966, #967 and #1195.)

Date: 2026-10-20. Branch: `trial/sdk-generators` (throwaway, not for merge).

This note gives facts and a recommendation. The project owner approves each
dependency.

## 1. Summary

- **There is no API description on `main` yet.** Slice 1 (#1188) is open, and
  `internal/api/apidesc` holds only the `Operation` type and `Diff`. The trial
  ran on a **fixture**: an OpenAPI 3.0.3 document and its 3.1.0 conversion,
  written from the shapes that ADR 210 (paths, cursor lists,
  `Idempotency-Key`, `If-Match`, `ETag`, JSON Merge Patch), ADR 182
  (`{"code","message","errorCode","errorArgs"}`) and ADR 211 (capabilities)
  fix. It has 12 operations and holds each construct of the trial at least
  once. The result is provisional until it runs on the real document.
  `scripts/` reruns the whole trial on any document.
- **Recommendation:**

  | Language | Generator | Version | License | Runtime dependencies of the generated code |
  | --- | --- | --- | --- | --- |
  | Go | oapi-codegen | `github.com/oapi-codegen/oapi-codegen/v2` v2.8.0 | Apache-2.0 | `github.com/oapi-codegen/runtime` v1.7.0 (Apache-2.0), `github.com/oapi-codegen/nullable` v1.2.0 (Apache-2.0), `github.com/apapsch/go-jsonmerge/v2` v2.0.0 (MIT), `github.com/google/uuid` v1.6.0 (BSD-3-Clause) |
  | Python | openapi-python-client | `openapi-python-client==0.29.1` | MIT | `httpx` 0.28.1 (BSD-3-Clause), `httpcore` 1.0.9 (BSD-3-Clause), `h11` 0.16.0 (MIT), `anyio` 4.15.1 (MIT), `idna` 3.20 (BSD-3-Clause), `attrs` 26.1.0 (MIT), **`certifi` 2026.7.22 (MPL-2.0)**, **`typing_extensions` 4.16.0 (PSF-2.0)** |
  | TypeScript | Hey API | `@hey-api/openapi-ts@0.99.0` with its bundled `@hey-api/client-fetch` | MIT | none: the fetch client is emitted as source into the package |

- **One tool for each language beats one tool for all languages.** OpenAPI
  Generator ran on all five languages with no edit of the document, but its Go
  client fails on any new answer field, which ADR 210 decision 8 calls an
  additive change. In 3.1 mode its Python and TypeScript clients change the
  order of positional arguments. Keep OpenAPI Generator as the way to make a
  C# or Java client later, as Kubernetes does for its non-Go clients.
- **Target OpenAPI 3.1.0**, limited to the part of 3.1 that maps one to one to
  3.0.3: `type: [X, "null"]` for a nullable field and `oneOf: [{$ref}, {type:
  "null"}]` for a nullable reference. Do not use other 3.1-only constructs.
  Each chosen tool read the 3.1 fixture and made the same client as from
  3.0.3. oapi-codegen calls its 3.1 support "initial", so the converter in
  `scripts/build_fixture.py` (3.0 to 3.1, about 15 lines in the other
  direction) is the fallback.
- **Two licenses need the owner:** `certifi` (MPL-2.0) and
  `typing_extensions` (PSF-2.0) are runtime dependencies of the Python SDK,
  through `httpx`. Neither is on the RFC 99 list. The OpenAPI Generator
  Python client also needs `typing_extensions`. Build-time only:
  `argparse` (Python-2.0, through `js-yaml`) in the npm tree of both
  TypeScript generators. Build-time dependencies are not shipped.
- **One open decision for the API, not for the generator:** a new value of
  an enum or of a discriminator (for example a new tool) fails decoding in
  both Python clients and in ogen. See section 6.

## 2. Method

- The fixture is in `2026-10-20-sdk-generator-trial/fixture/`, built by
  `scripts/build_fixture.py`. Both files pass `openapi-spec-validator`
  0.9.0.
- `scripts/generate.sh` runs every candidate on both files. Every tool has
  an exact version. Each version was released on or before 2026-10-06 (ADR
  7). The tools run in a sandbox only: `go run pkg@version`, `uvx --from
  pkg==version`, a pinned npm workspace (`scripts/node`, with a lock file),
  and the OpenAPI Generator jar from Maven Central with its SHA-1 checked. No
  server `go.mod` changed. `2026-10-20-sdk-generator-trial/go.mod` only
  fences the trial code off from the server module.
- Each run was done twice, and the trees were compared by SHA-256.
- Smoke tests (`transport/`) run each client against a fake server
  (`transport/go/smoke/fake_test.go` for Go, `transport/fakeserver.py` for
  Python and TypeScript). Each client gets a small hand-written transport,
  which is the shape of RFC 99 question 4: bearer token, an
  `Idempotency-Key` on each mutating request that has none, and retries of
  429, 502, 503 and 504 with the same key. The checks:
  1. `get_unit`: a discriminated `oneOf` (`details` by `tool`), two `null`
     fields, a typed `ETag`, the `Authorization` header, and one answer field
     that the fixture does not declare (`futureField`).
  2. `unknown_enum_and_discriminator`: `tool` and `details.tool` hold
     `ansible`, a value that the fixture does not list.
  3. `unknown_enum_only`: only `tool` holds `ansible`.
  4. `error_shape`: a 404 with `errorCode` and `errorArgs` (`string |
     integer` values).
  5. `pagination`: two pages through `pageToken` and `nextPageToken`.
  6. `merge_patch_null`: a `PATCH` with `application/merge-patch+json`,
     `If-Match`, and `"language": null` to clear a field (RFC 7396).
  7. `if_match_delete`: a required `If-Match` on `DELETE`.
  8. `retry_same_idempotency_key`: the first `POST` gets 503. The retry must
     send the same key.
- Go: `go vet`, `go test`, and govulncheck v1.8.0 on each client. Python: a
  venv for each client with packages no newer than 2026-10-06
  (`uv pip install --exclude-newer`). TypeScript: `tsc` 5.9.3 with `strict`
  and `noUncheckedIndexedAccess`, then Node 22.
- Not done: a smoke test against `opendustpan dev` (there is no route to
  test), `make trivy` and `make licenses` on SDK lock files (there is no SDK
  repository yet), and builds of the C# and Java samples (no .NET SDK in the
  sandbox; the Java sample was not built).

## 3. Go

| | oapi-codegen v2.8.0 | ogen v1.24.0 | OpenAPI Generator 7.25.0 (`go`) |
| --- | --- | --- | --- |
| Runs with no edit of the document, 3.0 / 3.1 | yes / yes (same output except two comments) | yes / yes (same output) | yes / yes (3.1 changes a field order and `*map` to `map`) |
| JSON | `encoding/json` | `github.com/go-faster/jx` (not `encoding/json`, which RFC 99 decides) | `encoding/json`, with a strict decoder |
| Smoke checks passed | 8 of 9 (its 9th check sends a raw merge-patch body). The failed check, a typed `null` in a merge patch, passed in a separate test with `nullable-type: true`. | 6 of 8 | 4 of 8 |
| New answer field | ignored | ignored | **fails** (`json: unknown field "futureField"`) on each model with required fields |
| New enum or discriminator value | decodes; an error only when the caller reads the union | **fails the call** | **fails the call** |
| Nullable | `*T` by default (cannot send `null`); `nullable.Nullable[T]` with `nullable-type: true` | `OptNilT` (absent, null, value) | `NullableT` with `SetXNil` |
| `oneOf` with a discriminator | a union type with `AsX`, `FromX`, `ValueByDiscriminator` | a sum type (`Type` + one field per variant) | tries each variant; ignores the mapping |
| Errors `{code, message, errorCode, errorArgs}` | `JSONDefault *Error`, typed `errorArgs` union | `*ErrorStatusCode` as `error`, typed | `GenericOpenAPIError.Model()`, typed |
| `Idempotency-Key`, `If-Match` | fields of a params struct | fields of a params struct | builder methods |
| Response `ETag` | typed (`Headers200.ETag`) | typed (`ETag OptString`) | only through `*http.Response` |
| Transport seam | `WithHTTPClient(doer)`, `WithRequestEditorFn` | `WithClient(ht.Client)`, plus a `SecuritySource` for the token | `Configuration.HTTPClient`; token through the context |
| Size | 1 file, 2,981 lines | 10 files, 8,742 lines | 28 Go files, 6,962 lines, plus docs |
| Modules linked by the client | 4 (listed in section 1) | 20, including `go.uber.org/zap`, `gopkg.in/yaml.v2` and `github.com/segmentio/asm` (**MIT-0**, not on the RFC 99 list) | 1: `gopkg.in/validator.v2` (Apache-2.0) |
| govulncheck v1.8.0 | none | none called (some in required modules) | none |
| Deterministic | yes | yes | yes |
| Maintenance | 5 releases in 12 months; 241 open issues, 81 open PRs; "mostly maintained in two people's free time" | 11 releases; 113 / 29; all releases by one person | 10 releases; about 5,000 / 634; a core team of 5 plus a committee per language (6 seats for Go) |

Configuration for the pick (`scripts/oapi-codegen.yaml` and
`scripts/oapi-codegen-nullable.yaml`): `models` and `client`,
`compatibility.always-prefix-enum-values: true` (without it the constant of
`"dark"` is `Dark`, and a later enum with the same value renames it), and
`output-options.nullable-type: true` (without it a merge patch cannot send
`null`; with it, the check passed in a separate run, and the client links
`github.com/oapi-codegen/nullable` v1.2.0).

Why oapi-codegen: it uses `encoding/json`, links four small permissive
modules, gives typed headers and typed errors, and is the only Go candidate
that decodes an answer from a newer server. Its risks: two part-time
maintainers who ask for sponsors, kin-openapi before v1, and 3.1 support that
its notes call "initial" (multi-type unions become `any`, so do not use them).

## 4. Python

| | openapi-python-client 0.29.1 | OpenAPI Generator 7.25.0 (`python`) |
| --- | --- | --- |
| Runs with no edit, 3.0 / 3.1 | yes / yes. `info.version` must be a PEP 440 version: `0.0.0-trial` broke the build of the package. | yes / yes. **In 3.1, path-level parameters come after operation-level ones**: `delete_backup(id, if_match)` becomes `delete_backup(if_match, id)`. The smoke test then sent the ID as `If-Match` and got 404. |
| Smoke checks passed (of 8) | 6 | 6 (3.0), 5 (3.1) |
| New answer field | kept in `additional_properties` | accepted |
| New enum value | **fails** (`ValueError: 'ansible' is not a valid Tool`) | **fails** (pydantic `ValidationError`) |
| Open enum `anyOf: [Tool, string]` | works: the field is `Tool \| str` | not tested |
| Nullable and merge patch | `UNSET` and `None` are different values, so `null` is sent | `None` is sent as `null` |
| Errors | `parsed` is `Error` for the default answer, typed | `ApiException.data` is the typed `Error` |
| Transport seam | an `httpx.BaseTransport` through `httpx_args` (sync and async) | none. The trial wrapped `ApiClient.call_api`. urllib3 `Retry` does not retry `POST` by default. |
| Size | 48 files, 4,692 lines | 32 files, 7,654 lines, plus docs, `setup.py`, `tox.ini` |
| Runtime dependencies | 8 (section 1); httpx 0.28.1 had no release for about 22 months | 8: `pydantic` 2.13.5, `pydantic-core`, `annotated-types`, `typing-inspection` (MIT), `typing_extensions` (PSF-2.0), `urllib3` 2.8.0 (MIT), `python-dateutil` (Apache-2.0 or BSD-3-Clause), `six` (MIT) |
| Generator dependencies (build time) | MIT, BSD, ISC; `certifi` MPL-2.0; `typing_extensions` PSF-2.0 | the jar (Apache-2.0); needs a JVM |
| Deterministic | yes | yes |
| Maintenance | 9 releases in 12 months; 90 / 36; before 1.0 (0.29.1 broke all custom templates); mostly one maintainer | as in section 3 |

Why openapi-python-client: a typed, small, readable client. httpx gives one
transport seam for sync and async. `UNSET` against `None` matches JSON Merge
Patch. It reads 3.1 officially. Its risks: one main maintainer, breaking
releases before 1.0 (pin exactly and keep the generated code in the
repository), httpx's slow release cadence, and the two licenses in section 1.

## 5. TypeScript

| | Hey API 0.99.0 | openapi-typescript 7.13.0 + openapi-fetch 0.17.0 | OpenAPI Generator 7.25.0 (`typescript-fetch`) |
| --- | --- | --- | --- |
| Runs with no edit, 3.0 / 3.1 | yes / yes (same output). Needs TypeScript 5.9.3 pinned: with `npx`, TypeScript 7 was picked and the generator crashed. | yes / yes (same output) | yes / yes. 3.1 changes the order of positional arguments in the docs. |
| Smoke checks passed (of 8) | 8 | 8 | 8 |
| `tsc --strict` | passes | passes | passes. With `stringEnums=true`, each discriminated union becomes `never`, so do not use that option. |
| New field, enum or discriminator value | accepted (no runtime validation) | accepted | accepted |
| Errors | `error` is typed as `Error` | `error` is typed as `Error` | `ResponseError`; the caller parses the body |
| Transport seam | `createConfig({ fetch })` | `createClient({ fetch })` | `new Configuration({ fetchApi })` |
| Size | 16 files, 2,573 lines | 1 file, 637 lines (types only) + the runtime package | 28 files, 3,404 lines |
| Runtime dependencies | none | `openapi-fetch` and `openapi-typescript-helpers` (MIT) | none |
| Build-time dependencies | 84 npm packages in the lock file for both npm tools: MIT, ISC, Apache-2.0, BSD-2-Clause, `argparse` (Python-2.0), `type-fest` (MIT or CC0-1.0) | same | the jar |
| Deterministic | yes | yes | yes |
| Maintenance | 61 releases in 12 months, many with breaking changes; before 1.0 ("pin an exact version"); 539 / 117; one npm maintainer | 2 and 3 releases in 12 months; 210 / 77; **the 2026 roadmap deprecates openapi-fetch** | as in section 3 |

Why Hey API: the generated package has no runtime dependency, its error and
body types are typed, and it passed every check. The frequent breaking
releases matter only when we upgrade, because the version is pinned and the
generated code is in the repository, so an upgrade is a reviewed diff.
openapi-typescript is the most-used tool (Octokit uses it for its types), but
its fetch client is deprecated. OpenAPI Generator `typescript-fetch` is the
fallback.

## 6. What must change in the description before generation works

1. **The description must exist, with typed bodies.** Slice 1 (#1188)
   describes each body as a generic JSON object, so every generator makes a
   client with no types. A useful SDK needs slice 2 (#1189).
2. **An explicit, stable `operationId` on each operation** (RFC 99 question
   7). Every tool names the method from it. Add `tags`: openapi-python-client
   and OpenAPI Generator group the methods by tag.
3. **`info.version` must be valid SemVer and PEP 440**, for example `0.1.0`.
   openapi-python-client copies it into `pyproject.toml`.
4. **Decide what a new enum value means.** Both Python clients and ogen fail
   to decode an answer with an enum value that the client does not know.
   ADR 210 decision 8 lists additive changes, but not a new enum value.
   Options: (a) a new value of an answer enum is a breaking change; or (b)
   each enum that can grow (tool, states, statuses) is open in the
   description, `anyOf: [{$ref: Tool}, {type: string}]`. Option (b) works in
   openapi-python-client and does not change the Go and TypeScript clients.
   The same question applies to a new discriminator value: a new kind of
   `details` fails in Python.
5. **Use one spelling for IDs in JSON field names.** The inventory adapter
   writes `unitID`. ADR 210 decision 10 says camelCase (`unitId`). Fix it
   before the first SDK, or every SDK carries both forms.
6. **Name the error schema something other than `Error`.** OpenAPI
   Generator renames it `ModelError` in TypeScript. A name such as
   `ErrorResponse` avoids that.
7. **Declare `Idempotency-Key` and `If-Match` as shared parameters**
   (`components.parameters`) on each operation that takes them, and declare
   `ETag` as a response header. Then the clients expose them as typed
   fields. The transport fills an empty `Idempotency-Key`.
8. **Declare `security: []` on public routes** (`capabilities`,
   `openapi.json`) and one bearer scheme for the rest.
9. **Nullable fields:** in 3.1 use only `type: [X, "null"]`, or `oneOf:
   [{$ref}, {type: "null"}]` for a reference. No other multi-type schema.

## 7. Benchmarks

- **Kubernetes:** client-go comes from in-house generators (`client-gen`
  and others) that read Go types, not OpenAPI. The other official clients
  (for example `kubernetes-client/python`) are made by OpenAPI Generator,
  version 7.25.0 for Python.
  [code-generator](https://github.com/kubernetes/code-generator),
  [python README](https://github.com/kubernetes-client/python/blob/master/kubernetes/README.md)
- **Stripe:** a private, closed-source generator from its OpenAPI document.
  The SDKs mix generated and hand-written code.
  [stripe/openapi](https://github.com/stripe/openapi),
  [stripe-node CONTRIBUTING](https://github.com/stripe/stripe-node/blob/master/CONTRIBUTING.md)
- **GitHub Octokit:** `@octokit/openapi-types` is built with
  openapi-typescript. The Go and .NET SDKs are built with Microsoft Kiota.
  Each language has its own tool.
  [openapi-types.ts](https://github.com/octokit/openapi-types.ts),
  [go-sdk](https://github.com/octokit/go-sdk)
- **Pulumi and AWS:** Pulumi generates its providers' SDKs from provider
  schemas with its own code generators (`tfgen`, `pulumi-gen-aws-native`).
  AWS generates its SDKs from Smithy models, not from OpenAPI.
  [pulumi-terraform-bridge](https://github.com/pulumi/pulumi-terraform-bridge),
  [smithy-go](https://github.com/aws/smithy-go)

None of these projects uses one general tool for its main SDKs. Each uses a
tool fit for each language, or its own generator, with a hand-written core
for transport. That matches the recommendation and RFC 99 question 4.

## 8. C# and Java (quality only)

OpenAPI Generator 7.25.0 made both with no edit of the document. Neither was
built in the trial.

- C# (`csharp`, `generichost`, net8.0): 49 source files, 11,835 lines.
  `Option<T>` for optional fields. It depends on `Microsoft.Extensions.Http`,
  `Hosting`, `Http.Polly` and `Net.Http.Headers` (MIT). **Not deterministic:**
  the `.sln` gets new GUIDs on each run. Set the project GUID options.
- Java (`java`, `native`): 36 source files, 8,216 lines. Jackson
  (Apache-2.0), `jackson-databind-nullable` (Apache-2.0), `jsr305`
  (BSD-3-Clause) and `jakarta.annotation-api` (EPL-2.0 or GPL-2.0 with the
  Classpath Exception; needs a license review). **Not deterministic:** a build
  timestamp in the README and in `ApiClient.java`. Set
  `hideGenerationTimestamp=true`.

Both run the same strict-decoding and enum checks as the Go and Python
clients of that tool. Test them before a C# or Java SDK.

## 9. Files on the branch

| Path | Content |
| --- | --- |
| `2026-10-20-sdk-generator-trial/fixture/` | the fixture in 3.0.3 and 3.1.0 |
| `.../scripts/build_fixture.py` | builds both files of the fixture |
| `.../scripts/generate.sh` | runs every generator with pinned versions |
| `.../scripts/*.yaml` | configuration of oapi-codegen, ogen and openapi-python-client |
| `.../scripts/node/` | pinned npm workspace (TypeScript 5.9.3, Hey API, openapi-typescript, openapi-fetch) with a lock file |
| `.../scripts/smoke-go.sh`, `smoke-python.sh`, `smoke-ts.sh` | the smoke tests |
| `.../transport/` | the hand-written transport sketches, the smoke checks and the fake servers |
| `.../generated/` | the output of each run, and the generator logs in `_logs/` |

Rerun: `python3 -I scripts/build_fixture.py fixture`, then
`scripts/generate.sh fixture <out> <openapi-generator jar> scripts/node`,
then each `smoke-*.sh` on `<out>`. To run on the real document, put it in
place of `fixture/openapi-3.1.json`.
