# Settings API

A setting is one named value that changes how the server works. The installation
has one settings resource, and each namespace has one. The resource shows each
declared setting with its effective value and the source of that value. It
also changes the values that the database layer holds. The console uses these
routes. A script can use them too.

The paths of the API start with `/api/dustpan/v1`.

## Paths

| Method and path | Action | Object | Answer |
| --- | --- | --- | --- |
| `GET /settings` | `server:read-settings` | The installation | Each setting of the installation |
| `PATCH /settings` | `server:manage-settings` | The installation | Each setting after the change |
| `GET /namespaces/{namespace}/settings` | `namespace:read-settings` | The namespace | Each setting of the namespace |
| `PATCH /namespaces/{namespace}/settings` | `namespace:manage-settings` | The namespace | Each setting after the change |

The `installation-operator` role holds the two actions of the installation.
The `admin` role holds the two actions of a namespace. The `site-administrator`
role holds all four actions, and the `account-administrator` role holds the two
actions of a namespace. No other built-in role holds them. A custom role can
hold them.

## Where a value comes from

The server takes the effective value of a setting from the first layer that
sets it:

1. A flag, an environment variable or the configuration file. These form the
   static layer. A flag wins over a variable, and a variable wins over the
   file.
2. The database layer. This is the value that `PATCH` stores.
3. The default of the setting.

A setting that the static layer sets is locked. The API cannot set it. A
namespace has no static layer.

## The resource

```json
{
  "revision": 3,
  "settings": [
    {
      "key": "branding.preset",
      "scope": "installation",
      "kind": "string",
      "value": "high-contrast",
      "default": "",
      "source": "database",
      "locked": false,
      "lockedBy": "",
      "shadowed": false,
      "databaseValue": "high-contrast",
      "apply": "live",
      "secret": false,
      "set": true,
      "databaseLayer": true,
      "mayReduceSecurity": false,
      "description": "The name of the branding preset of the installation. Empty means the standard palette."
    }
  ]
}
```

The settings are in key order.

