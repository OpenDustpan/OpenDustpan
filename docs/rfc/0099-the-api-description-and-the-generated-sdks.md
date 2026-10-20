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
acceptance, those answers go into an ADR. Question 6 is open. The only code
is the types and the comparison in `internal/api/apidesc`.

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
8. **Generators.** Pick them after a trial (question 6). The trial generates a
   client from the first-slice document and checks the licenses. Do not pick
   a generator before the first slice exists.
9. **Places.** Option 4A while the API changes before 1.0. `sdk/go` is its
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

6. **Which generator for each language?** Not decided. Each generator
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
