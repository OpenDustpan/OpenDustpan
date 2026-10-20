---
rfc: 99
title: The API description and the generated SDKs
status: discussing
created: 2026-10-20
---

# RFC 99. The API description and the generated SDKs

Status: discussing. ADR 264 records the decisions of the project owner:
SDKs for Go, TypeScript and Python are generated from a published description
of the tool-neutral REST API, and the CLI and the providers are generated
from it with a version pin before 1.0. This RFC holds the rest. It decides
nothing. The only code is the types and the comparison in
`internal/api/apidesc`.

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
- The move of ADR 210 is not complete. Routes still sit under
  `/api/server/...` and `/api/inventory/...`. A description of
  `/api/dustpan/v1` alone would miss them.
- Routes outside `/api/dustpan/v1` by design (ADR 210 decision 2): the
  tool-compatible APIs, the probe paths, `/runner/v1`, `/oidc/...`,
  `/sign-in/{id}/callback`, `/mcp`, `/metrics` and `/chat/...`.
- The error shape is `{"code", "message", "errorCode", "errorArgs"}`
  (ADR 182). The JSON style is in ADR 210 decision 10.
- The route test of each adapter fails when a route has no action. It has
  an explicit list of public routes.

## Options

### 1. The format of the description

| Option | Gives | Costs |
| --- | --- | --- |
| A. OpenAPI 3.1, as JSON | The format that most generators read. The JSON Schema of 3.1 is current. The standard library writes and reads JSON. | Some older generators read 3.0 only. |
| B. OpenAPI 3.0.3, as JSON | The widest tool support. | An older JSON Schema dialect. A nullable field is awkward. |
| C. A format of our own | Fits our route table. | No generator reads it. We write each generator. |
| D. A schema language that compiles to OpenAPI | Types in one place. | A new tool in the build, and one more language to learn. |

### 2. The source of truth

| Option | Gives | Costs |
| --- | --- | --- |
| A. Generate the description from the route tables | The routes cannot drift. | The route tables hold no schemas, so the first document has no body types. |
| B. Write the description by hand, and test it against the route tables | Full control of the schemas. | A person must write each entry. A test stops drift, but only after the fact. |
| C. Both: generate paths, methods and actions from the route tables, and take the schemas from Go types that a route names | One source for everything. | Each route must name its types. This changes every adapter. |

### 3. The generators

The SDKs must pass the license rule (`make licenses`, `make trivy`), and no
dependency enters the module without the approval of the project owner. A
generator runs in the build or in CI, not inside the server binary. Candidates
to check against their repositories again at the time of the decision:

- Go: `oapi-codegen`, `ogen`.
- TypeScript: `openapi-typescript` with a small fetch client.
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

1. **Format: option 1A.** OpenAPI 3.1 as JSON. The standard library writes it.
2. **Source: option 2C in two slices.** The first slice generates paths,
   methods, actions and the public flag from the route tables, and describes
   each body as a generic JSON object. The second slice adds schemas from
   Go types. The first slice cannot make a typed SDK. It makes the drift test
   and the document, and it shows the shape of the problem early.
3. **One list of operations.** Each adapter returns its operations as
   `apidesc.Operation` values (method, path, actions, public). The server
   joins the lists. The description is built from the joined list.
4. **The drift test.** A test in the package that wires the server compares
   the joined list with the operations of the checked-in description with
   `apidesc.Diff`. It fails for a route with no entry, an entry with no route
   and an entry whose actions differ. `Diff` fails closed: a malformed or
   repeated operation is an error, not a match.
5. **The route.** `GET /api/dustpan/v1/openapi.json` serves the description.
   It is a public route, like `capabilities`, so a generator can read it
   without a token. The route lists names and shapes only, and no data of a
   namespace. The route test has one row on the public list, with this reason.
6. **Scope.** The description covers `/api/dustpan/v1` only. It includes the
   routes of ADR 210 that have moved. A route that has not moved yet is on a
   list of exceptions in the drift test, with the name of the move. The list
   shrinks to zero when the move ends.
7. **The version.** `info.version` is the release of the server. An SDK and
   the description carry the same version. A release builds and publishes
   both. Before 1.0, a client pins the exact version (ADR 264 decision 3).
8. **Generators.** Pick them in a later RFC round, after a trial that
   generates a client from the first-slice document and checks the licenses.
   Do not pick them before the first slice exists.
9. **Places.** Option 4A while the API changes before 1.0. Review the
   place again at 1.0.
10. **Docs.** The user documentation has a page when a person can use the
    first SDK (ADR 214).

## Open questions

1. Does "every tool-neutral route" in the owner's issue include the routes
   outside `/api/dustpan/v1` that are not tool-compatible, for example
   `/runner/v1`, `/metrics` and `/mcp`? Recommendation: no. Each has its own
   protocol or its own client (ADR 210 decision 2). The runner protocol can
   have its own description later.
2. Is the description public? Recommendation: yes, as written above. The
   alternative is an installation action, for example `installation:observe`,
   which would block a generator that has no token.
3. Does the document list a feature that this installation has turned off
   (ADR 211)? Recommendation: the checked-in description lists every
   operation, and the served one lists only the operations that the server
   mounts. The drift test compares the full list with the full route table.
4. Does a generated SDK handle `Idempotency-Key`, `If-Match` and the cursor of
   a list (ADR 210) in one place? Recommendation: yes, in a small layer on
   top of the generated code that we write once for each language.
5. Which finish does the move of ADR 210 need before the first slice?
   Recommendation: none. The exception list of proposal 6 handles it.
6. Which generator for each language? See proposal 8.

## Follow-up work

Each item is a separate issue with the label `needs-triage`:

- the first slice: build the description from the route tables, the drift
  test, the route and an e2e step that fetches it.
- schemas from Go types for the request and response bodies.
- the Go SDK and its smoke test against `opendustpan dev`.
- the TypeScript SDK and its smoke test.
- the Python SDK and its smoke test.
- the console reads the description or the TypeScript SDK.
- examples in the description, and the docs page.

## Outcome

Empty until the RFC is accepted or dropped.
