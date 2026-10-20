# Jobs API

A job is work that the server asks an executor to do on one unit: a plan, an
apply, a destroy or a refresh. The jobs API lets a person or a client read a
job, its phases and the records of its phases.

This version of the API reads jobs only. No route starts, cancels or approves
a job yet.

## Paths

The paths of the API start with `/api/dustpan/v1`. Each path in this page is
relative to that prefix. The caller signs in as for the other APIs, with an
access token or a console session.

| Method and path | Action | Answer |
| --- | --- | --- |
| `GET /namespaces/{namespace}/jobs/{job_id}` | `namespace:read` on the namespace, and `job:read` on the unit of the job | One job |

A caller who cannot read the job gets the same 404 as for a job that does not
exist. The answer never tells that the job exists. This holds for a job ID that
is not valid, a job of another namespace than the one in the path, and a
caller who lacks `namespace:read` or `job:read`.

Errors have the body `{"code": 404, "message": "not found"}`. Any method other
than `GET` on this path gets 405.

## The job document

| Field | Content |
| --- | --- |
| `id` | The job ID, which starts with `job-` |
| `namespace` | The name of the namespace |
| `unit` | `id` (the stable unit ID), `tool` (`Pulumi` or `OpenTofu`) and `name` (the name of the unit in its tool) |
| `operation` | `plan`, `apply`, `destroy` or `refresh` |
| `executor` | `runner` or `write-back` |
| `state` | `queued`, `awaiting-approval`, `dispatched`, `running`, `canceling`, `awaiting-state`, `succeeded`, `failed`, `canceled`, `rejected` or `expired` |
| `wait` | Why a queued job does not dispatch: `unit-busy`, `frozen` or `locked`. The field is not there when nothing holds the job. |
| `reason` | Why a job that ended did end. The field is not there for a job that runs. |
| `location` | Where the program and the config of the unit are in git: `connection`, `repository`, `ref`, `directory` and `revision` |
| `pool` | The runner pool of a runner job |
| `steps` | The steps of a runner job. Each step has `name`, `role`, `phase` and `changesResources`. |
| `phases` | The phases of the job, in order (see below) |
| `current` | The index of the phase that runs or runs next. It equals the number of phases after the last phase. |
| `startedBy`, `agent`, `agentMode` | Who started the job, and the agent that acted for them |
| `breakGlass` | `reason` and `freeze`, when the job started through an active change freeze |
| `delivery` | The result of the deliver phase of a write-back job |
| `closed` | The state version that ended a write-back job |
| `verification` | `pending`, `verified` or `unverified`, after the job closed |
| `version` | A number that grows with each change of the job record |
| `createdAt`, `updatedAt` | Times in RFC 3339 format |

A step never shows its arguments or its variables. The document does not show
the idempotency key, the runner profile, the durable operation of a phase or
the item key of a plan or of a log.

### Phases

| Field | Content |
| --- | --- |
| `kind` | `plan`, `apply` or `deliver` |
| `claims` | How many claims the phase had. A takeover adds one. |
| `executor` | The runner or the instance that holds or held the last claim |
| `stateVersion` | The state version of the unit that a plan phase read |
| `steps` | The records of the steps that ran: `name`, `exitCode`, `startedAt`, `finishedAt` and the metadata of the log (`id`, `version`, `size`, `closed`, `redacted`) |
| `plan` | The metadata of the stored plan: `id`, `stateVersion`, `parts`, `createdAt`, `expiresAt` and `redactedAt`. A part has `kind`, `size`, `sha256` and `secret`. |
| `gates` | The gates in front of the phase: `type`, `approvals`, `groups`, `allowSelf` and `bindPlan` |
| `approvals` | The approvals and confirmations that the gates got: `gate`, `subject`, `agent`, `verdict`, `comment` and `at` |
| `startedAt`, `finishedAt` | Times in RFC 3339 format. A field is not there until the event happened. |

The document holds the metadata of plans and logs. It never holds their
content.
