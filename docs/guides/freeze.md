# Change freezes

A change freeze stops every new job start, cancel and phase dispatch on a
namespace. Use it during an incident or a change window. Jobs that are already
queued or running keep running. Reads, lists and logs keep working.

A namespace has at most one active freeze. Each freeze has a reason, the
subject of the person who created it, and the time it started. A freeze that
ends keeps its record, and the list of freezes shows it.

## Who can do what

| Action | Where the grant is | Built-in role that holds it |
| --- | --- | --- |
| `namespace:manage-freezes`: create and end a freeze | The namespace | `admin` |
| `namespace:break-freeze`: start a job during a freeze | The installation | `freeze-breaker` |
| `namespace:read`: list the freezes | The namespace | `viewer` and the roles above it |

The action `namespace:break-freeze` works only through a grant on the
installation. A grant on a namespace does not give it. A person with
`namespace:manage-freezes` and without `namespace:break-freeze` cannot start a
job during a freeze.

## Create a freeze

`POST /api/dustpan/v1/namespaces/<namespace>/freezes` with the reason. The reason
has 1 to 500 characters.

```sh
curl -X POST -H "Authorization: token $DUSTPAN_TOKEN" \
  -d '{"reason": "release freeze"}' \
  "https://dustpan.example/api/dustpan/v1/namespaces/acme/freezes"
```

The server answers `201` with the freeze. The answer has `id`, `reason`,
`active` and the creator. If the namespace already has an active freeze, the
server answers `409` and keeps the first freeze.

## List freezes

`GET /api/dustpan/v1/namespaces/<namespace>/freezes` returns the active freeze
and each ended freeze, in time order. The list has a page token and a limit.

```sh
curl -H "Authorization: token $DUSTPAN_TOKEN" \
  "https://dustpan.example/api/dustpan/v1/namespaces/acme/freezes"
```

## Start a job during a freeze

A start during a freeze answers `409` with the code `job.frozen`. The start
creates no job. The answer names the freeze. Read the reason with the list
command above.

```sh
curl -X POST -H "Authorization: token $DUSTPAN_TOKEN" \
  -d '{"unit":{"tool":"opentofu","name":"web"},"operation":"plan","pool":"jobs","location":{"repository":"acme/infra","ref":"main","directory":"net"},"reason":"nightly plan"}' \
  "https://dustpan.example/api/dustpan/v1/namespaces/acme/jobs"
```

A person with `namespace:break-freeze` can start the job during the freeze. The
start then needs a `breakFreezeReason` in the body. The server records the
reason and the starter in the audit log.

```sh
curl -X POST -H "Authorization: token $BREAK_TOKEN" \
  -d '{"unit":{"tool":"opentofu","name":"web"},"operation":"plan","pool":"jobs","location":{"repository":"acme/infra","ref":"main","directory":"net"},"reason":"incident fix","breakFreezeReason":"sev1 rollback"}' \
  "https://dustpan.example/api/dustpan/v1/namespaces/acme/jobs"
```

A start with an empty `breakFreezeReason` answers `400`. A start without the
break-freeze action answers `409` with `job.frozen`, even when it has a reason.

To give a person the break-freeze action, grant the `freeze-breaker` role on the
installation. See [roles and grants](roles-and-grants.md).

```sh
curl -X POST -H "Authorization: token $DUSTPAN_TOKEN" \
  -d '{"subject":"principal:prn-release-lead","role":"freeze-breaker"}' \
  "https://dustpan.example/api/server/grants"
```

## End a freeze

`POST /api/dustpan/v1/namespaces/<namespace>/freezes/current/end` ends the
active freeze. The server records the end time and the subject that ended it.
The next job start is accepted.

```sh
curl -X POST -H "Authorization: token $DUSTPAN_TOKEN" \
  "https://dustpan.example/api/dustpan/v1/namespaces/acme/freezes/current/end"
```

If the namespace has no active freeze, the server answers `404` and changes
nothing. Run the list command to check the state first.

## Errors and the command that helps

| Answer | What it means | The command that helps |
| --- | --- | --- |
| `409` with `job.frozen` on a start or a cancel | The namespace has an active freeze, and the person lacks `namespace:break-freeze`. | The list command above reads the reason. Ask a namespace administrator for the `freeze-breaker` role, or wait for the freeze to end. |
| `403` on a create or an end | The person lacks `namespace:manage-freezes` on the namespace. | Ask a namespace administrator for the `admin` role on the namespace. |
| `409` on a create | The namespace already has an active freeze. | The list command above shows the active freeze. |
| `404` on an end | The namespace has no active freeze. | The list command above shows the state. |
| `400` on a create or a start | The reason or the `breakFreezeReason` is empty or longer than 500 characters. | Send a reason of 1 to 500 characters. |
