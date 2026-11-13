# API description

The server publishes a machine-readable description of its tool-neutral REST
API. A generator can read it and make a client. A person can read it to find
the routes, the actions and the headers of the API.

The description is an OpenAPI document in JSON. It covers the paths under
`/api/dustpan/v1` only. It does not cover the Pulumi Cloud-compatible API, the
TFE-compatible API, the OpenTofu HTTP backend or the runner protocol. Each of
those keeps the shapes that its tool expects.

## Get the description

```sh
curl -s https://dustpan.example.com/api/dustpan/v1/openapi.json
```

The route needs no sign-in. It answers `GET` and `HEAD`. Any other method gets 405.

The server sends the same bytes on each request, on each build of the same
version, and with each set of modules. A module that is off does not change the
document. To find which modules are on, read `GET /api/dustpan/v1/capabilities`.

The document holds route names, actions and shapes. It holds no data of a
namespace, no setting and no secret.

## Format

| Part | Value |
| --- | --- |
| `openapi` | `3.1.0` |
| `info.version` | The version of the description, for example `0.2.1`. It is not the version of the server. Until the first release, it stays at one value when the description changes. It is valid as a SemVer version and as a PEP 440 version. |
| `servers` | One relative server, `/` |

The document uses the part of OpenAPI 3.1.0 that maps one to one to 3.0.3, so
a generator that reads 3.0.3 only can read it:

- A type that can be null is `type: [X, "null"]`.
- A reference that can be null is `oneOf: [ref, null]`.
- No other type has more than one member.
- No keyword that exists in 3.1.0 only is in use.

The server writes the document with sorted keys, so two documents of the same
routes are the same bytes.

## Operations

Each operation has these members:

| Member | Content |
| --- | --- |
| `operationId` | A stable name in lowerCamelCase, for example `listUnits`. A generator names an SDK method from it. A change of a path does not change it. No two operations share one. |
| `tags` | One to three tags. The first tag names the group of the operation. |
| `x-dustpan-actions` | The actions that the route declares, each with the kind of the object that it applies to, for example `{"action": "unit:lock", "object": "collection"}`. The kind is `namespace`, `unit`, `collection`, `caller` or `server`. The list is empty for a route that needs no sign-in. |
| `parameters` | One path parameter for each wildcard of the path. One query parameter for each query value that the route reads. A reference to a shared header parameter when the route takes the header. |
| `requestBody` | A reference to the schema of the body, for a route that reads a JSON body. A `PATCH` takes `application/merge-patch+json`. A route that reads no body has no `requestBody`. |
| `responses` | One key for each success status code that the route answers, for example `200`, `201`, `202` or `204`. Most routes have one key. A route that answers `201` for a first write and `200` for a replacement has both keys. Each success answer has a reference to the schema of the answer when the answer has a body. A `204` answer has no body. The description never has the key `2XX` or another range. The key `default` is for an error. |
| `security` | `[]` for a route that needs no sign-in. A route that needs sign-in has no `security` member and uses the default. |

The actions are those that the route declares. A handler can check more, for
example an action on an object that it finds from the request.

A route that takes a file as raw bytes, for example the upload of a provider
file to the registry, has no `requestBody`, because the description holds JSON
bodies only.

## Query parameters

A query parameter has `in: query`, a `name` in camelCase, `required`, a
`description` and a `schema`. The schema is `string`, `integer` with
`format: int64`, or `boolean`. A parameter that the query can hold more than
once, for example `label` of `listUnits`, is an array of one of these types.
A list that pages has `pageSize` and `pageToken`, and its answer has
`nextPageToken`.

The description lists every query value that a route reads. A server reads no
other query value of a route in the description. The route that lists the
units of a view answers 400 to a query value that it does not list.

## Request headers and cookies

The description shows these request values:

| Value | Place in the description |
| --- | --- |
| `Authorization` | The security scheme `bearerAuth` |
| `Content-Type` | The media type of the body |
| `Idempotency-Key`, `If-Match` | The shared parameters |
| The cookie `__Host-dustpan_session` | The security scheme `sessionCookie` |
| `X-Dustpan-Csrf` | The security scheme `csrfHeader` |

A request that uses the session cookie must also send `X-Dustpan-Csrf` when its
method is not `GET`, `HEAD` or `OPTIONS`. The server ignores the header on the
other methods. A client that is made from the description can send a write with
a session of the console.

The server also reads these headers, and the description does not show them,
because the browser or the proxy sets them, or because they change no answer:

- `Origin` and `Sec-Fetch-Site`: set by the browser. The server uses them to
  refuse a request with a session that comes from another site.
- `Forwarded` and `X-Forwarded-For`: set by the proxy that the server trusts.
  The server reads one of them for the address of the client.
- `User-Agent` and `Dustpan-Channel`: the server writes them to the audit
  record only.

The server reads no other request header and no other cookie on a route in the
description. This covers the code of the routes and the code that they call.

## Schemas

Each request body and each success answer has a schema in `components.schemas`.
The server makes the schema from the Go type that the route reads or writes, so
the description follows the code:

- A schema name is in UpperCamelCase, for example `CreateCollectionRequest`.
  The route names the schema, so a rename of a Go type does not rename the
  schema.
- A member name is the JSON name of the field, in camelCase.
- A member is required when the server always writes it, or when the server
  needs it in a request. A member that the server can leave out is not required.
- A member that can be null has `type: [X, "null"]`. A reference that can be
  null is `oneOf: [ref, null]`.
- A time is a string with `format: date-time`.
- A map has `additionalProperties` with the schema of its values. A member
  with any JSON value has a schema with a description only.

