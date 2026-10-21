---
rfc: 99
title: The API description and the generated SDKs
status: discussing
created: 2026-10-20
---

# RFC 99. The API description and the generated SDKs

Status: discussing. ADR 264 records the decisions of the project owner: the
tool-neutral REST API has a published description in OpenAPI, a CI check
fails on drift, SDKs for Go, TypeScript and Python are generated from it with
a version pinned to the server, and the CLI and the providers are generated
from it with a version pin before 1.0. This RFC holds the rest. Questions 1
to 5 have an answer from the product manager (see "Questions"). On
acceptance, those answers go into an ADR. The owner answered question 6 on
2026-10-20: OpenDustpan owns its client generator. Amendment 1 records that
answer, the design that follows from it, and four open questions (A1 to A4).
The first slice of the description is in `internal/api/apidesc`. The ports
of the generator and of the conformance suite are in `internal/clientgen`,
with no implementation.

## Problem

A developer who calls the API writes each call by hand and learns of a
break at run time. The server has no document that lists its operations.
The owner wants one description, and generated clients from it.

Facts on `main` (checked 2026-10-20):

- Each HTTP adapter has its own `Route` type and its own `Routes()` method:
  `internal/api/server`, `inventory`, `objectcontext`, `profile`, `audit`,
  `runnerapi` and the tool adapters. The types are not the same type, so no
  code can list all routes of the server.
- A route declares its method, its path, its scope and its actions. It
  declares no request type and no response type. A handler reads and
  writes JSON by itself.
- The move of ADR 210 is not complete. Routes of four areas still sit
  outside `/api/dustpan/v1`: `/api/server`, `/api/inventory`, `/api/audit`
  and `/api/runners`. The routes of `/api/context` moved already. A description
  of `/api/dustpan/v1` alone would miss the four areas.
- Routes outside `/api/dustpan/v1` by design (ADR 210 decision 2): the
  tool-compatible APIs, the probe paths, `/runner/v1`, `/oidc/...`,
  `/sign-in/{id}/callback`, `/mcp`, `/metrics` and `/chat/...`.
- The error shape is `{"code", "message", "errorCode", "errorArgs"}`
  (ADR 182). The JSON style is in ADR 210 decision 10.
- The route test of each adapter fails when a route has no action. It has
  an explicit list of public routes.
- Some generators read OpenAPI 3.0 only. A generator that does not read 3.1
  forces a 3.0 document.

## Options

### 1. The format of the description

ADR 264 decision 2 fixes the format as an OpenAPI document. A format of our
own and a schema language that compiles to OpenAPI are excluded by that
decision. The version stays open.

| Option | Gives | Costs |
| --- | --- | --- |
| A. OpenAPI 3.1, as JSON | The JSON Schema of 3.1 is current. The standard library writes and reads JSON. | Some generators read 3.0 only. |
| B. OpenAPI 3.0.3, as JSON | The widest tool support. | An older JSON Schema dialect. A nullable field is awkward. |

### 2. The source of truth

| Option | Gives | Costs |
| --- | --- | --- |
| A. Generate the description from the route tables | The routes cannot drift. | The route tables hold no schemas, so the first document has no body types. |
| B. Write the description by hand, and test it against the route tables | Full control of the schemas. | A person must write each entry. A test stops drift, but only after the fact. |
| C. Both: generate paths, methods and actions from the route tables, and take the schemas from Go types that a route names | One source for everything. | Each route must name its types. This changes every adapter. |

### 3. The generators

Amendment 1 replaces this option: OpenDustpan owns its client generator
(owner decision, 2026-10-20).

The SDKs must pass the license rule (`make licenses`, `make trivy`), and no
dependency enters the module without the approval of the project owner. A
generator runs in the build or in CI, not inside the server binary. Check
these candidates against their repositories again at the time of the
decision:

- Go: `oapi-codegen`, `ogen`.
- TypeScript: `openapi-typescript` with a small HTTP client.
- Python: `openapi-python-client`.
- One tool for all three: OpenAPI Generator (Java).
- Hosted generators (a vendor builds the SDK): they add a vendor to the
  release path. This RFC does not recommend them.

