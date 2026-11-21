# Profiles API

A profile is a named set of config values and secrets in one namespace. This
page describes the routes that create, read, list, change and delete
profiles, and the routes that open a profile and read the open session. No
tool reads a profile through these routes
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
| `GET /profiles/{profile}/revisions/{revision}` | `profile:read` | The document and the entries of one revision. `revision` is a number or `latest`. |
| `POST /profiles/{profile}/open` | `profile:open` | The session ID and its end. Never a value |
| `GET /profile-sessions/{session}` | `profile:open` | The values of the session, with the secrets |

A write or a delete does not need `profile:read`. A caller with only
`profile:write` can save a revision, and a caller with only `profile:manage`
can delete a profile.

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

An entry holds exactly one of `$value`, `$secret` and `$sealed`. A key that
starts with `$` is allowed only under `values`. In `providers`, `rotators`,
`exports`, `checks` and `imports`, such a key answers 400: put the secret in
`values` and read it with `${ values.<path> }`. The
server writes the document again from its tree when the document has a
`$secret`. Comments and the order of keys stay, and the spacing can change.

A document has at most 256 KiB, 4,096 leaves in `values`, a depth of 32
levels, 64 provider blocks, 16 rotator blocks and 32 imports. A namespace
has at most 1,000 profiles. An answer with status 422 names the limit.

## Exports

`exports` says what each consumer of the profile gets. It holds the keys
`variables`, `files`, `pulumi`, `opentofu` and `proxy`. Any other key
answers 400.

| Key | Consumer |
| --- | --- |
| `variables`, `files`, `pulumi`, `opentofu` | The launcher of a run command or a step |
| `proxy` | The job proxy only |

The launcher never gets `proxy`, and the job proxy gets only `proxy`. So a
credential in `proxy` never reaches the job.

`proxy` is a map from a credential name to a credential. A credential name
has 1 to 63 characters of `a-z`, `0-9`, `_` and `-`, and starts with a
letter or a digit. `proxy` holds at most 64 credentials. Each credential has
a `type` and exactly the fields of its type:

| `type` | Fields |
| --- | --- |
| `header` | `name`, `value` |
| `basic` | `user`, `password` |
| `query` | `name`, `value` |
| `client-certificate` | `certificate`, `key` |
| `ca` | `certificates` |

```yaml
values:
  grafana:
    token: { $secret: glsa_example }
exports:
  proxy:
    grafana-admin:
      type: header
      name: Authorization
      value: ${ values.grafana.token }
```

- `name` is literal text. A header name has only the token characters of
  HTTP, and cannot be a header that frames or routes a request, for example
  `Host`, `Content-Length` or `Proxy-Authorization`. A query parameter name
  has only `A-Z`, `a-z`, `0-9`, `.`, `_`, `~` and `-`.
- Each other field is exactly one reference into `values`, for example
  `${ values.grafana.token }`. Literal text answers 400.
- `value`, `password` and `key` must read a secret source: a `$secret`
  value, the output of a provider or a rotator
  (`${ providers.<name>.<output> }`) that the document declares, a value of
  a profile that `imports` lists (`${ imports["<profile>"].values.<path> }`
  or `${ profiles["<profile>"].values.<path> }`), or a path that the
  document does not hold. A provider or rotator block must be in the
  document itself. A path that the document does not hold passes only when
  the document has imports, because only an import can give it. A field can read such a
  source through other values, but each value on the way must be exactly
  one reference, for example `${ values.token }`. A plain value, a literal,
  text around a reference, an operator, a function and a read of `$env`
  answer 400. So to send `Bearer <token>`, store the whole text as the
  `$secret`.
- A field cannot read a map or a list.
- The text of a `header` field or of a `basic` field cannot hold a control
  character, for example CR or LF. A `basic` user cannot hold `:`.
- A path that the document does not hold answers 400 when the document has
  no imports.
- The save cannot see a value that another profile gives, so it does not
  check that such a value is a secret. A check when the profile opens does
  not exist yet.

A change of a value that an export reads, directly or through other
expressions, is a routing change (see "Routing changes").

## Secrets

The server seals each secret with a data key of the profile before it
writes anything. The plain text is in no table, no file, no log and no
audit entry.

