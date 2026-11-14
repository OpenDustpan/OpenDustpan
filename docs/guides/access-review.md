# Access review

This guide shows how to read the access review of an installation, a namespace,
a unit or a principal, how to read a past state of the grants, and how to save
the review. It also shows what each read writes to the audit log.

The access review lists the grants of an object, the principal that holds each
grant, and the time that each grant was made, revoked or changed. An operator
uses it to answer who can do what, and to find the access that is no longer
needed.

The examples use a token in `$TOKEN` and the host `dustpan.example.com`.

## The routes

Each route needs one read action on the object in the path. The caller needs the
action for that object.

| Route | Action | Object |
| --- | --- | --- |
| `GET /api/server/access-review` | `server:read-access` | The installation |
| `GET /api/server/namespaces/{namespace}/access-review` | `namespace:read-access` | The namespace |
| `GET /api/server/namespaces/{namespace}/collections/{collection}/access-review` | `collection:read-access` | The collection |
| `GET /api/server/namespaces/{namespace}/unit-access-review?tool=<tool>&unit=<name>` | `unit:read-access` | The unit |
| `GET /api/server/principals/{principal}/access-review` | `server:read-access` on the principal | The principal |

The principal route works for a service principal only. For any other kind
of principal, the answer is `400`. The unit route needs both `tool` and `unit`.

The collection route, `GET /api/server/namespaces/{namespace}/collections/{collection}/access-review`,
lists the grants on the collection, the grants on each unit that is in the
collection, and the grants on the namespace and on the installation. A grant on
the namespace or the installation appears only when its role gives a data action
or manages access, and its row has `inheritedFrom`. A deleted unit
that is not purged stays in the list, with `objectState` set to `deleted`.
The export reads at most `accessReview.collectionUnitCap` units, 10000 by
default, and the setting takes 1 to 100000. A collection over that count gets
`422`. A change to a larger cap needs a fresh sign-in.

The route takes the same query values as the other routes: `limit`, `cursor`,
`from` and `to`, `subject`, and `expand`. An unknown collection gets `404`.

The route answers `501` in three cases:

- The server runs without its access store, so it keeps no roles and grants.
- The store cannot read an access review. Both built-in stores, PostgreSQL and
  SQLite, can read one.
- The server cannot list the units of the collection. A review without those
  units would be partial, so the server answers `501` and gives no list.

## Query values

| Query | Meaning |
| --- | --- |
| `limit` | Rows per page, from 1 to 1000. The default is 100. A value above 1000 gets `400`. |
| `cursor` | The `nextCursor` of the page before. Use it with the same query as the page before. |
| `from`, `to` | A history window, in Unix milliseconds. Give both or neither. `from` must be before `to`. |
| `subject` | Only the rows of one holder. Use `principal:<id>` or `group:<id>#member`. |
| `expand` | `members`, to show one row for each member of a group. Any other value gets `400`. |

A cursor only works with the same object, mode, window, subject and expand
values that made it. A cursor from another export gets `400`.

## Read a snapshot

With no `from` and `to`, the answer is a snapshot of the grants at one time.
The time is set on the first page, and the later pages keep it.

```sh
curl -s -H "Authorization: Bearer $TOKEN" \
  "https://dustpan.example.com/api/server/namespaces/acme/access-review?limit=100"
```

The answer holds these top-level fields:

| Field | Meaning |
| --- | --- |
| `total` | The number of rows, on the first page only. It is `null` on the later pages. |
| `asOfMs` | The time of the snapshot, in Unix milliseconds. It is `null` for a history. |
| `grants` | The rows of the page. |
| `nextCursor` | The cursor of the next page. It is empty on the last page. |

A row holds these fields:

| Field | Meaning |
| --- | --- |
| `grantID` | The ID of the grant. It is empty on a bootstrap row. |
| `subject` | The holder, `principal:<id>` or `group:<id>#member`. |
| `subjectName` | The name of the holder. |
| `holder` | `bootstrap admin token` on a bootstrap row only. |
| `role` | The role of the grant. |
| `object` | The object of the grant. |
| `objectState` | `live`, `deleted` or `purged`. |
| `inheritedFrom` | The object that holds the grant of this row, when the row comes from a container of the reviewed object. `@server` is the installation. |
| `grantedBy`, `grantedAtMs` | Who made the grant, and when. |
| `revokedBy`, `revokedAtMs` | Who ended the grant, and when. `null` for a current grant. |
| `effectiveFromMs`, `effectiveUntilMs` | The period in which the grant applies. |
| `memberCount` | The number of members of a group. Shown on a snapshot. |
| `lastAccessTracked`, `lastAccessAtMs` | For a principal row, the last time the principal used a token or a session. Omitted when there is no use. |
| `via` | For a member row, the group, the member and the period of the membership. |
| `canGrantAccess` | Whether the caller can give this access to another principal. |