| Field | Meaning |
| --- | --- |
| `key` | The name of the setting |
| `scope` | `installation` or `namespace` |
| `kind` | `string`, `bool` or `int`. The kind says if `value` is a string, a boolean or a whole number. |
| `value` | The effective value |
| `default` | The value that the setting has when no layer sets it |
| `source` | The layer of the effective value: `flag`, `env`, `file`, `database` or `default` |
| `locked` | `true` when the API cannot set the key |
| `lockedBy` | Why the key is locked: the flag, the variable or the file key that sets it. For a setting that only the static layer can set, it says so. |
| `shadowed` | `true` when the key is locked and the database layer also holds a value. The static value wins. |
| `databaseValue` | The value in the database layer, or `null` |
| `apply` | `live` when a new value takes effect with no restart. `restart` when it takes effect at the next start. |
| `secret` | `true` when the value is a secret |
| `set` | For a secret, `true` when a value exists. For another setting, `true`. |
| `databaseLayer` | `true` when the database layer can hold a value |
| `mayReduceSecurity` | `true` when a change of the setting can make the installation less secure. See [A change that reduces security](#a-change-that-reduces-security). |
| `description` | A short text |

A secret never shows its value, its default or a database value. These fields
are `null`. The `set` field says if the secret has a value. No answer, log
line or audit entry holds the text of a secret.

The settings that the server reads from the static layer at start, for
example `listen` and `database`, are in the resource too. They have no
database layer. They show where their value comes from. A `PATCH` of one of
them answers 409. See [Configuration](configuration.md) for the flags and the
variables.

## Revision and ETag

The resource of a scope has one revision. The revision changes:

- at each change of a database value,
- at a start or a reload of the configuration file that changes an effective
  value or a source in the static layer.

`GET` and `PATCH` answer with the `ETag` header. It holds the revision in
quotes. A scope that never changed has the revision `"0"`.

## Change settings

`PATCH` takes a JSON merge patch. Send the header
`Content-Type: application/merge-patch+json` and the header `If-Match` with the
`ETag` of your last read.

- A member of the object names a setting. A string, a boolean or a whole
  number sets the database value. `null` deletes the database value, and the
  effective value goes back to the default or to the static value.
- A setting that the body leaves out keeps its value.
- All members change, or none do.
- A body that names a key two times answers 400. So does a body that is not one
  JSON object, and a member that is an object or a list.
- A body over 16384 bytes answers 413. Any other `Content-Type` answers 415.
- A request with no `If-Match` header answers 428. A header that is not one
  revision in quotes answers 400. A weak tag, for example `W/"3"`, never
  matches, so it answers 412.
- When the revision is not the current one, the server answers 412 and changes
  nothing. Read the resource again.

A patch that changes no database value answers 200 with the resource. It does
not change the revision, and it writes no audit entry.

```sh
curl -i "$SERVER/api/dustpan/v1/settings" -H "Authorization: Bearer $TOKEN"
# ETag: "3"

curl -X PATCH "$SERVER/api/dustpan/v1/settings" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/merge-patch+json" \
  -H 'If-Match: "3"' \
  -d '{"branding.preset": "high-contrast"}'
```

### Locked settings

A `PATCH` that sets a key that the static layer sets answers 409 with
`settings.locked`. The answer names the key and the layer in `errorArgs`, and
it changes no key. A `PATCH` of a key that only the static layer can set
answers 409 with `settings.static-only`. This includes every secret.

A `null` for a locked key is allowed. It deletes the database value that the
static value hides, and the key shows `shadowed: false` afterward.

### A change that reduces security

The setting description says if a change of the setting can reduce security,
for example turning a protection off or making a limit longer. A change that
reduces security needs a fresh authentication. When one member of a patch
reduces security, the whole patch needs it.

- A console session needs a sign-in within the freshness window of the session
  policy. An older sign-in answers 401 with the path of a new sign-in.
- A request with an access token can never give a fresh authentication. It
  answers 403 with `mfa.fresh-step-required` and changes nothing. Make the
  change in the console.
- An installation with no console sign-in has no step to ask for. The change
  needs none.

A change that does not reduce security works with an access token.

## Audit

Each change of a database value writes one audit entry. The entry has the kind
`change` and the action of the route (`server:manage-settings` or
`namespace:manage-settings`). The scope is `@server` for the installation and the
namespace for a namespace. One `PATCH` writes one entry, with one change for
each key. A change holds the key, the effective value before and after, the
layer of the new value, and `takesEffect` when the new value needs a restart.
A secret shows only `not set`.
A `null` for a locked key changes no effective value. Its change shows the database value that goes away, `not set` as the new value, and the layer `database`.

A request that the server refuses because of its content writes one entry with
the outcome `failure`. A stale revision changes nothing and writes no entry.

## Errors

An error has the fields `code` (the HTTP status), `message` and `errorCode`.

| Status | `errorCode` | When |
| --- | --- | --- |
| 400 | `settings.patch-invalid` | The body is not one JSON object of settings |
| 400 | `settings.key-unknown` | The scope does not declare a key of the body |
| 400 | `settings.value-invalid` | A value does not fit the kind or the rules of its setting. `errorArgs.key` names the key. |
| 400 | `request.if-match-invalid` | `If-Match` is not one revision in quotes |
| 401 | | No valid token, or the sign-in of the session is too old for this change |
| 403 | `access.denied` | The caller lacks the action |
| 403 | `mfa.fresh-step-required` | The change reduces security and the request has an access token |
| 404 | `namespace.not-found` | The namespace does not exist |
| 409 | `settings.locked` | The static layer sets the key. `errorArgs.key` and `errorArgs.source` name them. |
| 409 | `settings.static-only` | Only the static layer can set the key. `errorArgs.key` names it. |
| 412 | `request.precondition-failed` | The revision changed since your read |
| 413 | `request.body-too-large` | The body is over 16384 bytes |
| 415 | `request.content-type-unsupported` | The `Content-Type` is not the merge patch type |
| 428 | `request.precondition-required` | The request has no `If-Match` header |
| 501 | `settings.unavailable` | The store of the server keeps no settings |

## Settings of the installation

The server declares two settings that the database layer holds. Each other
setting of the installation is static only.

| Key | Kind | Default | Apply | Meaning |
| --- | --- | --- | --- | --- |
| `accessReview.collectionUnitCap` | `int` | 10000 | `live` | The largest count of units that the export of a collection reads. The server refuses an export over it, and refuses a value outside 1 to 100000. A raise counts as a change that reduces security. |
| `branding.preset` | `string` | empty | `live` | The name of the branding preset of the installation. A name of lower-case letters, digits and hyphens, at most 32 characters. Empty means the standard palette. |
| `egress.allowPlainConnections` | `bool` | `false` | `live` | Allow LDAP and SMTP connections with no TLS. An LDAP bind or an SMTP login then sends its password with no encryption. |

The flag `--branding-preset`, the variable `DUSTPAN_BRANDING_PRESET` and the
key `branding.preset` of the configuration file set it from the static layer
and lock it.

The flag `--allow-plain-connections`, the variable
`DUSTPAN_ALLOW_PLAIN_CONNECTIONS` and the key
`egress.allowPlainConnections` of the configuration file set it from the
static layer and lock it.

A namespace has no declared setting yet.

### Bounds of a PostgreSQL store

A server over a PostgreSQL store declares each bound of its code as a
read-only setting. The value is a Go duration, the source is `default`, and a
`PATCH` of one answers 409 with `settings.static-only`. A server over a
SQLite store declares none of these settings.

| Key | Kind | Default | Apply | Meaning |
| --- | --- | --- | --- | --- |
| `database.postgres.statementTimeout` | `string` | `25s` | `restart` | The statement timeout of each connection of the PostgreSQL store. |
| `database.postgres.idleInTransactionTimeout` | `string` | `1m0s` | `restart` | The idle-in-transaction timeout of each connection of the PostgreSQL store. |
| `database.postgres.clientConnectionCheckInterval` | `string` | `5s` | `restart` | The interval at which the PostgreSQL server checks that a client of a connection of the store is still there. |
| `database.postgres.backup.statementTimeout` | `string` | `1h0m0s` | `restart` | The statement timeout of a backup, a restore and the copy of the rows of a backup in the PostgreSQL store. |
| `database.postgres.backup.idleInTransactionTimeout` | `string` | `15m0s` | `restart` | The idle-in-transaction timeout of a backup, a restore and the copy of the rows of a backup in the PostgreSQL store. |
| `database.postgres.schemaChange.statementTimeout` | `string` | `10m0s` | `restart` | The statement timeout of the creation and the removal of a staging schema in the PostgreSQL store. |
| `database.postgres.schemaChange.idleInTransactionTimeout` | `string` | `1m0s` | `restart` | The idle-in-transaction timeout of the creation and the removal of a staging schema in the PostgreSQL store. |
| `database.postgres.migration.timeout` | `string` | `15m0s` | `restart` | The statement timeout and the idle-in-transaction timeout of the migration transaction of the PostgreSQL store. |