### 4. Where the SDKs live

Amendment 1 replaces this option: one repository for each language (owner
decision, 2026-10-20).

| Option | Gives | Costs |
| --- | --- | --- |
| A. In this repository, in `sdk/go`, `sdk/typescript`, `sdk/python` | One pull request changes the API, the description and the SDKs. CI can check drift. | Release tags of three packages come from one repository. |
| B. One repository for each SDK | Each package has its own release page and tags. | A change of the API needs a pull request in four repositories. |
| C. The Go SDK in `pkg/`, the other two in their own repositories | The Go SDK follows ADR 35. | The Go client is part of the server module and its dependencies. |

## Proposal

1. **Format: option 1A.** OpenAPI 3.1 as JSON, unless the generator trial
   (question 6) shows that the chosen generators read 3.0 only. Then the
   document is 3.0.3 (option 1B). The version changes no other part of this
   RFC.
2. **Source: option 2C in two slices.** The first slice generates paths,
   methods, actions and the public flag from the route tables, and describes
   each body as a generic JSON object. The second slice adds schemas from
   Go types. The first slice cannot make a typed SDK. It makes the drift
   test and the document. The shape of the problem that it shows: all
   routes sit in many `Routes()` tables of different types, so the first
   slice must add one small `Operations() []apidesc.Operation` method next to
   each `Routes()`, and the second slice must add a body type to every route.
3. **One list of operations.** Each adapter has a small
   `Operations() []apidesc.Operation` method next to its `Routes()`. It
   returns the method, the path, the actions and the public flag of each
   route. `pkg/dustpan` joins the lists of the adapters that it mounts. The
   description is built from the joined list.
4. **The drift test.** A test in the package that wires the server builds
   the server with every module turned on and compares the joined list with
   the operations of the checked-in description with `apidesc.Diff`. It
   fails for a route with no entry, an entry with no route, and an entry
   whose path, actions or public flag differ. The path must match exactly,
   because a generator turns the name of a wildcard into the name of an SDK
   parameter. `Diff` fails closed: a malformed or repeated operation is an
   error, not a match. `Operation.Validate` allows only these values.
   The methods are GET, HEAD, POST, PUT, PATCH and DELETE. A path is under
   `/api/dustpan/v1/`. Each segment is a literal (`[a-z0-9-]+`) or a
   wildcard (`{name}` with `name` in `[a-z_][a-z0-9_]*`). `{name...}` and
   `{$}` are allowed in the last segment only. An action has the shape
   `<area>:<verb>` (ADR 210 decision 7). No action is empty or repeated. A public route has no actions. A route that is not public has at
   least one.
5. **The route.** `GET /api/dustpan/v1/openapi.json` serves the description.
   It is a public route, like `capabilities`, so a generator can read it
   without a token. It serves the same full document for each build: all
   modules, the same bytes as the checked-in file. The mount state of a
   module does not filter it (question 3). `info.version` is the version of
   the API description, not the version of the build. A filtered view, if a
   client needs one, sits behind `caller:read` (ADR 211) and is not part of
   this RFC. The route test has one row on the public list, with this
   reason.
6. **Scope.** The description covers `/api/dustpan/v1` only. The test of
   proposal 4 puts each route of the joined table in exactly one of three
   classes. A route in no class, or in more than one class, fails the test:
   1. The route is under `Prefix`, and `Diff` compares it.
   2. The route is on the list of ADR 210 decision 2. The test reuses the
      list of the route test. It does not keep a second list.
   3. The route is on the not-moved list, with its old path and the path
      that it will have. The list holds the routes of `/api/server`,
      `/api/inventory`, `/api/audit` and `/api/runners` that have not
      moved. It shrinks to zero when the move ends.
7. **The version.** `info.version` is the version of the API description. It
   changes when the description changes. Each SDK carries a version that is
   pinned to the version of the server (ADR 264 decision 4). A release builds
   and publishes the description and the SDKs together. Before 1.0, a client
   pins the exact version (ADR 264 decision 5).
8. **Generators.** Replaced by amendment 1. Pick them after a trial (question 6). The trial generates a
   client from the first-slice document and checks the licenses. Do not pick
   a generator before the first slice exists.