A bootstrap row is the bootstrap administrator token. Every review lists it when
the request has no `subject` filter. On the installation review it has no
`inheritedFrom`. On any other review, `inheritedFrom` is `@server`, the
installation.

## Read a history

Give `from` and `to` to read the grants that were in effect in a window. The
history includes the grants that ended in the window.

```sh
curl -s -H "Authorization: Bearer $TOKEN" \
  "https://dustpan.example.com/api/server/namespaces/acme/access-review?from=1767225600000&to=1767830400000"
```

A history has `asOfMs` set to `null`, and it has no `memberCount`.

## Filter by holder

Give `subject` to see one holder. For a principal, the answer also includes the
grants of the groups that the principal was in, during the time that it was a
member.

```sh
curl -s -H "Authorization: Bearer $TOKEN" \
  "https://dustpan.example.com/api/server/namespaces/acme/access-review?subject=principal:prn-..."
```

## Show the members of a group

Give `expand=members` to get one row for each member of a group. A group with
no member in the window keeps one row with no membership. Each member row
has the `via` field.

```sh
curl -s -H "Authorization: Bearer $TOKEN" \
  "https://dustpan.example.com/api/server/namespaces/acme/access-review?expand=members"
```

## Where a membership comes from

Each `via` object has `memberSource`, `memberSourceId` and `memberSuspended`.
The `memberSource` field has one of these values:

- `local`: The membership was made through the API. Check who made it in the audit log.
  The `memberSourceId` field is empty.
- `scim`: A SCIM call kept the membership. The source ID names the identity in the directory.
  Check the directory group.
- `claim`: The membership came from a group claim at sign-in. The source ID names the identity.
  Check the identity provider's group claim.

In a snapshot, `memberSuspended: true` means the membership was suspended at the snapshot time and grants nothing.
A membership suspended after the snapshot time shows `false`.
In a history, only active memberships are listed, so `memberSuspended` is always `false`.

## Save the review

The review has no file format. Each read answers JSON. The server has no CSV
download and no NDJSON export for the access review. To save a review, read each
page and keep the `grants` array. Follow `nextCursor` until it is empty.

```sh
cursor=""
while :; do
  page=$(curl -s -H "Authorization: Bearer $TOKEN" \
    "https://dustpan.example.com/api/server/namespaces/acme/access-review?limit=1000${cursor:+&cursor=$cursor}")
  printf '%s\n' "$page" >> review-acme.jsonl
  cursor=$(printf '%s' "$page" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("nextCursor",""))')
  [ -z "$cursor" ] && break
done
```

Keep the saved file with care. It lists the grants of the holders, the names
of the holders and the times of the changes.

## The audit log

Each page that a read answers writes one entry in the audit log, and a failed
read writes none. The kind of the entry is `read-access`. The action is the read
action of the route, for example `namespace:read-access`. The subject is the
caller.

The object of the entry names the read:

| Read | Object of the entry |
| --- | --- |
| A snapshot | `access-review <object> snapshot`, with `asOf=<ms>` when the time is known |
| A history | `access-review <object> history <fromMs>-<toMs>` |
| A holder filter | Adds ` subject=<subject>` |
| An expanded read | Adds ` expand=members` |

The namespace of the entry is `@server` for the installation review. It is the
name of the namespace for a namespace review.

The reads of your own access are different. `GET /api/server/caller/access` and
`GET /api/server/caller/grants` write no entry. Any signed-in principal can
read them.

## Who can read a review

| Scope | Who can read |
| --- | --- |
| Installation | A principal that holds `server:read-access` on the installation. |
| Namespace | A principal that holds `namespace:read-access` on the namespace. |
| Collection | A principal that holds `collection:read-access` on the collection. |
| Unit | A principal that holds `unit:read-access` on the unit. |
| Principal | A principal that holds `server:read-access` on the service principal. |

A caller without the action gets `403`.

## Read your own access

Any signed-in principal can read its own access. Use the caller routes:

```sh
curl -s -H "Authorization: Bearer $TOKEN" \
  https://dustpan.example.com/api/server/caller/grants
```

`GET /api/server/caller/grants` pages the caller's own grants, with the same
`limit` and `cursor` values. `GET /api/server/caller/access` answers the question
of which access the caller has on an object. See
[Roles and grants](roles-and-grants.md) for that query.
