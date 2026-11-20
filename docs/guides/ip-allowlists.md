# IP allowlists

This guide shows how to turn on IP allowlists for an installation, how to set
the list of one namespace, how to read the effective state, and how to delete
a list that locks people out.

An IP allowlist limits the client addresses that may use an installation or a
namespace. A request from another address gets `403`.

An installation can hold two kinds of list. The installation list is a setting
of the installation, and it applies to every request that uses a credential or
starts a sign-in. A namespace list is a rule of one namespace, and it applies
only when the `--ip-allowlists` feature is on. A request must pass the
installation list first, then the namespace list.

The examples use a token in `$TOKEN` and the host `dustpan.example.com`. The
[security model](../explanation/security-model.md) explains the rules in more
detail.

## Turn on the installation list

The installation list is off by default. Read the settings resource and note
the `revision`, or the `ETag` header, for `If-Match`:

```sh
curl -si -H "Authorization: Bearer $TOKEN" \
  https://dustpan.example.com/api/dustpan/v1/settings
```

The keys are `ipAllowlist.enabled` and `ipAllowlist.installation`. Turn the
list on and set it in one change:

```sh
curl -si -X PATCH -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/merge-patch+json" \
  -H 'If-Match: "3"' \
  -H "Idempotency-Key: $(uuidgen)" \
  -d '{"ipAllowlist.enabled":true,"ipAllowlist.installation":"10.0.0.0/8,2001:db8::/32,192.0.2.7"}' \
  https://dustpan.example.com/api/dustpan/v1/settings
```

The caller needs `server:manage-settings`. The answer is `200` with the new
resource and the new `ETag`. The change takes effect at once.

Only a change that reduces security needs a fresh sign-in, and only when the
installation has console sign-in (the server has a public URL). These changes
reduce security:

- `ipAllowlist.enabled` goes from `true` to `false`.
- `ipAllowlist.installation` is cleared.
- `ipAllowlist.installation` gets an entry that the old list does not cover.
  The first list of an installation that has none is such a change.

These changes need no fresh sign-in: `ipAllowlist.enabled` goes to `true`, and
the list becomes narrower or stays as wide. An access token gets `200` for them.