9. **Places.** Replaced by amendment 1. Option 4A while the API changes before 1.0. `sdk/go` is its
   own Go module, so that the server `go.mod` does not hold its dependencies.
   Review the place again at 1.0.
10. **Docs.** The user documentation has a page when a person can use the
    first SDK (ADR 214). The first SDK issue ships that page.

## Threat notes

The public route is security code. The steps are those of ADR 250. Each step
needs its own test.

| Step | Note |
| --- | --- |
| 1. Check the input | The route takes no input except the method and the path. `Operation.Validate` is an allowlist for the operations that enter the document (proposal 4). |
| 2. Encode or contain | The server writes the document with `encoding/json` from operations that passed `Validate`. The route serves fixed bytes and builds no response from the request. |
| 3. Limit the effect | The document names routes, actions and shapes. It holds no data of a namespace, no setting and no secret. Every caller gets the same bytes, so the mount state of a module does not leak. The size is fixed at build time. The route is read-only and uses the common limits for the rate and for the time. |
| 4. Check authorization | The route is on the explicit public list of the route test, with a reason. Each other route keeps its own actions. |
| 5. Fail closed | A route that is malformed, repeated or in no class of proposal 6 fails the drift test. The build fails, and the server does not serve a partial document. |

## Questions

### Decided by the product manager

The product manager decided questions 1 to 5 under the delegation of the
owner of 20 October 2026. They go into an ADR when this RFC is accepted. Each
has a benchmark.

1. **Scope.** The description covers `/api/dustpan/v1` only. The runner
   protocol, `/mcp` and `/metrics` keep their own protocols (ADR 210
   decision 2). The runner protocol can have its own description later.
   Benchmark: the Kubernetes `/openapi/v3` description excludes `/metrics`
   and the health paths. The REST description of GitHub excludes the Git
   protocol and the webhooks.
2. **Public.** `GET /api/dustpan/v1/openapi.json` is public and serves the
   full document (proposal 5). Benchmark: GitHub and Stripe publish their
   descriptions openly, and the source of this project is open.
3. **The mount state.** The served document, the checked-in document and the
   list of operations in the drift test are the same: all operations. A
   client reads `/api/dustpan/v1/capabilities` (behind `caller:read`) for the
   mount state of a module (ADR 211). Benchmark: Stripe and GitHub publish
   one document for all products.
4. **The transport layer.** Each SDK has one small hand-written transport
   layer for `Idempotency-Key`, `If-Match` and the cursor of a list (ADR 210).
   The smoke test of each SDK tests it. Benchmark: `client-go` has a
   generated clientset and hand-written `rest` helpers.
5. **The writer.** The server writes the JSON with `encoding/json`, with
   sorted keys, and a golden-file test checks it. The choice between 3.1 and
   3.0.3 waits for the generator trial (proposal 1).

### Open

6. **Which generator for each language?** Answered by the owner on
   2026-10-20: OpenDustpan owns its generator (amendment 1). The text below
   is the question as it was before that answer. Not decided then. Each generator
   dependency needs the approval of the owner after a trial (issue 1195).
   The trial criteria:
   - The generator makes the client with no hand edit of the generated files.
   - The client compiles or type-checks, and a smoke test passes against
     `opendustpan dev`.
   - The licenses of the generator, of the runtime dependencies of the
     generated code and of the generated code are known. MIT, BSD-2-Clause,
     BSD-3-Clause, Apache-2.0 and ISC are accepted. GPL, LGPL, AGPL, SSPL,
     BUSL and an unknown license are refused.
   - `make trivy` and `make licenses` see the lock files of the SDK.
   - `govulncheck` reports nothing for the Go SDK.
   - Two runs on the same document give the same files.
   - The build tool is pinned and sits outside the `go.mod` of the server.
     `sdk/go` is its own module.
7. **Does each operation have an explicit, stable `operationId`?** A generator
   names an SDK method from it. Recommendation: yes, and `Diff` compares it.
   Without it, a generator derives the name from the path, and a change of
   the path renames the method. The cost: each route declares a name.
