# Reading state contents

This guide shows how to read the contents of the state of a unit with the
tool-neutral API, with each secret value replaced by a marker. The read
answers where the secret values are. It never answers a secret value.

A unit is a Pulumi stack or an OpenTofu workspace. The read works the same
for both.

## What you get

The read gives one page of markers for one version of the state. A marker
shows that the value at one path is a secret. The value is not in the answer.

- Pulumi: a marker names each value that the stack stores as a secret.
- OpenTofu: a marker names each sensitive output, each sensitive attribute
  path and each sensitive value path of the state. A value that OpenTofu marks
  as sensitive as a whole is one marker, with no marker for a key below it.

## Before you start

You need:

- An access token of a principal that holds the action `unit:read` on the
  unit. A grant of `unit:read` on the installation, the namespace, the
  collection or the unit gives this. A grant on a different unit gives 403.
- The reference of the unit: the tool, a colon, the namespace, a slash and
  the name of the unit. For a Pulumi stack the name is
  `<project>/<stack>`.

Examples of references: `opentofu:acme/network` and
`pulumi:acme/web/prod`.

## Read the current version

`GET /api/dustpan/v1/units/<reference>/state` reads the current version of the
state. Replace each `/` of the reference with `%2F`, so that the reference
fills one path segment.

```sh
curl -H "Authorization: token $DUSTPAN_ADMIN_TOKEN" \
  "https://dustpan.example/api/dustpan/v1/units/opentofu:acme%2Fnetwork/state"
```

The answer has these fields:

- `unit`: the reference of the unit.
- `version`: the version that the server read, as text.
- `markers`: the markers of this page. Each marker has `secret` (always
  `true`) and `path`.
- `valuePaths`: the same paths as `markers`, in the same order.
- `nextCursor`: the cursor of the next page. The field is empty on the last
  page.

Each path is a JSON Pointer (RFC 6901) into the state document of that
version. The paths are in sorted order, so that the pages are stable.

## Read one version

`version` selects the version. For OpenTofu, send the ID of a state version.
For Pulumi, send the number of an update.

```sh
curl -H "Authorization: token $DUSTPAN_ADMIN_TOKEN" \
  "https://dustpan.example/api/dustpan/v1/units/opentofu:acme%2Fnetwork/state?version=sv-abc123"
```

A malformed `version` answers 400 with the code `state.bad-path`. A version
that does not exist answers 404 with the code `state-version.not-found`.

## Read the next page

A page holds at most 200 markers. When the state has more markers,
`nextCursor` is not empty. Send it as `cursor` to read the next page.

```sh
curl -H "Authorization: token $DUSTPAN_ADMIN_TOKEN" \
  "https://dustpan.example/api/dustpan/v1/units/opentofu:acme%2Fnetwork/state?cursor=<nextCursor>"
```

A cursor is opaque. Do not change it. A cursor is valid:

- for the same unit, the same version and the same caller that got it,
- for 10 minutes,
- if it has 512 bytes or fewer.

A cursor that breaks one of these rules answers 400 with the code
`state.bad-path`. The server reads no state for a cursor that is too long.
To start again, send the request with no cursor.

A cursor is valid only on the instance of the server that made it. Each
instance makes its own signing key when it starts. If more than one instance
runs behind a load balancer, a cursor that one instance made answers 400 on
another instance. A restart of an instance also ends the cursors that it made.
Read the first page again, with no cursor, to get a cursor that the instance
accepts.

## Errors

| Status | Code | Meaning |
| --- | --- | --- |
| 400 | `state.bad-path` | The version or the cursor is not valid. |
| 403 | `unit.forbidden` | The caller does not hold `unit:read` on the unit. |
| 404 | `unit.not-found` | The unit does not exist. |
| 404 | `unit.no-state` | The unit has no state yet. |
| 404 | `state-version.not-found` | The version does not exist. |
| 413 | `state-version.too-large` | The version is larger than 5 MB. The server refuses it before it loads the version. |
| 413 | `stack.state-too-complex` (Pulumi), `state-version.too-complex` (OpenTofu) | The state nests deeper than 100 levels, or the work of the read is more than 128 MiB. For OpenTofu, a sensitive mark of the state that the server cannot find with one rule gives this code too. The server refuses the state before it builds the path of any marker. |
| 500 | `server.internal` | The server cannot read the shape of the version, for example an OpenTofu state that the client encrypted. The answer has no content. |
| 504 | `state.read-timeout` | The read took more than 30 seconds. |

The server counts each refusal with its denial limiter and writes one audit
entry for each window. The answer of a refusal keeps its own status.

## Limits

- The server refuses a version larger than 5 MB before it loads the version.
- For OpenTofu, the server loads the version in chunks of 256 KB and stops
  at 5 MB. For Pulumi, the server checks the size of the stored version
  before it loads the version, and checks the size again after the load. The
  server decodes the state document whole, so the limit of 5 MB bounds the
  memory of the decode.
- The server limits the work of one read by the shape of the state, not only
  by its size. It refuses a state that nests deeper than 100 levels, or whose
  values need more than 128 MiB of work, with 413 and the code
  `stack.state-too-complex` or `state-version.too-complex`. The work of a value
  is the length of its path plus a fixed cost for its marker. One budget covers
  the whole read. The server decides this before it builds the path of any
  marker. A state with many markers under these limits is read in pages.
- The server refuses an OpenTofu state that it cannot read, with 500 and no
  content. A reader can then tell this answer from a state with no secrets.
  Examples are a state that the client encrypted, a `resources` value that is
  not an array, and a resource with no type or name.
- A read that takes more than 30 seconds ends with 504 and no bytes. The
  server checks the time after it parses the state, inside the masking and
  after it walks the state, so a read never answers 200 after the deadline.
- The read uses the stable ID of the unit that the grant names, not its name.
  A unit that is deleted and made again with the same name, while the read
  runs, has a new ID. The read refuses it and answers 404.
