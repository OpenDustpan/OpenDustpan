# Change freezes

This guide shows how to create a change freeze, list the freezes, start a job
through a freeze with a break-glass reason, and end a freeze.

A change freeze stops the jobs that change a unit on a namespace. While a freeze
is active, the server refuses a start and a dispatch of a phase that applies,
refreshes or destroys a unit. A plan still runs. A cancel of a job still
succeeds. A phase that already runs when the freeze starts runs to its end. A
freeze does not stop state writes from a tool. Reads, lists and logs keep
working.

A namespace has at most one active freeze. Each freeze has a reason, the subject
of the person who created it, and the time it started. An ended freeze keeps its
record, and the list of freezes shows it.

The examples use a token in `$TOKEN`, the host `dustpan.example`, and the
namespace `acme`. The token needs the action `namespace:manage-freezes` on the
namespace to create or end a freeze. The `admin` role holds it. The token needs
`namespace:read` to list the freezes.

## Create a freeze

`POST /api/dustpan/v1/namespaces/<namespace>/freezes` with the reason. The reason
has 1 to 500 characters.

```sh
curl -X POST -H "Authorization: token $TOKEN" \
  -d '{"reason": "release freeze"}' \
  "https://dustpan.example/api/dustpan/v1/namespaces/acme/freezes"
```

The server answers `201` with the freeze. The answer has `id`, `reason`,
`active` and `createdBy`. If the namespace already has an active freeze, the
server answers `409` with `job.frozen` and keeps the first freeze.

## List freezes

`GET /api/dustpan/v1/namespaces/<namespace>/freezes` returns every freeze of the
namespace, in the order that they started. The active freeze has
`"active":true`.

```sh
curl -H "Authorization: token $TOKEN" \
  "https://dustpan.example/api/dustpan/v1/namespaces/acme/freezes"
```

## Start a job during a freeze

A start of a phase that changes a unit answers `409` with the code `job.frozen`
during a freeze. The start creates no job. The jobs route starts plan jobs
only, and a plan is not a phase that changes a unit. So this route accepts a
plan start during a freeze. A start of another operation answers `422` with
`job.unsupported`.

```sh
curl -X POST -H "Authorization: token $TOKEN" \
  -d '{"unit":{"tool":"opentofu","name":"web"},"operation":"plan","pool":"jobs","location":{"repository":"acme/infra","ref":"main","directory":"net"},"reason":"nightly plan"}' \
  "https://dustpan.example/api/dustpan/v1/namespaces/acme/jobs"
```

## Break a freeze with a reason

A person who holds the `namespace:break-freeze` action can start a job through
the freeze. The start then needs a `breakFreezeReason` in the body. The reason
must have 1 to 500 characters. The job record keeps the reason and the ID of the
freeze.

The action comes from the `freeze-breaker` role. This role is granted on the
installation, not on a namespace. A grant on a namespace does not give the
action. Use this command to grant the role. A caller that may manage access on
the installation can run it:

```sh
curl -X POST -H "Authorization: token $TOKEN" \
  -d '{"subject":"principal:prn-release-lead","role":"freeze-breaker"}' \
  "https://dustpan.example/api/server/grants"
```

The person also needs the role for the operation on the namespace. A plan
that names a runner pool needs the `admin` role on the namespace.

Then the person starts the job with a `breakFreezeReason`:

```sh
curl -X POST -H "Authorization: token $BREAK_TOKEN" \
  -d '{"unit":{"tool":"opentofu","name":"web"},"operation":"plan","pool":"jobs","location":{"repository":"acme/infra","ref":"main","directory":"net"},"reason":"incident fix","breakFreezeReason":"sev1 rollback"}' \
  "https://dustpan.example/api/dustpan/v1/namespaces/acme/jobs"
```

The server answers `201` with the job. A start with an empty `breakFreezeReason`
answers `400`. A start that asks for a break-glass without the break-freeze
action answers `409` with `job.frozen`, even when it has a reason.

To give the action to a person, see [roles and grants](roles-and-grants.md).

## End a freeze

`POST /api/dustpan/v1/namespaces/<namespace>/freezes/current/end` ends the
active freeze. The server records the end time and the subject that ended it.
The next job start is accepted.

```sh
curl -X POST -H "Authorization: token $TOKEN" \
  "https://dustpan.example/api/dustpan/v1/namespaces/acme/freezes/current/end"
```

If the namespace has no active freeze, the server answers `404` with the code
`job.not-found` and changes nothing. Run the list command to check the state
first.

## Errors and the command that helps

| Answer | What it means | The command that helps |
| --- | --- | --- |
| `409` with `job.frozen` on a start | The namespace has an active freeze, and the start changes a unit without the break-freeze action. | The list command above reads the reason. Ask a namespace administrator for the `freeze-breaker` role, or wait for the freeze to end. |
| `403` on a create or an end | The person lacks `namespace:manage-freezes` on the namespace. | Ask a namespace administrator for the `admin` role on the namespace. |
| `409` with `job.frozen` on a create | The namespace already has an active freeze. | The list command above shows the active freeze. |
| `404` with `job.not-found` on an end | The namespace has no active freeze. | The list command above shows the state. |
| `400` on a create or a start | The reason or the `breakFreezeReason` is empty or longer than 500 characters. | Send a reason of 1 to 500 characters. |