8. **Does the description give the object kind of each action (ADR 17)?**
   The route declares an action and the object it applies to. Recommendation:
   yes, as an extension field of the operation, so that a generated CLI can
   say what a caller needs. The cost: each route declares the object kind.

## Amendment 1: OpenDustpan owns its client generator (2026-10-21)

On 2026-10-20 the project owner answered question 6 with a different
approach: OpenDustpan writes its own client generator, and it uses no
third-party OpenAPI generator. The owner's decisions are in issue 966 and
issue 1281. This amendment records them, describes the design that follows
from them, and lists the questions that stay open. The trial of third-party
generators is on the branch `trial/sdk-generators`
(`docs/research/2026-10-20-sdk-generator-trial.md`). Its findings about the
description and about client behavior still apply. Its choice of tools does
not.

This amendment uses "client" for a generated client library, as the owner's
decisions do. ADR 35 uses "SDK" for the server SDK in `pkg/`, and the two
are different things. The earlier sections of this RFC say "SDK" for a
client, and they keep that word.

### What the amendment changes in this RFC

- **Question 6** has an answer: the owner decisions below. The trial
  criteria of question 6 for third-party generators do not apply.
- **Option 3 and proposal 8** no longer apply. The project picks no
  third-party generator, so no generator needs approval. The generator is code of this
  project under its own license.
- **Option 4 and proposal 9** no longer apply. Each client has its own
  repository (decision O7). No client lives in `sdk/` of this repository.
- **Proposal 7** stays, with the versioning rule of question A4 below.
- **Question 4 of the decided questions** (the transport layer) stays. The
  generator does not write the transport. A person writes it once for each
  language, and the conformance suite tests it.
- ADR 264 decision 4 asks that "the license of each generator must pass the
  license policy of the project". The generator uses the Go standard library
  only, and it is under the license of this project, so it passes.

### Owner decisions

These decisions are final. This amendment does not open them again.

**O1. One generator of our own (owner decision, 2026-10-20).** One
deterministic program, written in Go, generates every client. It has three
parts:

- A shared parser. It reads only the subset of OpenAPI that the description
  uses. That subset is OpenAPI 3.1, limited to the constructs that map one to
  one to OpenAPI 3.0.3. It also holds open enums, the `ErrorResponse` schema,
  the shared `Idempotency-Key` and `If-Match` parameters and the `ETag`
  response header.
- One template set for each language.
- Golden-file tests. The same description always gives the same bytes.

**O2. The templates come from good clients with permissive licenses (owner
decision, 2026-10-20).** An agent writes the template set of each language
one time. The templates follow the patterns of well-known clients with
permissive licenses, for example go-github, the Stripe libraries and Octokit.
Code that is copied keeps its license notice. No code comes from a GPL
project.

**O3. One shared conformance suite (owner decision, 2026-10-20).** One suite
runs every client against a real server in CI. It covers each operation, the
error shape, pagination, `Idempotency-Key`, `If-Match` and `ETag`, and
unknown fields and unknown enum values. A client release needs a pass.

**O4. Each client uses what is standard for its language (owner decision,
2026-10-20).** Each client uses the usual build tool, the usual package
layout and the usual HTTP and JSON libraries of its language. The goal is
that a team that adopts a client has no problem with it. When the standard
choice makes the work of this project harder (for example Gradle), the
client still uses it. Dependencies that are standard for a language are
approved in advance. The license gate of this repository does not apply in a
client repository, because the project does not distribute the dependencies
of a client.

**O5. The standard library comes first (owner decision, 2026-10-20).** Where
the standard library of a language has HTTP and JSON, the client uses only
the standard library. A client adds a dependency only where the language has
no standard HTTP or no standard JSON. Its README says why.

**O6. The languages and their order (owner decision, 2026-10-20).**