An access token never gives a fresh sign-in. With an access token, a change that
reduces security gets `403` with the code `mfa.fresh-step-required`, and nothing
changes. The example above sets the first list, so it gets this answer when the
installation has console sign-in. To satisfy the rule, sign in to the console
again, and make the change in the console, or with the session of that new
sign-in. The console sends the session cookie `__Host-dustpan_session` and the
`X-Dustpan-CSRF` header. When the installation has no console sign-in, no change
needs a fresh sign-in, and the access token in the example is enough. The
[settings guide](settings.md#changes-that-need-a-fresh-sign-in) explains the
rule.

The list value is one string of CIDRs or addresses, separated by commas.
The settings resource keeps and shows the string as you sent it, for example
`10.1.2.3/8,127.0.0.1,127.0.0.1`. The server reads the string as a list when it
checks a client address:

- Each item is a CIDR, such as `10.0.0.0/8`, or a single address, such as
  `192.0.2.7`. A single address counts as `/32` for IPv4, or `/128` for IPv6.
- The host bits of a CIDR are dropped. `10.1.2.3/8` counts as `10.0.0.0/8`.
- A duplicate is dropped. The order of the other items is kept.
- The list holds at most 64 distinct entries. A 65th entry gets `400`.
- An item that is not a CIDR or an address gets `400`, with the code
  `ip-allowlist.invalid-cidr`, and the message names the item. The stored list
  does not change.

An empty list denies every address while `ipAllowlist.enabled` is `true`. Set
both keys in one change, so that a client never applies a list that it did not
mean to set.

A client outside the installation list cannot sign in and cannot use an access
token. The answer is `403` with the code `access.ip-not-allowed`, and the
message names the scope that refused: `the address is outside the IP allowlist
of the installation`. The server writes an audit entry with the action
`installation:deny-ip-allowlist`, the scope and the client address. The check runs before the credential, so a refused request
reads no token and writes no sign-in entry.

The installation list applies to the Pulumi CLI and the OpenTofu CLI in the
same way as to the console: each request that carries their access token is
checked. A CLI from a refused address gets the `403` and cannot run a plan or
an apply.

## Turn on the namespace feature

The namespace feature is off by default. Start the server with
`--ip-allowlists`, or set `DUSTPAN_IP_ALLOWLISTS`. The server needs a store to
run the feature. Without a store, the server does not start.

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

Set the list in the console. A stored access token gets `403` with `mfa.fresh-step-required`. The body is:

```json
{"cidrs":["10.0.0.0/8","2001:db8::/32","192.0.2.7"]}
```


The body holds `cidrs`, a list of addresses or networks:

- Each item is a CIDR, such as `10.0.0.0/8`, or a single address, such as
  `192.0.2.7`. A single address is stored as `/32` for IPv4, or `/128` for IPv6.
- The host bits of a CIDR are dropped. `10.1.2.3/8` is stored as `10.0.0.0/8`.
- A duplicate is dropped. The order of the other items is kept.
- The list holds at most 64 distinct entries.
- The body is at most 16 KiB.
- An empty list gets `400`. To delete the list, use `DELETE`.

An item that is not a CIDR or an address, a 65th entry, or an empty list gets
`400` with the code `ip-allowlist.invalid-cidr`, and the message names the
item.

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

A namespace with no list allows every address that the installation list
allows. An unknown namespace answers `404` with the code `namespace.not-found`.

When the feature is off, `GET` still returns the stored list, with `enabled`
set to `false`. The list stays stored and does not apply.

## Delete the list

A stored access token gets `403` with `mfa.fresh-step-required`. Delete the list in the console.

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

## What a list shows

When the feature is on, a list leaves out each namespace whose list does not
contain the client address. A list never shows a namespace, or a stack, a
workspace or a collection in it, that a request from the same address cannot
open.

This applies to every principal that is not exempt, also to a principal whose
grant covers every object: an administrator, or a principal with a grant on
the installation. For that principal:

- `GET /api/server/namespaces` leaves out the namespace, and its `total` does
  not count it.
- `GET /api/dustpan/v1/me/access/objects` answers `"all": true` and names the
  namespace in `excluded.namespaces`. `all` covers every object except the
  objects of the namespaces in that list.
- The list of the deleted stacks and workspaces of the namespace holds none.

The bootstrap administrator token is exempt, as it is for a request, unless the
server runs with `--ip-allowlists-include-bootstrap`. From an address in the
list, the namespace is in each list again.

To find the namespaces that a list leaves out for your address, read your
access:

```sh
curl -s -H "Authorization: Bearer $TOKEN" \
  "https://dustpan.example.com/api/dustpan/v1/me/access/objects?type=namespace"
```

```json
{"type": "namespace", "action": "namespace:read", "all": true,
 "excluded": {"namespaces": ["acme"]}, "objects": [], "total": 0, "nextPageToken": ""}
```

Three health probe paths are outside the installation list: `/health/live`,
`/health/ready` and `/health/ready/<component>`. They answer from the state of
the process, they do not authenticate a credential and they do not read a
token. The path must match exactly, in lower case, in its decoded and its
escaped form. Any other path, including `/api/server/metrics`, is checked.

## Audit

Each change to a list writes one entry. The action is `namespace:set-ip-allowlist`
for a set and `namespace:remove-ip-allowlist` for a delete. A refused request
writes a refusal entry with the action of the request, for example
`collection:read`. A request that the installation list refuses writes an
`installation:deny-ip-allowlist` entry. The entry has the kind `security`, the
outcome `denied`, the scope that refused and the client address. The server
limits these entries: it writes one entry for each scope and client address in
each window. The next entry for that address, or the entry at shutdown, gives
the count of the further refusals. This limit has its own table, so many
client addresses do not change the limit of other denials. The limit applies to the entry only. Each
refused request gets the same 403 answer. Read the entries in the
[audit log](audit-log.md).

If the audit entry of a change cannot be written, the change does not happen.

## Lock-out recovery

The installation list is checked before every credential. Use an address that
you control in the installation list. If the list refuses every client, send
the `PATCH` that sets `ipAllowlist.enabled` to `false` from a client in the
list, or from a client that you add to the list first. Without such a client,
the setting is only in the database. A person with database access must repair
it. The server does not start a bypass for the installation list.

A namespace list that refuses every client, including the operator, can be
deleted in two ways.

1. Restart the server with `--ip-allowlists`, and without
   `--ip-allowlists-include-bootstrap`. The bootstrap administrator token is
   exempt again. Then delete the list in the console. The delete needs a fresh sign-in.
2. If no bootstrap token is available, restart the server without
   `--ip-allowlists`. The lists stay stored and do not apply while the feature
   is off. The delete works while the feature is off, so delete the list in the console
   on that run. Then restart with `--ip-allowlists` again.

The [security model](../explanation/security-model.md) describes the recovery
in more detail.

## Limits

A namespace list holds at most 64 entries. The server also reads the
namespaces that have a list, in one read of at most 1000 namespaces. This
limit is a [code rule](../reference/code-rules.md#ip-allowlists): no grant or
setting changes it, and the API does not show it yet.

The server reads the namespaces that have a list for each list request of a
principal whose grant covers every object, so that the list can leave out the
namespaces that refuse the client address. When more than 1000 namespaces have
a list, the read fails with the error
`ipallow: too many namespaces have an IP allowlist`. The server fails closed:
each list request of such a principal answers `500`, and the server log names
the error. A request on one object still works. To end the refusal, delete the
lists that you do not need until 1000 namespaces or fewer have a list.

## Errors

| Code | Status | Meaning |
| --- | --- | --- |
| `access.ip-not-allowed` | `403` | The client address is not in the installation list or in the list of the namespace. For the installation list, the message names the scope `installation`. |
| `ip-allowlist.invalid-cidr` | `400` | An entry of a list is not a CIDR or an address, or the list holds more than 64 entries. The message names the entry. |
| `mfa.fresh-step-required` | `403` | The change needs a fresh sign-in. |
| `access.denied` | `403` | The caller does not hold the action. |

A namespace list body that is not one JSON object with `cidrs` gets `400` with
no `errorCode`. A `PUT` with the feature off gets `409` with the
message that names the `--ip-allowlists` setting. It has no `errorCode` either.
