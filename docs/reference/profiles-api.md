# Profiles API

A profile is a named set of config values and secrets in one namespace. This
page describes the routes that create, read, list, change and delete
profiles and their entries. No tool reads a profile through these routes
yet. A client that manages profiles, for example a script or the console,
uses this API.

The paths of the API start with `/api/dustpan/v1`. Each path in this page is
relative to `/api/dustpan/v1/namespaces/{namespace}`.

## Paths

| Method and path | Action | Answer |
| --- | --- | --- |
| `GET /profiles` | `profile:read` | One page of the profiles of the namespace |
| `POST /profiles` | `profile:write` | The new profile, with revision 1 |
| `GET /profiles/{profile}` | `profile:read` | The profile |
| `DELETE /profiles/{profile}` | `profile:manage` | No body |
| `GET /profiles/{profile}/revisions` | `profile:read` | One page of the revisions, newest first |
| `POST /profiles/{profile}/revisions` | `profile:write` | The profile, with the new revision |
| `GET /profiles/{profile}/revisions/{revision}` | `profile:read` | The document and the entries of one revision |
| `GET /profiles/{profile}/entries` | `profile:read` | The entries of the newest revision |
| `GET /profiles/{profile}/entries/{entry}` | `profile:read` | One entry |
| `PUT /profiles/{profile}/entries/{entry}` | `profile:write` | The profile, with the new revision |
| `DELETE /profiles/{profile}/entries/{entry}` | `profile:write` | The profile, with the new revision |

The built-in roles give `profile:read` to each role that holds
`namespace:read`. The role `admin` holds all the profile actions. A grant
of a profile action works on a namespace. A grant on one profile does not
exist yet.

## Names and IDs

`{profile}` is the name of the profile or its ID. An ID starts with `prf-`
and never changes. A name has one or two segments of `a-z`, `0-9`, `.`, `_`
and `-`, joined with `/`, and at most 100 characters. The first segment of
two is the folder of the profile. A name never starts with `prf-`.

Write a name of two segments as one escaped path segment:
`payments%2Faws-prod`. The path `payments/aws-prod` answers 404. The ID
always works. A reverse proxy that decodes `%2F` before the server breaks
the names of two segments, so use the ID behind such a proxy.

## The document

A revision holds one YAML document. The top-level keys are
`schemaVersion` (required, 1), `description`, `imports`, `providers`,
`rotators`, `values`, `exports` and `checks`. Any other key answers 400.

`values` is a tree of maps, lists and scalars. A map whose keys start with
`$` is an entry:

| Key | Meaning |
| --- | --- |
| `$value` | A plain value |
| `$secret` | A secret in plain text. The server seals it and stores `$sealed` in its place. |
| `$sealed` | A sealed secret. Only the server writes it. |
| `$final` | `true` or `false` |
| `$neverStore` | `true` or `false`. It needs `$secret` or `$sealed`. |
| `$type` | A map with the `kind` of the value |
| `$description` | Text for people |

An entry holds exactly one of `$value`, `$secret` and `$sealed`. The
server writes the document again from its tree when the document has a
`$secret`. Comments and the order of keys stay, and the spacing can change.

A document has at most 256 KiB, 4,096 leaves in `values`, a depth of 32
levels, 64 provider blocks, 16 rotator blocks and 32 imports. A namespace
has at most 1,000 profiles. An answer with status 422 names the limit.

## Secrets

The server seals each secret with a data key of the profile before it
writes anything. The plain text is in no table, no file, no log and no
audit entry.

- A read answers a secret as `"secret": true` with no `value`. The document
  of a revision holds `$sealed` values. A `$sealed` value works only in the
  profile that made it. A save with a `$sealed` value of another profile
  answers 400.
- A read with `showSecrets=true` also answers the plain text of each secret.
  It needs `profile:open`. The answer has `Cache-Control: no-store`. The
  server writes an audit entry of the kind `reveal` before it answers, and
  the entry names the number of values and never a value. If the server
  cannot write the entry, no value leaves.

## Routing changes

A change of a provider block, a rotator block, `imports` or `exports`
changes where secrets go. So does a change of a value that a block reads,
directly or through other expressions. Such a save needs `profile:open` as
well as `profile:write`. Without it, the answer is 403 with the error code
`profile.routing-change-needs-open` and the list of the blocks and paths.

The server decides this from the text of the two documents. It reports too
much rather than too little: an index that the server cannot compute counts
as a read of the whole tree. A create with a provider block is a routing
change.

## Concurrency

Each answer that returns one profile has an `ETag` header and an `etag`
field. The tag names the newest revision: `"prf-k3v9q2m7x4t8p1w6z5r0.3"`. A write on
`/revisions` or on `/entries/{entry}` needs `If-Match` with the tag that the
caller read. A missing header answers 428. A tag that is not the newest
revision answers 412. `DELETE /profiles/{profile}` takes `If-Match` too, but
does not need it.

## Lists

A list takes `pageSize` (1 to 500, 100 by default) and `pageToken`. The
answer holds `nextPageToken` when more items follow. A token works only for
the query that made it. `GET /profiles` also takes `folder`. The list of
revisions takes the same two values.

## The key for a safe retry

No route takes the header `Idempotency-Key` yet. A write with the header
answers 400 with the error code `request.idempotency-key-unsupported` and
changes nothing, so the server never ignores a key without an error.

## Errors

An error has the fields `code` (the HTTP status), `message` and, for most
errors, `errorCode`.

| Status | When |
| --- | --- |
| 400 | A bad name, document, body or query value |
| 401 | No valid token |
| 403 | The caller lacks the action, or a routing change lacks `profile:open` |
| 404 | An unknown namespace, profile, revision or entry |
| 409 | A name in use |
| 412 | `If-Match` does not match the newest revision |
| 422 | A document above a limit, or a `schemaVersion` above 1 |
| 428 | A write with no `If-Match` |

## Backups and deletes

The profiles and their revisions are part of each backup. A delete frees the
name at once and hides the profile. The revisions stay in the database. No
route restores or purges a deleted profile yet.