| Language | HTTP | JSON | Build and release | When |
| --- | --- | --- | --- | --- |
| Go | `net/http` | `encoding/json` | Go modules, Git tags | first |
| Python | `urllib.request`, with an optional `[httpx]` extra | `json` | `pyproject.toml`, PyPI | second |
| TypeScript | global `fetch` (Node 18 and later, browsers, Deno, Bun) | `JSON` | npm | third |
| C# | `HttpClient` | `System.Text.Json` | `dotnet pack`, `dotnet nuget push` | later |
| Java | `HttpURLConnection`, no `java.net.http` | `jackson-core` streaming API, with explicit generated code for each model | Maven | deferred until enough users ask |
| Swift | `URLSession` | `Codable` | Swift Package Manager, Git tags | after the API is stable |
| Rust | `ureq` | `serde`, `serde_json` | crates.io | after the API is stable |
| Zig | `std.http.Client` | `std.json` | Git tags | after the API is stable |
| C | not decided | not decided | not decided | when C has an official package registry |

- **Python.** `pip install opendustpan` has no dependencies and uses the
  synchronous `urllib.request`. `pip install opendustpan[httpx]` adds httpx,
  which gives an async client and connection pooling. The hand-written
  transport selects the transport. The generated code is the same for both.
  The conformance suite runs on both transports. The extra keeps a wide
  httpx version range, so that it does not conflict with the pins of other
  libraries. This is the pattern of the Stripe Python library.
- **Java.** The generated code reads and writes JSON with the streaming API
  of `jackson-core`, with no `jackson-databind` and no reflection. The JAR
  works on Android, so the transport does not use `java.net.http`. The rest
  of the Java plan in issue 1281 stays as the plan for when the work starts.
- **Zig.** The owner said that Zig comes only if a generator path exists.
  With the generator of this project, a Zig client is one more template set,
  so the generator meets that condition. Zig still waits until the API is stable.

**O7. One repository for each language (owner decision, 2026-10-20).** The
first repository is `client-go`, with the module path
`opendustpan.org/client`. Then come `client-python` and `client-node`. The
npm packages go in the npm organization `opendustpan`. The project claims
the PyPI name at the first real release.

### The design of the generator

```text
openapi.json --> parser --> model --> template set of a language --> files --> writer
                                         (one per language)
```

The Go sketch of the ports is in `internal/clientgen` (package
`clientgen`), with the model in `internal/clientgen/model`. It has types and
interfaces only, and no implementation. Its place in the repository depends
on question A1. A depguard rule keeps both packages away from the server
code, so a later move is a move of files.

**The input.** The generator reads only the bytes of the published
description: `internal/api/apidesc/openapi.json`, which the server serves at
`GET /api/dustpan/v1/openapi.json`. It does not import `internal/api/apidesc`
or any other server package. A team that downloads the description from a
server gets the same client as the project.

**The subset.** The parser reads the constructs below and nothing else. A
construct that is not in the list is an error (`ErrUnsupported`), with the
JSON pointer of the construct. The parser never skips a construct and never
guesses its meaning.

| Part | Constructs that the parser reads |
| --- | --- |
| Document | `openapi: 3.1.0`, `info` (`title`, `version`, `description`), `servers`, `tags`, `paths`, `components`, top-level `security` |
| Operation | `operationId`, `tags`, `summary`, `description`, `parameters`, `requestBody`, `responses`, `security` (empty for a public route), `x-dustpan-actions` |
| Parameter | `in: path`, `in: query`, `in: header`. A `$ref` to `#/components/parameters/IdempotencyKey`, `IfMatch` or `IfMatchRequired`. A scalar schema, or an array of a scalar schema. |
| Request body | `application/json` and `application/merge-patch+json`, each with one schema |
| Response | one success answer (a status code or `2XX`), `default` with a `$ref` to the error schema, the `ETag` header as a `$ref` to `#/components/headers/ETag` |
| Schema | `type` `object`, `string`, `integer`, `number`, `boolean`, `array`. `properties`, `required`, `additionalProperties`, `items`, `enum`, `format`, `description`, `minLength`, `maxLength`. A local `$ref` to `#/components/schemas/...`. |
| Nullable | `type: [X, "null"]`, and `oneOf: [{$ref}, {type: "null"}]` for a reference. No other multi-type schema. |
| Open enum | `anyOf: [{$ref: <enum>}, {type: string}]`. A client keeps a value that it does not know, and does not fail. |
| Error | `#/components/schemas/ErrorResponse` with `code`, `message`, `errorCode`, `errorArgs` |

