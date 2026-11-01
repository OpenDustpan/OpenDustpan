# IP allowlists

This guide shows how to turn on IP allowlists for an installation, how to set
the list of one namespace, how to read the effective state, and how to delete
a list that locks people out.

An IP allowlist limits the client addresses that may use a namespace. A request
from another address gets `403`. An allowlist is set per namespace, and it applies
only when the installation has the feature on.

The examples use a token in `$TOKEN` and the host `dustpan.example.com`. The
[security model](../explanation/security-model.md) explains the rules in more
detail.

## Turn on the feature

The feature is off by default. Start the server with `--ip-allowlists`, or set
`DUSTPAN_IP_ALLOWLISTS`. The server needs a store to run the feature. Without a
store, the server does not start.

| Setting | Effect |
| --- | --- |
| `--ip-allowlists` | Turns the feature on. The default is off. |
| `--ip-allowlists-include-bootstrap` | Applies the lists to the bootstrap administrator token too. The default is off, so the bootstrap token is exempt. |

Behind a reverse proxy, also set the trusted proxies. Otherwise every client
appears to come from the proxy's address. Use `--trusted-proxies` and
`--trusted-proxy-header` for this.

## Set the list of a namespace

`PUT /api/server/namespaces/{namespace}/ip-allowlist` sets the list. The caller
needs `namespace:manage-ip-allowlist` on the namespace, with a fresh sign-in.
The answer is `200` with the list as stored.

```sh
curl -s -X PUT -H "Authorization: Bearer $CONSOLE_SESSION" \
  -H "Content-Type: application/json" \
  -d '{"cidrs":["10.0.0.0/8","2001:db8::/32","192.0.2.7"]}' \
  https://dustpan.example.com/api/server/namespaces/acme/ip-allowlist
```

The body holds `cidrs`, a list of addresses or networks:

- Each item is a CIDR, such as `10.0.0.0/8`, or a single address, such as
  `192.0.2.7`. A single address is stored as `/32` for IPv4, or `/128` for IPv6.
- The host bits of a CIDR are dropped. `10.1.2.3/8` is stored as `10.0.0.0/8`.
- A duplicate is dropped. The order of the other items is kept.
- The list holds at most 64 distinct entries.
- The body is at most 16 KiB.
- An empty list gets `400`. To delete the list, use `DELETE`.

An item that is not a CIDR or an address gets `400`, and the message names the
item. These `400` answers carry no `errorCode`.

## Read the list

`GET /api/server/namespaces/{namespace}/ip-allowlist` returns the state of the
list. The caller needs `namespace:manage-ip-allowlist`.

```json
{"enabled": true, "optedIn": true, "cidrs": ["10.0.0.0/8", "2001:db8::/32", "192.0.2.7/32"]}
```

| Field | Meaning |
| --- | --- |
| `enabled` | The feature is on for the installation. |
| `optedIn` | The namespace holds a list. |
| `cidrs` | The stored list. It is `[]` when there is none. |

The API has no single field for the effective state. The list applies when all
three of these are true:

1. `enabled` is `true`.
2. `optedIn` is `true`.
3. The client address is in one of the `cidrs`.

A namespace with no list allows every address. An unknown namespace reads as
having no list.

When the feature is off, `GET` still returns the stored list, with `enabled`
set to `false`. The list stays stored and does not apply.

## Delete the list

`DELETE /api/server/namespaces/{namespace}/ip-allowlist` deletes the list. It
answers `204`. The caller needs `namespace:manage-ip-allowlist`, with a fresh
sign-in. The delete works when the feature is off. A delete of a namespace that
has no list answers `204` and writes no audit entry.

## What the list applies to

When the feature is on, the list is checked before a route runs. A request that
the list refuses gets `403` with the code `access.ip-not-allowed` and the
message `forbidden`. The answer does not name the list or the namespace.

The check applies to every route that checks access, including the runner
protocol and the jobs and drift routes. It also applies to the address-only
routes of the OpenTofu state writes and the Pulumi update leases. These routes
have no principal, so the bootstrap exemption does not reach them.

A read that is refused by the list gets `403`, not the `404` that a denied
object normally gets.

## Audit

Each change to a list writes one entry. The action is `namespace:set-ip-allowlist`
for a set and `namespace:remove-ip-allowlist` for a delete. A refused request
writes a refusal entry with the action of the request, for example
`collection:read`. Read the entries in the [audit log](audit-log.md).

If the audit entry of a change cannot be written, the change does not happen.

## Lock-out recovery

A list that refuses every client, including the operator, can be deleted in two
ways.

1. Restart the server with `--ip-allowlists`, and without
   `--ip-allowlists-include-bootstrap`. The bootstrap administrator token is
   exempt again. Then send the `DELETE`.
2. If no bootstrap token is available, restart the server without
   `--ip-allowlists`. The lists stay stored and do not apply while the feature
   is off. The `DELETE` route works while the feature is off, so delete the list
   on that run. Then restart with `--ip-allowlists` again.

The [security model](../explanation/security-model.md) describes the recovery
in more detail.

## Errors

| Code | Status | Meaning |
| --- | --- | --- |
| `access.ip-not-allowed` | `403` | The client address is not in the list of the namespace. |
| `mfa.fresh-step-required` | `403` | The change needs a fresh sign-in. |
| `access.denied` | `403` | The caller does not hold the action. |

A list refusal for a bad body, such as an empty list or an invalid item, is a
`400` with no `errorCode`. A `PUT` with the feature off gets `409` with the
message that names the `--ip-allowlists` setting. It has no `errorCode` either.
