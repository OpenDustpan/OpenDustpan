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
| `info.version` | The version of the description, for example `0.1.0`. It is not the version of the server. It changes when the description changes. It is valid as a SemVer version and as a PEP 440 version. |
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
| `parameters` | One path parameter for each wildcard of the path, and a reference to a shared header parameter when the route takes the header |
| `requestBody` | A reference to the schema of the body, for a route that reads a JSON body. A `PATCH` takes `application/merge-patch+json`. A route that reads no body has no `requestBody`. |
| `responses` | `2XX` for success, with a reference to the schema of the answer when the answer has a body, and `default` for an error |
| `security` | `[]` for a route that needs no sign-in. A route that needs sign-in has no `security` member and uses the default. |

The actions are those that the route declares. A handler can check more, for
example an action on an object that it finds from the request.

A route that takes a file as raw bytes, for example the upload of a provider
file to the registry, has no `requestBody`, because the description holds JSON
bodies only.

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
| `components.securitySchemes.bearerAuth` | The one scheme: an HTTP `Authorization: Bearer` header with an access token. All routes use it except the routes that need no sign-in. |
| `components.parameters.IdempotencyKey` | The `Idempotency-Key` request header: a string of 1 to 255 printable ASCII characters. A route that takes it refers to this parameter. |
| `components.parameters.IfMatch` | The `If-Match` request header: the `ETag` of the version that the request replaces. A version that does not match answers 412. |
| `components.parameters.IfMatchRequired` | The same header for a route that answers 428 without it. The parameter has `required: true`. |
| `components.headers.ETag` | The `ETag` answer header. A route that sends it declares it in its `2XX` answer. |
| `components.schemas.ErrorResponse` | The body of an error: `code` (the HTTP status), `message`, and the optional `errorCode` and `errorArgs` |

## Values that can grow

A list of values that can get a new value, such as a kind or a state, is open
in the description. The list is a schema of its own in `components.schemas`,
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
public flag or a header differs. The server does not start when a route under
`/api/dustpan/v1` has no valid `operationId`, when an action has no object
kind, or when a route names no body type and does not say that it has no body.

To write the checked-in file again after a change of the routes, run:

```sh
go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description
```