The subset grows only in a pull request that adds the construct to the
parser, to the golden files and, where the construct changes behavior on the
wire, to the conformance suite. A discriminated `oneOf`, for example, is not
in the subset until the description needs it.

**Pagination.** A list operation pages with the names of ADR 210 decision 10:
`pageSize` and `pageToken` in the query, and `nextPageToken` in the answer.
The parser marks an operation as a list (`model.Page`) only when it finds all
three, and the items field is the one array field of the answer. When it
finds only a part of the names, the description is not valid. This needs the
typed bodies of issue 1189.

**The model.** The model holds what a template needs and no OpenAPI syntax.
Every list in it has a fixed order: operations by ID, schemas by name,
fields by wire name, parameters by the order of the path and then by name.
The model holds no Go map, so a template that ranges over the model gives
the same bytes on each run.

**Names.** A name from the description becomes an identifier only when it
matches an allowlist: lowerCamelCase for an operation ID, UpperCamelCase for
a schema, and camelCase for a field. Each template set has a table of the
reserved words of its language and one fixed rule that changes such a name,
for example an added underscore. A name that the rule cannot make valid is an
error (`ErrInvalidName`). The wire name of each field stays in the generated
code, so the identifier and the wire name can differ.

**The templates.** The template sets use `text/template` from the Go
standard library. `text/template` does not encode a value by itself, so each
template set gives functions that encode for its language: a string literal,
a comment line, an identifier. A template writes a value from the
description only through one of these functions. A golden-file test with
hostile names and texts checks each function.

**Generated and hand-written code.** The generator writes the models, the
operations and the adapter of the conformance suite. A person writes the
transport once for each language: the base URL, the token, the
`Idempotency-Key` that a mutating call gets when the caller gives none, the
retries with the same key, the cursor of a list, and the translation of
errors. Each generated file starts with a header that names the generator
version and the description version. In Go, the header follows the standard
form `// Code generated ... DO NOT EDIT.`. The writer changes no file that
has no such header, so it never overwrites the transport.

**Determinism.** The generator does not read the clock, the environment,
the host name or the order of a Go map. It writes no time stamp and no
absolute path. A test runs the generator two times on the same input and
compares the bytes. The golden files of each language are in this
repository, and a test fails when the output differs from them.

### The design of the conformance suite

The Go sketch of the contract is in `internal/clientgen/conformance`: the
protocol messages, the ports of the harness and the list of cases. The cases
have names and no body. A case with no body fails a release (decision O3).

```text
harness (Go) --stdin/stdout, JSON-RPC 2.0--> adapter of one client
     |                                              |
     |                                              v
     +--- controls ---> recording proxy <--HTTP-- generated client + transport
                              |
                              v
                     real server (opendustpan dev)
```

- **The server.** The harness starts `opendustpan dev` of the release that
  the client was generated from, with an empty database, on a port of the
  loopback address. It makes one access token for the run.
- **The proxy.** A recording proxy sits between the client and the server.
  It records each request (the method, the path, the query, the headers that
  the suite checks, and the body). It can also change one exchange: answer a
  503, drop an answer after the server ran the operation, add a field that
  the description does not declare, or put an enum value that the
  description does not list. The real server never sends an unknown field or
  value, so the proxy is the only way to test them against a real server.
- **The adapter.** A small program for each client. The generator writes the
  table that maps an operation ID to a call of the client. A person writes
  the loop that reads and writes the messages. The adapter has four methods:
  `initialize`, `call`, `paginate` and `shutdown`. Each message is one line
  of JSON, at most 1 MiB long.
- **The cases.** Seven groups: operations, errors, pagination, idempotency,
  concurrency, compatibility and version. The case
  `every-operation-is-called` calls each operation of the description at
  least one time, so a new operation with no call fails the suite.
- **Transports.** A client with more than one transport (Python) runs the
  full suite on each transport.

### Threat notes for the generator and the suite

The generator reads a document and writes source code, and the harness
starts processes and opens connections, so both are security code. The steps
are those of ADR 250. Each step needs its own test.

