# Self-service blueprint API

The blueprint routes manage the drafts and the published versions of the
blueprints of a namespace. A published version is the document that a
requester uses. The routes live under `/api/dustpan/v1/self-service/`.

## Grants apply at namespace level

Grants apply at namespace level today: a blueprint action is checked on the
namespace that holds the blueprint. A grant on one blueprint is not part of
this API. Grants on one blueprint come in a later change.

Each route checks the namespace first. The request of a caller who cannot
read the namespace gets `404`. The checks of each route are:

| Route | Method | Actions on the namespace | Refusal |
| --- | --- | --- | --- |
| `/api/dustpan/v1/self-service/blueprints/{namespace}` | `GET` | `namespace:read`, `blueprint:read` | `404` |
| `/api/dustpan/v1/self-service/blueprints/{namespace}/{blueprint}/draft` | `GET` | `namespace:read`, `blueprint:read` | `404` |
| `/api/dustpan/v1/self-service/blueprints/{namespace}/{blueprint}/draft` | `PUT` | `namespace:read`, `blueprint:publish` | `403` |
| `/api/dustpan/v1/self-service/blueprints/{namespace}/{blueprint}/versions` | `POST` | `namespace:read`, `blueprint:publish`, and `principal:manage-tokens` on the source principal of the blueprint | `403` |
| `/api/dustpan/v1/self-service/blueprints/{namespace}/{blueprint}/versions/{version}` | `GET` | `namespace:read`, `blueprint:read` | `404` |
| `/api/dustpan/v1/self-service/blueprints/{namespace}/{blueprint}/versions/{version}/retire` | `POST` | `namespace:read`, `blueprint:retire` | `403` |

The source principal of a blueprint is checked on that principal itself, not on
the namespace: a grant on the namespace alone does not allow a publish that
names the principal.

A request that the route refuses is written to the audit log. For these
refusals, the audit entry names the namespace of the path as its object,
because that is the scope of the grant that the check used. A denial that the
blueprint service makes after the route has passed is written with the
blueprint reference as its object.

## Draft and versions

- `GET .../draft` returns the draft with its version, as `{"version": n,
  "blueprint": {...}}`. The `ETag` header holds the version in quotes.
- `PUT .../draft` stores the blueprint document in the body. The `If-Match`
  header must hold the version that the caller read, in quotes. A missing or
  malformed `If-Match` header answers `428`. A version that changed since it
  was read answers `409`. A successful write answers `200` with the new
  version and the new `ETag`.
- `POST .../versions` publishes the draft version named in the body, as
  `{"draftVersion": n}`. The server compiles every expression with the Expr
  engine, and then stores the next version. The answer is `201` with the
  version description. A draft version that is already published answers
  `409`.
- `GET .../versions/{version}` returns one version description.
- `POST .../versions/{version}/retire` retires one version and answers `204`.
- `GET /api/dustpan/v1/self-service/blueprints/{namespace}` returns the newest
  version of each blueprint of the namespace, as `{"versions": [...]}`.

A version description holds `namespace`, `blueprint`, `number`,
`draftVersion`, `publishedAt`, `publishedBy`, `exprVersion` and `retired`.

## Blueprint document

The document is strict JSON. The server refuses an unknown field, a duplicate
key and a document over 256 KiB. The description of the request body lists
each field and its type. The server check stays the authority for the values
it accepts.

### Open enums

Every enum of the API is open, so that a client keeps a value that it does
not know yet. The server accepts only the values in this list. Each list
follows the order of the Go constants.

| Field | Values |
| --- | --- |
| `inputs[].type` | `string`, `integer`, `number`, `boolean`, `string-list` |
| `policy[].effect` | `allow`, `approve`, `deny` |
| `quotas[].kind` | `units`, `requests`, `expr` |
| `quotas[].scope` | `namespace`, `requester` |
| `delivery.default`, `delivery.homes[]` | `git`, `opendustpan` |
| `delivery.git.deliveries[]` | `review-request`, `direct-push` |

### Fixed filter values

A filter value (`equals.value`) is a JSON scalar: a string, a number or a
boolean. The server refuses an object, an array and `null`. The description
states the scalar form in prose, because the description format cannot state
a choice between kinds.

## Errors

Each refused request answers with a catalog code. The codes of this API are
in the `self-service.` area:

| Code | Meaning |
| --- | --- |
| `self-service.blueprint-not-found` | The namespace has no blueprint with the name, or no version of it. |
| `self-service.blueprint-invalid` | The blueprint is not valid, or the path names no valid blueprint or version. |
| `self-service.blueprint-too-large` | The blueprint document is larger than the limit. |
| `self-service.blueprint-stale` | The draft changed since it was read, or the `If-Match` header is missing. |
| `self-service.blueprint-already-published` | This draft version is already published: change the draft first. |
| `self-service.blueprint-limit` | The namespace holds the largest number of blueprints or versions. |