- A read answers a secret as `"secret": true` with no `value`. The document
  of a revision holds `$sealed` values. A `$sealed` value works only in the
  profile that made it. A save with a `$sealed` value of another profile
  answers 400.
- A read of a revision with `showSecrets=true` also answers the plain text of each secret.
  It needs `profile:open` and a console session with a recent sign-in. An
  access token answers 403 with the error code `mfa.fresh-step-required`.
  A session with an old sign-in answers 401 with the same error code and the
  field `reauthenticate`. Each refusal writes an audit entry of the kind
  `reveal` with the outcome `failure`. The answer has `Cache-Control: no-store`. The
  server writes an audit entry of the kind `reveal` before it answers, and
  the entry names the number of values and never a value. If the server
  cannot write the entry, no value leaves. A read with `showSecrets=true`
  that lacks `profile:open` answers 403, and the server writes an audit
  entry of the kind `reveal` with the outcome `denied`.

## Open a profile

An open merges the profile with its imports, computes the `${ }` values and
seals the result with a data key of the profile. The answer holds the session
ID and its end, never a value. The session ID starts with `pos-`. `SERVER` is
the public URL of the server, and `TOKEN` is a personal token of a person:

```sh
SERVER=https://dustpan.example
TOKEN=<personal-token>

curl -s -X POST -H "Authorization: token $TOKEN" -H "Content-Type: application/json" \
  -d '{"consumer":"api"}' \
  "$SERVER/api/dustpan/v1/namespaces/acme/profiles/web/open"
```

The body has three fields:

| Field | Meaning |
| --- | --- |
| `paths` | The joined paths to read, for example `["region", "db.password"]`. Empty means all paths. At most 1,024 paths. |
| `consumer` | `api`, `console` or `pulumi`. Required. |
| `duration` | The life of the session, for example `"90m"`. Empty means 2 hours. From 1 minute to 12 hours. |

A later import wins over an earlier one: a map merges key by key, a list or
a scalar replaces, and a `null` deletes. A value that is `$final` in an
import cannot change: the open answers 422. An expression reads `values`
only: any other name answers 422. A profile with a provider block answers
422, because no provider runs yet.

An open needs `profile:open` only, with no fresh sign-in. A personal token
opens with no recent sign-in. A job token answers 403. This differs from the
reveal, which needs a console session with a recent sign-in. An open writes
one audit entry of the kind `access` with the consumer, the paths and the
profiles that it read, and never a value. A denial writes one entry with the
outcome `denied`.

An open reads at most 64 revisions, follows imports at most 16 levels deep,
and answers at most 4 MiB. The server sets no limit on the number of live
sessions of an owner.

## Read an open session

Only the principal that opened the session reads it. The answer maps each
path to its value and its secret mark. A read writes no audit entry. `SESSION`
is the session ID of the open:

```sh
curl -s -H "Authorization: token $TOKEN" \
  "$SERVER/api/dustpan/v1/namespaces/acme/profile-sessions/$SESSION"
```

`?path=region` reads one path. An unknown path answers 404. The answer has
`Cache-Control: no-store`, because it can hold secrets.

A read checks `profile:open` again. After the grant ends, the read answers
403 and deletes the session. A read by another principal answers 404: it never
shows that the session exists. A session that ended answers 404. A forged ID
answers 404.

## Routing changes

A change of a provider block, a rotator block, `imports` or `exports`
changes where secrets go. So does a change of a value that a block or an
export reads, directly or through other expressions. The exports are
`variables`, `files`, `pulumi`, `opentofu` and `proxy`. Such a save needs
`profile:open` as
well as `profile:write`. Without it, the answer is 403 with the error code
`profile.routing-change-needs-open` and the list of the blocks and paths.

The server decides this from the text of the two documents. It reports too
much rather than too little: an index that the server cannot compute counts
as a read of the whole tree. A create with a provider block is a routing
change.

## Concurrency

Each answer that returns one profile has an `ETag` header and an `etag`
field. The tag names the newest revision: `"prf-k3v9q2m7x4t8p1w6z5r0.3"`. A write on
`/revisions` needs `If-Match` with the tag that the caller read. A missing
header answers 428. A tag that is not the newest revision answers 412. A
weak tag (`W/"..."`) answers 412, because the server compares tags in the
strong way. `DELETE /profiles/{profile}` takes `If-Match` too, but
does not need it.