| Step | The generator | The harness |
| --- | --- | --- |
| 1. Check the input | The parser is an allowlist of constructs and names. It refuses a document larger than a fixed size, an unknown key, an external `$ref` and a name that is not on the allowlist. | The harness refuses a message that is not valid JSON, that is longer than 1 MiB, or that has an unknown ID. |
| 2. Encode or contain | A value goes into source only through the encoder of the language. The writer writes files through `os.Root` in the output directory. | The harness starts the adapter with `exec.Command` and an argument list, never a shell. |
| 3. Limit the effect | The generator uses no network. It writes only in the output directory. | Each call has a time-out. The proxy and the server listen on the loopback address only. The token belongs to a throwaway database. |
| 4. Check authorization | A generated client sends the token only to its base URL. A case checks that a redirect to another host gets no token. | The token is a test token of the run. It is never written to a log or to a report. |
| 5. Fail closed | An unknown construct or a bad name stops the run with no partial output. | A protocol error, a case with no body, or an adapter that exits early fails the run. A release workflow refuses to publish without a pass. |

### Open questions

Each question has options and a recommendation for the product manager.

#### A1. Where does the generator live?

| Option | Gives | Costs |
| --- | --- | --- |
| A. This repository, in the server module: packages under `internal/clientgen`, and a `main` package under `cmd/` | One pull request changes a route, the description, the generator and the golden files. `make check` covers the generator with no new setup. A depguard rule keeps it away from server code. | A client repository that runs the harness downloads the server module, or uses a binary that the release attaches. |
| B. This repository, in a Go module of its own (for example `tools/clientgen/go.mod`) | The same single pull request. The module boundary blocks each import of server code. `go run ...@version` does not download the dependencies of the server. | `make check` must cover a second module: lint, nilaway, govulncheck and tests. A nested module needs tags with a path prefix. |
| C. A repository of its own (`opendustpan/client-generator`) | Its own tags and release page. | A change of the description that needs a generator change is two pull requests in two repositories. The golden test of the generator reads a description from another repository. The owner must create the repository. |

**Recommendation: A.** Most changes of the generator come from a change of
the description, and A keeps both in one pull request and under one gate. The
depguard rule makes a later move to B or C a move of files, as it does for
the Pulumi journal today. The release attaches the harness binary, so a
client repository does not download the server module.

Benchmark: Kubernetes keeps its client generators (`code-generator`) apart
from `client-go`, and it publishes both from the main repository. Pulumi
keeps its SDK code generators in the main `pulumi/pulumi` repository. Check
both again at the time of the decision.

#### A2. How does each language repository run the conformance suite?

| Option | Gives | Costs |
| --- | --- | --- |
| A. One Go harness that drives a small adapter for each language over JSON-RPC 2.0 on stdin and stdout | A person writes each case one time. A new case covers every language on the day it lands. The harness controls the proxy, so faults are the same for each language. One report format. | Each language has an adapter program. The protocol is a contract with a version. The adapter tests the client through a fixed shape, not through the idioms of the language (an iterator, an async call). |
| B. A port of the suite in each language | Each repository uses its own test tools. The tests read like the code of a user. | One suite for each language, which can drift. A new case needs a pull request in each repository. A pass in two languages does not mean the same thing. |
| C. Case files in JSON that a runner in each language reads | One source of cases. Native test tools. | Retries, faults and pagination need logic in each runner. That is a part of the harness written again in each language. |

**Recommendation: A**, with a short native smoke test in each client
repository for the idioms that the adapter does not reach (for example the
async client of Python). The generator writes the operation table of each
adapter, so the hand-written part of an adapter is the message loop only.
`initialize` carries a protocol version, and the harness refuses an adapter
with another version. The release of this repository attaches the harness
binary and the server binary, and a client repository pins both by version.

Benchmark: the language conformance tests of Pulumi run one Go harness
against the language host of each SDK over gRPC. The integration tests of
Apache Arrow drive each implementation from one harness. The Language Server
Protocol uses JSON-RPC 2.0 over stdin and stdout. Smithy protocol tests are
option C. Check each again at the time of the decision.

