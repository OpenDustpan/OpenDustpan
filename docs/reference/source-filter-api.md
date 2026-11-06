# Source filter API

The source filter of a namespace decides which submodules and which Git LFS
objects a source snapshot holds. A namespace that never set the filter has the
empty filter: a snapshot holds no submodule and no Git LFS object.

The server serves the routes when it has a store of the source filter. The
SQLite and PostgreSQL stores have one. The actions are the actions of the
[runner API](runner-api.md).

## Routes

Both routes use the path `/api/dustpan/v1/namespaces/{namespace}/source-filter`.
The caller signs in as for the other APIs, with an access token or a console
session. A caller who cannot read the namespace receives 404. A caller who can read
it, but lacks the action of the route, receives 403.

| Method | Action | Answer |
| --- | --- | --- |
| `GET` | `namespace:read-runners` | The filter of the namespace |
| `PUT` | `namespace:manage-runners` | The filter as the server stored it |

`PUT` replaces the whole filter, so its body holds each field. A change writes
one audit entry with the action `namespace:manage-runners`. The entry has one
change with the key `sourceFilter`. Its old and new values are the text of the
filter before and after the change.

The server stores the filter. No fetch reads it yet. A change of the filter
changes no snapshot that exists.

## The filter

| Field | Content |
| --- | --- |
| `submodules` | A list of URL patterns. The server fetches a submodule whose URL matches a pattern. |
| `largeFiles` | A list of URL patterns. The server fetches the Git LFS objects of a repository whose URL matches a pattern. |
| `failOnDenied` | `true` fails the fetch when the commit has a submodule or a Git LFS pointer that the filter does not allow. `false` keeps the gitlink or the pointer file as it is. |
| `protectedBranches` | A list of exact branch names. The server stores the list. No gate reads it yet. |

Each list has at most 64 entries. A list is never `null`.

A URL pattern is an `https` URL of a repository, for example
`https://git.example.com/platform/modules`. The last part of the path can be
`*`, which matches one path part. A pattern has no user, no port, no query, no
fragment and no `%`.

A branch name has 1 to 255 characters of ASCII letters, digits, `.`, `_`, `-`
and `/`. It does not start with `-`. A part between two `/` is not empty, does
not start with `.` and does not end with `.lock`. `..` does not occur. A branch
name is not a pattern, so `release/*` fails. A name is not in the list twice.

A change of `submodules`, `largeFiles` or `failOnDenied` makes a new snapshot
key, so the next fetch of a commit makes a new snapshot. A change of
`protectedBranches` alone does not, because it does not change the content of a
snapshot.

## Example

```text
$ curl -s -H "Authorization: Bearer $TOKEN" \
    https://dustpan.example.com/api/dustpan/v1/namespaces/acme/source-filter
{"submodules":[],"largeFiles":[],"failOnDenied":false,"protectedBranches":[]}

$ curl -s -X PUT -H "Authorization: Bearer $TOKEN" \
    -d '{"submodules":["https://git.example.com/platform/*"],"largeFiles":[],"failOnDenied":true,"protectedBranches":["main"]}' \
    https://dustpan.example.com/api/dustpan/v1/namespaces/acme/source-filter
{"submodules":["https://git.example.com/platform/*"],"largeFiles":[],"failOnDenied":true,"protectedBranches":["main"]}
```

## Errors

Errors have the body `{"message": "...", "code": 400, "errorCode": "..."}`.

| Status | `errorCode` | Cause |
| --- | --- | --- |
| 400 | `request.body-invalid` | The body is not one JSON object. It has an unknown field, a missing field, a `null` field, or data after the object. |
| 400 | `source.filter-invalid` | A list has more than 64 entries, a pattern fails its rules, or a branch name fails its rules or is in the list twice. The message names the value. |
| 401 | | The request has no valid credential. |
| 403 | | The caller can read the namespace but lacks the action of the route. |
| 404 | `namespace.not-found` | The namespace does not exist, or the caller cannot read it. The answer is the same for both. |
| 500 | `source.filter-unreadable` | The filter that the server stored fails the checks of a filter. Send a `PUT` with a valid filter to replace it. |

A refused change stores nothing and writes no audit entry of a change.

## Backup

The filter is part of the backup of the namespace. A restore refuses a backup
that holds a filter that fails the checks above.