## Lists

A list takes `pageSize` (1 to 500, 100 by default) and `pageToken`. The
answer holds `nextPageToken` when more items follow. A token works only for
the query that made it. `GET /profiles` also takes `folder`. The list of
revisions takes the same two values.

## The key for a safe retry

Each write takes the header `Idempotency-Key`. The write routes are the
create (`POST /profiles`), the save (`POST /profiles/{profile}/revisions`),
the delete (`DELETE /profiles/{profile}`) and the open
(`POST /profiles/{profile}/open`). The header is optional. A client
that retries a call that timed out sends the same key again, and the server
does the write one time.

- **The key.** A string of 1 to 255 printable ASCII characters. Use a random
  value, for example a UUID. A key that is not valid answers 400 with the
  error code `request.idempotency-key-invalid` and changes nothing.
- **The scope.** The server keeps the first answer for the caller, the method,
  the path and the key. The same key from another caller, or on another path,
  is a new key. A caller never sees the answer of another caller.
- **A replay.** A request with a known key and the same body, query and
  `If-Match` header gets the first answer again, with its status and its headers, and the server
  does not write again. The answer has the header `Idempotent-Replayed: true`.
  The server checks the permissions of the caller again before it sends the
  answer. A caller that lost the permission gets 403.
- **Another request.** The server compares the body, the query and `If-Match`.
  The same key with another body, query or `If-Match` answers 422 with
  the error code `request.idempotency-key-reused` and changes nothing.
- **A request that runs.** A request with a key whose first request has not
  ended answers 409 with the error code `request.idempotency-key-in-progress`.
  Try again later. A first request that has no answer after 5 minutes (for
  example after a crash) expires. The next request with the key then does the
  write.
- **Which answers the server keeps.** The server keeps the answer of a write
  that succeeded. It does not keep an error answer, so a retry with the same
  key after the client fixes the cause does the write. The server never keeps
  400, 401, 403, 404, 409, 412, 422, 423, 429 or 5xx.
- **The open keeps the session ID only.** The answer of an open can hold
  secrets, so the server never keeps it. A replay of the key answers 409 with
  the error code `request.idempotency-key-replayed-secret` and the session ID
  in `objectID`. Read the session, or send a new key to open again.
- **For how long.** 24 hours from the first request. A restore of a backup
  deletes all keys. A change of the key-encryption key, or the loss of it,
  deletes all keys too: the server cannot open the key of its hashes, so it
  makes a new one.
- **What the server keeps.** The first answer, and a keyed hash of the request
  body. The server never keeps the body. The answer of a write holds no secret
  value.

A server that has no store of keys answers 400 with the error code
`request.idempotency-key-unsupported` to a write with the header. The server
never ignores a key without an error.

## Errors

An error has the fields `code` (the HTTP status), `message` and, for most
errors, `errorCode`.

| Status | When |
| --- | --- |
| 400 | A bad name, document, body or query value |
| 401 | No valid token |
| 403 | The caller lacks the action (`access.denied`). The IP allowlist refuses the address (`access.ip-not-allowed`). A session check fails (`session.csrf`, `session.other-site`). A routing change lacks `profile:open` (`profile.routing-change-needs-open`). A console session has an old sign-in (`mfa.fresh-step-required`) |
| 404 | An unknown namespace, profile, revision or session |
| 409 | A name in use, a request with a key whose first request has not ended, or a replay of an open key (`request.idempotency-key-replayed-secret`) |
| 412 | `If-Match` does not match the newest revision |
| 422 | A document above a limit, a `schemaVersion` above 1, or a key that a request with another body used |
| 422 | An open that a provider block stops (`profile.provider`), a never-store path (`profile.never-store`), or a changed final value (`profile.final`) |
| 422 | An open that reads outside `values` (`profile.reference`), an import cycle (`profile.import-cycle`), or a result above a limit (`profile.limit`) |
| 428 | A write with no `If-Match` |

## Backups and deletes

The profiles and their revisions are part of each backup. A delete frees the
name at once and hides the profile. The revisions stay in the database. No
route restores or purges a deleted profile yet.

Open sessions are part of no backup. A restore empties the table. The server
deletes each ended session on a schedule.
