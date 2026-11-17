# Set the proxy settings of a runner pool

This guide shows how to save the proxy settings of a runner pool, how to read
the effective values, and what each error means.

The proxy settings of a pool say which private networks the job proxy of the
pool may open, and which upstream proxy it uses. The proxy rules of a
namespace and of a unit say which connection a job can make. The settings of
the pool limit where the proxy can connect. See
[Set the proxy rules of a job](job-proxy-rules.md) for the rules.

The paths in this guide are under `/api/dustpan/v1/namespaces/{namespace}`.
Use the token of a person. A pool that has no saved settings shows the default
values, with `source` set to `default` and `version` 0. The default has no
allowed ranges and no upstream proxy.

## The routes

| Route | Action | Use |
| --- | --- | --- |
| `GET runner-pools/{pool}/proxy-settings` | `namespace:read-runners` | Read the effective settings. |
| `PUT runner-pools/{pool}/proxy-settings` | `namespace:manage-runners` | Replace the settings. |

`{pool}` is the ID of the pool or its name. A save writes one audit entry.

## Fields

| Field | Content |
| --- | --- |
| `allowedRanges` | The private network ranges in CIDR form that the proxy may open, at most 64. An empty list allows none. |
| `upstreamProxy` | The URL of the upstream proxy, with the scheme `http` or `https`. An empty text means no upstream proxy. The URL holds no user name and no password. |
| `upstreamCredential` | The user name and the password for the upstream proxy. Only a save has this field. |

The user name is 1 to 256 bytes and has no colon. The password is at most 1024
bytes. A credential needs an upstream proxy.

An answer has these fields:

| Field | Content |
| --- | --- |
| `allowedRanges` | The effective ranges. |
| `upstreamProxy` | The effective URL, or an empty text. |
| `credentialSet` | `true` when the pool has a credential. The answer never shows the credential. |
| `source` | `default` or `set`. |
| `version` | The version of the settings. It also is the `ETag`, in quotes. |
| `updatedBy`, `updatedAt` | Who saved the settings, and when. A pool with the default has neither. |

## Save the settings

Send the whole settings. A save replaces the stored settings.

```
curl -X PUT -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -H 'If-Match: "<version>"' \
  -d '{"allowedRanges":["10.0.0.0/8"],"upstreamProxy":"http://proxy.example.com:3128","upstreamCredential":{"username":"runner","password":"<password>"}}' \
  "$SERVER/api/dustpan/v1/namespaces/acme/runner-pools/builders/proxy-settings"
```

The credential has three states in a save:

- A body with no `upstreamCredential` field keeps the stored credential.
- A body with `"upstreamCredential": null` removes it.
- A body with a user name and a password replaces it.

The server seals the credential. No read shows it again. To see whether a pool
has one, read `credentialSet`.

Send the `version` of your last read in `If-Match`, in quotes, to save only
when nobody changed the settings. A save with no `If-Match` replaces the
settings with no check.

## Errors

| Status | Cause | What to do |
| --- | --- | --- |
| 400 | A range is not valid, the URL is not valid, or the credential is not valid. The message names the field. | Correct the field and save again. |
| 400 | `If-Match` is not a version in quotes. | Send the `version` of a read, in quotes. |
| 404 | The pool does not exist in the namespace. | List the pools with `GET runner-pools`. |
| 412 | The settings changed after your read. | Read the settings again and save again. |
| 413 | The body is larger than 16 KB. | Send fewer ranges. |
| 415 | The content type is not `application/json`. | Send the header `Content-Type: application/json`. |

A caller who cannot read the namespace gets 404. A caller who can read it, but
lacks the action of the route, gets 403.