#### A3. How does a change of the description reach the client repositories?

| Option | Gives | Costs |
| --- | --- | --- |
| A. A bot pull request in each client repository for each server release | The release workflow of this repository generates each client from the released description and opens a pull request in each client repository. The CI of the client runs the suite against that server release. A person merges and releases. | A break shows in a client only at a release. Needs a credential that can open pull requests in the client repositories. |
| B. A bot pull request for each change of the description on `main` | Early signal. | Many pull requests before 1.0. A client follows a description that no release shipped. |
| C. Each client repository checks for a new release on a schedule | No credential in this repository. | A delay. Each repository needs the same scheduled workflow. |

**Recommendation: A, plus a check before the merge in this repository.** A
pull request in this repository that changes the description, the generator
or a template runs the golden tests, builds the Go client and runs the suite
for Go against the build of that pull request. So most breaks show before
the merge, and the bot opens pull requests only for releases. The credential
is a GitHub App with write access to the client repositories only. The owner
creates it.

Benchmark: the client repositories of Twilio and Stripe get commits of
regenerated code from an automated process when the API description
changes. Check this again at the time of the decision.

#### A4. How do client versions relate to the description version?

| Option | Gives | Costs |
| --- | --- | --- |
| A. The client version is the description version | One number. | A fix of the transport needs a new description version. A description patch with no change for one client still forces a release. |
| B. The client takes the major and minor version of the description, and the patch version is its own | The pin of ADR 270 decision 9 (`>=0.12.0 <0.13.0`) maps to the client version. A transport fix is a client patch. The version is valid SemVer and PEP 440. | The patch numbers of two clients differ. The client must carry the exact description version in its metadata. |
| C. The client version is the server version | It reads as ADR 264 decision 4 says. | A server release with no change of the API forces a client release. A client fix waits for a server release. |
| D. A version of its own, with a compatibility table | Each client is free. | A table to keep. A user cannot see the API version in the client version. |

**Recommendation: B.** Each client carries, in its metadata and in its user
agent, the exact description version and the server release that it was
generated from. That meets ADR 264 decision 4 (a version pinned to the
server) and ADR 270 decision 9 (a pin on the description). The ADR that
accepts this amendment states that reading. Before 1.0, each description
minor version can break a client, so the client minor version follows it.

Benchmark: the major version of the Kubernetes Python client follows the
minor version of Kubernetes (client 31 for Kubernetes 1.31). go-github has its own major version and
gives the API version in its README. Check both again at the time of the
decision.

### Changes to the description before the first client

From the trial (section 6 of the trial notes). The first slice already gives
a stable `operationId`, `tags`, a valid `info.version`, `ErrorResponse`, the
shared parameters, the `ETag` header and `security: []` on public routes.
Still to do:

- Typed bodies (issue 1189). Without them, every client has untyped bodies.
- Open enums: each enum that can grow (a tool, a state, a status) is an open
  enum of the subset.
- One spelling for IDs in field names (`unitId`, not `unitID`).

### Follow-up work for the amendment

Each item is a separate issue with the label `needs-triage`:

- The conformance suite: the harness, the proxy, the case bodies and the Go
  adapter. This is the first slice, as the owner said.
- The parser and the model, with golden files of the model.
- The Go template set and `client-go`.
- The Python template set, both transports and `client-python`.
- The TypeScript template set and `client-node`.
- The bot pull requests and the release steps (question A3).
- An ADR that records decisions O1 to O7 and the answers to A1 to A4.

## Follow-up work

Each item is a separate issue with the label `needs-triage`:

- #1188 the first slice: build the description from the route tables, the
  drift test, the route and an e2e step that reads it.
- #1189 schemas from Go types for the request and response bodies.
- #1195 the generator trial and the license check.
- #1190 the Go SDK, its smoke test and the docs page.
- #1191 the TypeScript SDK and its smoke test.
- #1192 the Python SDK and its smoke test.
- examples in the description (needs #1189).
- the console reads the description or the TypeScript SDK (a task of the
  console repository).

## Outcome

Empty until the RFC is accepted or dropped.
