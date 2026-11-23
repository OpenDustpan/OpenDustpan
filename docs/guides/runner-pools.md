# Runner pools

This guide shows how to make a runner pool, how to make a join token for a
runner, how to list the runners of a pool, and how to drain and retire a
runner.

A runner pool is a set of runners in one namespace. A runner joins a pool with
a join token. The server serves these routes only when the installation has a
public URL. The [runner API reference](../reference/runner-api.md) has the
fields and the limits.

The examples use a token in `$TOKEN`, the host `dustpan.example.com` and the
namespace `acme`. The token needs the actions `namespace:read-runners` and
`namespace:manage-runners` on the namespace.

## Make a pool

```sh
curl -sS -X POST https://dustpan.example.com/api/dustpan/v1/namespaces/acme/runner-pools \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"name":"jobs","purpose":"jobs"}'
```

The answer is `201` with the pool. Use the pool `id` or its name in the next
commands. The purpose of a pool does not change after you make it.

A request with a field that is not valid gets `400` with the code
`runner.invalid`. The message names the field and the rule, for example
`runner request is not valid: lease: use 0, or 30000 to 600000 milliseconds.`
The message never repeats the value that you sent. These are the valid values:

| Field | Valid values |
| --- | --- |
| `name` | 1 to 63 lowercase letters, digits and `-`. It starts and ends with a letter or a digit. |
| `purpose` | `jobs`. The field is required. A namespace pool cannot have the `sweeps` purpose. |
| `lease` | 0, or 30000 to 600000 milliseconds. Zero takes 90000. |
| `concurrency` | 0, or 1 to 64. Zero takes 1. |
| `isolation` | A list of `kubernetes`, `firecracker`, `gvisor` or `process`. |
| `gitCredentials.mode` | `none`, `forge` or `custom`. |

List the pools of the namespace:

```sh
curl -sS https://dustpan.example.com/api/dustpan/v1/namespaces/acme/runner-pools \
  -H "Authorization: Bearer $TOKEN"
```

## Make a join token

```sh
curl -sS -X POST https://dustpan.example.com/api/dustpan/v1/namespaces/acme/runner-pools/jobs/join-tokens \
  -H "Authorization: Bearer $TOKEN"
```

The answer is `201`. The field `token` holds the secret, and the field `command`
holds the command for the runner. The server shows the secret one time. A token
is good for one join and lives 1 hour by default.

To list the tokens of a pool, use `GET` on the same path. The list has no
secret. To revoke a token that no runner used, send `POST` to
`/api/dustpan/v1/namespaces/acme/runner-pools/jobs/join-tokens/{id}/revoke`.

## List the runners of a pool

```sh
curl -sS https://dustpan.example.com/api/dustpan/v1/namespaces/acme/runner-pools/jobs/runners \
  -H "Authorization: Bearer $TOKEN"
```

The list holds the runners that are not retired. Each entry has the fields
`status`, `draining` and `keyIdentifier`.

## Drain a runner

A draining runner claims no new work. It keeps the work that it has.

```sh
curl -sS -X POST https://dustpan.example.com/api/dustpan/v1/namespaces/acme/runners/{runner}/drain \
  -H "Authorization: Bearer $TOKEN"
```

## Retire a runner

A retired runner authenticates nothing. The retire ends the claims of the
runner in the same transaction.

```sh
curl -sS -X POST https://dustpan.example.com/api/dustpan/v1/namespaces/acme/runners/{runner}/retire \
  -H "Authorization: Bearer $TOKEN"
```

## Errors

A caller who cannot read the namespace gets `404`. A caller who can read the
namespace but lacks the action of the route gets `403`. A change writes one
audit entry. The [audit log guide](audit-log.md) shows how to read it.
