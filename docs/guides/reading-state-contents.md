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
  path and each sensitive value path of the state.

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

## Errors

| Status | Code | Meaning |
| --- | --- | --- |
| 400 | `state.bad-path` | The version or the cursor is not valid. |
| 403 | `unit.forbidden` | The caller does not hold `unit:read` on the unit. |
| 404 | `unit.not-found` | The unit does not exist. |
| 404 | `unit.no-state` | The unit has no state yet. |
| 404 | `state-version.not-found` | The version does not exist. |
| 413 | `state-version.too-large` | The version is larger than 5 MB. The server does not load it. |
| 504 | `state.read-timeout` | The read took more than 30 seconds. |

The server counts each refusal with its denial limiter and writes one audit
entry for each window. The answer of a refusal keeps its own status.

## Limits

- The server refuses a version larger than 5 MB before it loads the version.
- The server loads the version in chunks of 256 KB. It then decodes the
  whole state document in memory. The chunked load limits the read of the
  blob store. It does not limit the memory that the decode uses.
- A read that takes more than 30 seconds ends with 504 and no bytes.
