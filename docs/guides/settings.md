# Settings

This guide shows how to read and change the settings of an installation and of
a namespace through the API. It also shows the refusals that you can get and the
one request that helps with each of them.

Each scope has one settings resource. The installation has one, and each
namespace has one. The resource lists each setting that the scope declares, with
its effective value, its source and its lock state. A change is a JSON merge
patch of database values.

The examples use a token in `$TOKEN` and the host `dustpan.example.com`.

## The two paths

| Scope | Path | Read action | Change action |
| --- | --- | --- | --- |
| Installation | `/api/dustpan/v1/settings` | `server:read-settings` | `server:manage-settings` |
| Namespace | `/api/dustpan/v1/namespaces/{namespace}/settings` | `namespace:read-settings` | `namespace:manage-settings` |

## Read the settings

```sh
curl -si -H "Authorization: Bearer $TOKEN" \
  https://dustpan.example.com/api/dustpan/v1/settings
```

The answer is `200`. The `ETag` header and the field `revision` hold the
revision of the resource. Each item has these fields:

| Field | Meaning |
| --- | --- |
| `key` | The name of the setting. |
| `scope` | The scope that declares the setting. |
| `kind` | The kind of the value: `string`, `int` or `bool`. |
| `value` | The effective value. It is `null` for a secret. |
| `default` | The value that the setting has when no other layer sets it. It is `null` for a secret. |
| `source` | The layer that gives the value: `default`, `database`, `file`, `env` or `flag`. |
| `locked` | `true` when the static layer sets the key. A locked key shows `lockedBy`. |
| `shadowed` | `true` when a database value exists, but a static value hides it. |
| `databaseValue` | The database value, or `null`. |
| `apply` | When a change takes effect: `live` at once, or `restart` after the server starts again. |
| `secret` | `true` when the value is a secret. The API never shows it. |
| `set` | `true` when a value exists. For a secret, it shows that the static layer gave a value. |
| `databaseLayer` | `true` when a database value can set the key. `false` means that only a flag, a variable or the file sets it. |
| `mayReduceSecurity` | `true` when a change can make the scope less secure. |
| `description` | A short text that says what the setting does and what values it takes. |

For a namespace, send the same request to the namespace path. A namespace that
does not exist gets `404`.

## Change a setting

Send a merge patch with the revision that you read in `If-Match`. A key with a
value sets the database value. A key with `null` deletes it.

```sh
curl -si -X PATCH -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/merge-patch+json" \
  -H 'If-Match: "3"' \
  -H "Idempotency-Key: change-$(date +%s)" \
  -d '{"branding.preset":"high-contrast"}' \
  https://dustpan.example.com/api/dustpan/v1/settings
```

The answer is `200` with the new resource and the new `ETag`. The rules:

- All keys of the patch change, or none do.
- A patch that changes nothing keeps the revision and writes no audit entry.
- Each change writes one audit entry in the scope. A refused change writes one
  failure entry. No entry holds a value of a secret.
- A patch needs an `Idempotency-Key`. A request with no key gets `400`, and
  nothing changes. A second request with the same key, the same body and the
  same `If-Match` gets the stored answer, with the header
  `Idempotent-Replayed: true`. The change does not run again. The same key with
  another body or another `If-Match` gets `422`, and nothing changes.

## Changes that need a fresh sign-in

A change that makes the scope less secure needs a fresh sign-in. The field
`mayReduceSecurity` of a setting shows which settings can do this. An access
token never gives a fresh sign-in, also the bootstrap admin token. Make the
change in the console, or with a console session that signed in a short time
ago. When the installation has no console sign-in, the change needs no fresh
sign-in.

For the key `signIn.localPassword.enabled`, the server counts a change to
`"true"`, or to empty, as a change that reduces security. A change to
`"false"` is not. So the bootstrap admin token can set the key to `"false"`.
The same token then gets 403 with `mfa.fresh-step-required` when it sets the
key to `"true"` or to `null`. Only a console session with a recent sign-in
can turn the password sign-in on again. If the password sign-in was the only
sign-in, no person can start such a session, and no API route turns the
setting on again.

## Refusals

Each refusal changes nothing. Read the resource again with
`GET /api/dustpan/v1/settings`, then send the patch again.

| Status | Code | Cause |
| --- | --- | --- |
| `403` | `mfa.fresh-step-required` | The change reduces security and the request has no fresh sign-in. |
| `409` | `settings.locked` | The static layer sets the key. The answer names the key and the source. Send `null` to delete a database value that the lock hides. |
| `409` | `settings.static-only` | Only a flag, a variable or the file sets this key. |
| `400` | `settings.key-unknown` | The scope does not declare the key. |
| `400` | `settings.value-invalid` | The value has the wrong kind or is outside the range of the key. |
| `400` | `request.idempotency-key-required` | The request has no `Idempotency-Key` header. Send a new key. |
| `412` | `settings.revision-stale` | The revision of `If-Match` is not the current revision. |
| `428` | `request.precondition-required` | The request has no `If-Match` header. |
| `415` | `request.content-type-unsupported` | The content type is not `application/merge-patch+json`. |
| `422` | `request.idempotency-key-reused` | The key was used with another request. Send a new key. |

## The restore access setting

The key `restore.replace-access-tables` is a boolean with the default `false`.
When it is `true`, each holder of `server:restore` replaces the access tables in
a restore. A change needs `server:manage-settings`, `server:restore-access` and
a fresh console sign-in. See [Restore access setting](restore-access-setting.md).

## The temporary administrator session limit

The key `tempadmin.session-limit` sets how long a new temporary administrator
record and its token last. The installation settings list it. The default is
`1h0m0s`, the value is a duration from 1 millisecond to 24 hours, and a change
applies at once (`apply` is `live`). The key has `mayReduceSecurity` set, so a
change needs a fresh sign-in.

A record that exists keeps its end time. A new record ends after the limit, or
at the maximum of the token policy when that is shorter.

A stored value that the server cannot read as a duration in this range stops
the creation of a new record. To delete such a value, stop the server and run
`opendustpan host create-temp-admin --reset-session-limit --reason <text> --database <database>`
on the host. The command deletes the stored value and writes an audit entry.