Two members break the camelCase rule today: `unitID` and `versionID`. A client
reads them as they are. They are renamed in a later version of the API.

## Shared parts

| Part | Content |
| --- | --- |
| `components.securitySchemes.bearerAuth` | An HTTP `Authorization: Bearer` header with an access token. All routes use it except the routes that need no sign-in. |
| `components.securitySchemes.sessionCookie` | The cookie `__Host-dustpan_session` of a session of the console. A route accepts it in place of `bearerAuth`, together with `csrfHeader`. |
| `components.securitySchemes.csrfHeader` | The `X-Dustpan-Csrf` request header: the CSRF value of the session. A request with a session needs it when its method is not `GET`, `HEAD` or `OPTIONS`. Without it, or with a wrong value, the answer is 403 with the `errorCode` `session.csrf`. |
| `components.parameters.IdempotencyKey` | The `Idempotency-Key` request header: a string of 1 to 255 printable ASCII characters. A route that takes it refers to this parameter. |
| `components.parameters.IfMatch` | The `If-Match` request header: the `ETag` of the version that the request replaces. A version that does not match answers 412. |
| `components.parameters.IfMatchRequired` | The same header for a route that answers 428 without it. The parameter has `required: true`. |
| `components.headers.ETag` | The `ETag` answer header. A route that sends it declares it in its success answer. |
| `components.schemas.ErrorResponse` | The body of an error: `code` (the HTTP status), `message`, and the optional `errorCode` and `errorArgs` |
| `components.schemas.ErrorCode` | The `errorCode` values that exist today: every code of the error catalog, sorted. `errorCode` is an open enum of it: a client must accept a code that is not in the list, because the list grows. |
| `components.schemas.ErrorAnswer` | The default answer of each route. It is a `oneOf` with a `discriminator` on `errorCode`. |
| `components.schemas.<Code>Error` | The error answer of one code that has arguments, for example `LockLockedError` for `lock.locked`. `errorCode` has the one value of the code. `errorArgs` refers to the schema `<Code>ErrorArgs`. That schema lists each argument of the code with its type. A name is a string. A count is an integer. A time is a string with the format `date-time`. Each answer of the code has all of its arguments. `access.denied` is the one exception: it has `action` and `objectKind` only when the server knows them. A program must still decide by `errorCode`, not by `message`. |

## Error answers by code

A code that has arguments has its own schema, so that a program reads the
arguments with their types. The `discriminator` of `ErrorAnswer` maps the value
of `errorCode` to this schema. A code that has no arguments, and a code that the
description does not list yet, has the shape of `ErrorResponse`: the last branch
of the `oneOf`. A client must accept such an answer. The schemas come from the
error catalog of the server, so a code and its arguments are in the description
when they are in the catalog.

## Values that can grow

A list of values that can get a new value, such as a kind or a state, is open
in the description only when the description declares an enum for the list. The
list is a schema of its own in `components.schemas`,
for example `ContextStatus`, with `type: string` and `enum`. A member that holds
one of these values is `anyOf: [{$ref: the schema}, {type: string}]`. A member
that can also be null has a third branch, `{type: "null"}`. A client keeps a
value that it does not know. A new value is an additive change.

## Routes that are not in the description

The routes of some areas still sit under the old paths `/api/server`,
`/api/inventory`, `/api/audit` and `/api/runners`. They are not in the
description. Each route joins the description when it moves under
`/api/dustpan/v1`.

The route that reads one unit across namespaces is also not in it yet,
because its path holds a wildcard name that the description does not allow.

## The check in the repository

A test builds the server with each module on and compares its operations with
the checked-in description. The test fails when a route has no entry, when an
entry has no route, and when a path, an `operationId`, a tag, an action, a
public flag, a header or a query parameter differs. Each failure names the
route and the command below.

A handler reads the query, the request headers and the cookies through
functions that give the values of the declared names only. The linter refuses
a direct read in the code of the tool-neutral REST API and in the helpers that
its routes call: the session check, the resolver of the client address and the
CORS wrapper. A handler cannot read a value that the description does not
show. A test reads the source of each adapter and checks that every query name
that it reads is a constant that a label declares.

A read of a name that the description does not hold is an error in the code.
In a test it panics. In the server it gives an empty value, and the server logs
the route, the name and the command below once.

The server does not start when a route under `/api/dustpan/v1` has no valid
`operationId`, when an action has no object kind, or when a route names no body
type and does not say that it has no body.

To write the checked-in file again after a change of the routes, run:

```sh
go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description
```

## The version of the description

Until the first release, `info.version` stays at one value. A change of the
description does not change it. Before 1.0, the tool-neutral API under
`/api/dustpan/v1` can change in a way that breaks a client, and its path and
its version stay the same. The release notes list each break.

The test of `openapi.pin` in `internal/api/apidesc` is disabled until the
first release. The file stays in the tree. At the first release, the release
sets `DocumentVersion`, writes the pin again and turns the test on again:

```sh
go test ./internal/api/apidesc -run TestVersionFollowsTheDescription -update-description-pin
```

After a change of the routes, a contributor does these steps:

1. Write the description again:

   ```sh
   go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description
   ```

2. Write the generated clients and the golden files again:

   ```sh
   make clients-generate
   go test ./internal/clientgen/golang -run TestRenderGolden -update
   ```

`make clients-check` fails when a generated client differs from a new run on
the description.

Commit `openapi.json`, the generated clients and the golden files in the same
pull request. When two pull requests change the description, a merge of the
second can conflict in these generated files. Do not resolve such a conflict
by hand: take either side, then do steps 1 and 2 again on the merged code.
