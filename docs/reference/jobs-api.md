# Jobs API

A job is work that the server asks an executor to do on one stack or workspace: a plan, an
apply, a destroy or a refresh. The jobs API lets a person or a client start a
plan job, list jobs, read a job, its phases and the records of its phases, read
the log of a phase, and approve or reject a job that waits for approval.

This version starts plan jobs of OpenTofu workspaces on runners. The server defines the plan
step of a Pulumi stack too, but no runner runs it yet. No route
cancels jobs yet. The [drift API](drift-api.md) starts a drift
check, which is a job with the operation `refresh` and the purpose `drift-check`.
The job document has the field `purpose` for such a job.

## Paths

The paths of the API start with `/api/dustpan/v1`. Each path in this page is
relative to that prefix. The caller signs in as for the other APIs, with an
access token or a console session.

| Method and path | Action | Answer |
| --- | --- | --- |
| `POST /namespaces/{namespace}/jobs` | `namespace:read` on the namespace, and `unit:run-plan` on the workspace that the body names | The new job, with status 201 |
| `POST /namespaces/{namespace}/jobs/{job_id}/approvals` | `namespace:read` on the namespace, and `job:read` and `job:approve` on the stack or workspace of the job | The job after the verdict, with status 201 |
| `GET /namespaces/{namespace}/jobs` | `namespace:read` on the namespace. The server also checks `job:read` on the stack or workspace of each job | A page of jobs, newest first |
| `GET /namespaces/{namespace}/jobs/{job_id}` | `namespace:read` on the namespace, and `job:read` on the stack or workspace of the job | One job |
| `GET /namespaces/{namespace}/jobs/{job_id}/logs/{phase}` | `namespace:read` on the namespace, `job:read` on the stack or workspace of the job, and `job:read-logs` on the same object | The log of the phase, as a stream |

The server serves the `POST` route only when it has the stores of runners, the
inventory and the gates. Without them, the server serves the `GET` routes only. The
server serves the log route only when it has the stores that a log read needs: the
job store with its records, the blob store and the item keys.

A caller who cannot read the job gets the same 404 as for a job that does not
exist. The answer never tells that the job exists. This holds for a job ID that
is not valid, a job of another namespace than the one in the path, and a
caller who lacks `namespace:read` or `job:read`.

A caller who has `job:read` but not `job:read-logs` on the unit of the job gets 403 with the code
`access.denied` from the log route. The caller can see that the job exists, so the
answer does not hide it.

Any method other than `GET` on the path of one job gets 405.

## Start a job

The body is one JSON object. A field that is not in the table fails the
request with 400, so a client cannot send the source kind, the executor, the
steps or the profile of a job. The server sets them.

| Field | Content |
| --- | --- |
| `unit` | `tool` (`opentofu`) and `name`: the workspace to plan. The name has no control character. |
| `operation` | `plan`. Any other operation fails with 422 and the code `job.unsupported`. |
| `pool` | Optional. The name of a runner pool of the namespace. The pool runs jobs, not sweeps. With no pool, the server uses the only pool of the namespace that runs jobs. A namespace with no such pool, or with more than one, fails the start with 422 and the code `job.unsupported`. A request that names a pool also needs `namespace:manage-runners` on the namespace. |
| `location` | Where the program and the config are in git. Give `repository` and `ref`. The fields `connection`, `directory` and `revision` are optional. The directory is relative and stays in the repository. The revision is a commit ID. The `repository` is a path such as `acme/infra`, or an `https://` URL with no user name, password, query or fragment. The host of the URL is a DNS name. An IP address such as `10.0.0.5` or `[fd00::1]`, a number such as `2130706433`, and `localhost` are refused. Any other form fails with 400 and the code `job.invalid`. The error does not repeat the value. Other forms include `ssh://`, `file://`, `git@host:r.git`, and an `https://` URL with a user name or a password. The credential helper of the runner pool gives the credentials for the code. A request with no location fails with 422 and the code `job.unsupported`, because the server keeps no code location of a workspace yet. A request that names a location also needs `unit:define-steps` on the workspace. A `connection` must belong to the namespace. The server keeps no connections in this version, so a request that names one gets the same 403 as a refused workspace. |
| `reason` | Optional text for the audit entry. It has at most 500 characters and no control character. |

The job service does these checks in this order, and fails closed at each one:

1. The caller holds `unit:run-plan` on the workspace. A workspace that does
   not exist and a workspace that the caller may not use give the same 403.
   The access limits of the caller hold for this action: a limit that forbids
   `unit:run-plan` gives 403.
2. The caller holds the extra actions of the fields that the request names:
   `namespace:manage-runners` for a pool and `unit:define-steps` for a
   location. A caller without them learns nothing about the pools of the
   namespace.
3. The pool exists, runs jobs and belongs to the namespace. The connection of
   the location belongs to the namespace.
4. No enforced gate covers the workspace at `change.propose` or `state.write`.
   The server cannot evaluate these gates for a job yet, so it refuses the
   start with 403 and the code `job.gate-enforced`, and never skips a gate. The
   message names the gate. A gate in evaluate mode does not stop the start.
   The server checks these gates again at each claim of a phase by a runner,
   also at a takeover. A gate saved after the start fails the job at the
   claim, with a reason that names the gate (see the poll in
   [the runner API](runner-api.md)).
5. The approval defaults of the namespace, the collection and the workspace.
   A plan changes nothing, so it gets no approval gate.
6. The change freezes, the manual locks and the limits of open jobs. A manual
   lock or a job that holds the workspace queues the job with the wait reason
   `locked` or `unit-busy`. An active change freeze does not stop a plan.

A start writes one audit entry in the same transaction as the job. It names
the caller, the action, the workspace, the job ID, the source kind and the
reason. Each refusal of a start by a caller who may read the namespace writes
an entry of the refusal: a refused action, the policy, the agent mode, a
change freeze, the limit of open jobs, an enforced gate and a start that the
server cannot do. A request that is not valid writes no entry of a refusal.
For one caller, one action and one namespace, the server writes the first
entry of each window and counts the others.

The job starts with the source kind `api`. The source kind `schedule` and the
other kinds are for features of the server that start jobs. No request of the
API can set them.

The server does not decide the steps from the request. For an OpenTofu
workspace, the plan steps are `tofu init -input=false` and
`tofu plan -input=false -out=tfplan`. For a Pulumi stack, the plan step is
`pulumi preview --non-interactive --stack=<namespace>/<project>/<stack>`. It
changes no resource. A Pulumi job has no stored plan, so `job:read-plans` shows
nothing for it. The preview is in the log of the step, and the Pulumi CLI masks
it only. A caller reads the log with `job:read-logs`. A job starts with
no profile, so a runner gives it no secrets of a profile.

### Retry a start

Send the header `Idempotency-Key` with 1 to 255 printable ASCII characters.
Two starts with the same key and the same body, from one caller, give one job.
The second answer has the same status and the header `Idempotent-Replayed:
true`. The server compares the whole request: the workspace, the operation,
the pool, the location and the reason. The same key with another request fails
with 422 and the code `request.idempotency-key-reused`. A key belongs to its
caller: the same string from another caller makes another job. The server
keeps a key as long as it keeps the job.

### Answers

The answer to a start has the header `ETag` with the version of the job, in
quotes, and the header `Location` with the path of the job. The `GET` route
answers the `ETag` too.

Errors have the body `{"code": 400, "message": "...", "errorCode": "..."}`.
The `errorCode` is a stable code for a program.

| Status | `errorCode` | Cause |
| --- | --- | --- |
| 400 | `request.body-invalid` | The body is not one JSON object, has an unknown field, has no unit, or has more than 64 KiB |
| 400 | `request.idempotency-key-invalid` | The key is not valid, or there is more than one key |
| 400 | `job.invalid` | The operation, the unit name, the pool or the location is not valid |
| 401 | | The caller did not sign in |
| 403 | `access.denied` | The caller lacks `unit:run-plan`, or lacks `namespace:manage-runners` for a pool or `unit:define-steps` for a location. The connection of the location is not usable. The job token of a job tries to start a job. |
| 403 | `access.ip-not-allowed` | The client address is outside the IP allowlist of the namespace |
| 403 | `gate.limited` | An access limit forbids the start |
| 403 | `gate.rule-failed` | An access limit failed to evaluate, so the start is refused |
| 403 | `job.policy-denied` | The policy refused the start |
| 403 | `job.agent-denied` | The agent mode does not allow the start |
| 403 | `job.gate-enforced` | An enforced gate at `change.propose` or `state.write` covers the workspace, or the server cannot read the gates |
| 404 | | The namespace does not exist, or the caller cannot read it |
| 409 | `job.frozen` | A change freeze refuses the start |
| 409 | `namespace.fenced` | The primary installation of the namespace changed while the request ran: send the request again |
| 422 | `request.idempotency-key-reused` | The key names a job with another request |
| 422 | `job.unsupported` | The operation or the tool is not available in this version. The namespace has no default pool, or has more than one. The request names no code location. |
| 429 | `job.limit` | The workspace has 16 open jobs, or the namespace has 1,000 |

## Approve or reject a job

A job with a gate of type `approval` in front of its current phase waits in the
state `awaiting-approval`. `POST /namespaces/{namespace}/jobs/{job_id}/approvals`
records the verdict of the caller on that gate. The route takes no
`Idempotency-Key` header. A request with the header fails with 400.

The body is one JSON object. A field that is not in this table fails the request.

| Field | Meaning |
| --- | --- |
| `verdict` | `approve` or `reject`. Any other value fails with 422 and the code `job.invalid`. |
| `planBinding` | The plan that the caller saw. Send it with `approve` when a plan phase comes before the current phase. It has the fields `plan`, `displaySha256`, `applySha256` and `stateVersion`. Take them from the plan in the job document: its `id`, the `sha256` of its `plan-display` part, the `sha256` of its `plan-apply` part, and its `stateVersion`. A `reject` needs no binding. |
| `comment` | Optional text, at most 500 characters, with no control character and no line break. The server keeps it in the approval and in the audit log. |

The server applies these rules in this order. The first rule that fails ends
the request, and the server writes nothing to the job.

1. The caller needs `job:read` and `job:approve` on the stack or workspace of the job.
   A caller who cannot read the job gets 404.
2. The job must wait for approval. A job that ended answers 409 with the code `job.finished`.
   A job in another state answers 409 with the code `job.not-awaiting-approval`.
3. An agent cannot approve. An agent can reject.
4. The person who started the job cannot approve it, also when an agent started it for that
   person, unless the gate has `allowSelf`. That person can reject.
5. A person who approved this phase already cannot approve it again. The server answers 409
   with the code `job.already-approved`. A person who approved can still reject.
6. If a gate names groups, the caller must be an enabled person with an active membership
   in at least one of its groups. A suspended or ended membership does not count. The server compares the immutable ID of the group,
   so a group that someone renamed still matches, and a group that someone made again with
   the same name does not. A caller who is not a member, a server that cannot read the
   membership, and a gate that names groups with no group IDs all answer 403 with the
   code `access.denied`. A gate with no groups needs no membership.
7. The plan binding must be the binding of the stored plan. Another plan answers 409
   with the code `job.plan-changed`. A plan that is older than 24 hours, or that the
   server redacted, answers 409 with the code `job.plan-stale`. A job that waits for a
   plan that was never stored also answers `job.plan-stale`. If the state of the unit
   changes after the plan, the server finds it when it dispatches the apply phase, and
   the dispatch refuses the approvals then.
8. The approval of a `destroy` job needs a fresh MFA step of the caller, at most 15
   minutes old. A rejection needs none.

One approval keeps the job in `awaiting-approval` until the gate has all the approvals
that it needs from distinct people. The last approval moves the job to `queued`. A
rejection moves the job to `rejected` at once. Any caller with `job:approve` on the unit
can reject, also a caller who is not in the groups of the gate, because a rejection only
stops the change. Each verdict adds one entry of the kind `change` to the audit log of the namespace. The
entry has the caller, the verdict, the phase, the comment and the event that followed.

A refusal with the code `access.denied`, `job.agent-denied`, `job.self-approval`,
`job.already-approved`, `job.auth-stale`, `job.plan-changed` or `job.plan-stale` adds one entry with the outcome
`denied` to the audit log. A request that is not valid, a job that does not wait, and a
version conflict add none.

This version cannot give the server the MFA step of a console session yet. So the
server refuses the approval of a destroy with 403 and the code `job.auth-stale`, and
changes nothing. A rejection of a destroy works.

Example:

```text
POST /api/dustpan/v1/namespaces/acme/jobs/job-4kd9q2m7xw1ztr8bnhyc/approvals
{
  "verdict": "approve",
  "planBinding": {
    "plan": "pln-3fa7c1d0e9b24a68",
    "displaySha256": "5d41402abc4b2a76b9719d911017c592a0f1a5d0cc66a4fbb4a47ab1d53f1c8e",
    "applySha256": "7d793037a0760186574b0282f2f435e7b5e3b4d5a3c8c2a9f6c1e0d4b8a79f21",
    "stateVersion": "sv-17"
  },
  "comment": "The plan adds two subnets and changes nothing else."
}
```

The answer is the job document with status 201 and the header `ETag`.

| Status | `errorCode` | Cause |
| --- | --- | --- |
| 400 | `request.body-invalid` | The body is not one JSON object, has an unknown field, or has an `Idempotency-Key` header |
| 401 | | The caller did not sign in |
| 403 | `access.denied` | The caller lacks `job:approve` on the unit, or is not a member of a group of the gate |
| 403 | `job.agent-denied` | An agent tried to approve |
| 403 | `job.self-approval` | The person who started the job tried to approve it |
| 403 | `job.auth-stale` | The approval of a destroy has no fresh MFA step |
| 404 | `job.not-found` | The namespace or the job does not exist, or the caller cannot read it |
| 409 | `job.already-approved` | The caller approved this phase already |
| 409 | `job.not-awaiting-approval` | The job waits for no approval |
| 409 | `job.finished` | The job ended |
| 409 | `job.plan-changed` | The plan binding is not the binding of the stored plan |
| 409 | `job.plan-stale` | The stored plan is too old, the server redacted it, or no plan exists |
| 409 | `job.conflict` | The job changed while the request ran: read the job again |
| 413 | `request.body-too-large` | The body has more than 16 KiB |
| 422 | `job.invalid` | The verdict is not `approve` or `reject`, the comment is not valid, or a text of `planBinding` is too long |

## List jobs

`GET /namespaces/{namespace}/jobs` answers a page of the jobs of the namespace,
newest first. The page holds only the jobs whose stack or workspace the caller
can read with `job:read`. A job that the caller cannot read is not in the page.
Each job has the form of [the job document](#the-job-document).

| Query value | Meaning |
| --- | --- |
| `unit` | Keep the jobs of the stack or workspace with this unit ID |
| `status` | Keep the jobs in this state, for example `running` or `failed`. Give the value more than once to keep the jobs in any of the states |
| `purpose` | Keep the jobs with this purpose. The only value is `drift-check` |
| `trigger` | Keep the jobs that the run trigger with this ID started |
| `pageSize` | The largest number of jobs on the page, from 1 to 200. The default is 50 |
| `pageToken` | The `nextPageToken` of the page before, for the same query |

The answer is `{"jobs": [...], "nextPageToken": "..."}`. The field `nextPageToken`
is not in the answer of the last page. The token is opaque: the server seals its cursor, so a token never names a job. A token works for the query that made it. A
token that was changed, or that another query made, gets 400 with the code
`request.page-token-invalid`. A value that is not valid gets 400 with the code
`request.invalid-query`. One request reads at most 2,000 jobs from the store. When it
reaches that limit, the page can hold fewer jobs than `pageSize`, and it still has a
`nextPageToken`. Continue with the token.

## Read a job log

`GET /namespaces/{namespace}/jobs/{job_id}/logs/{phase}` streams the log of one
phase. The `phase` is `plan`, `apply` or `deliver`. The log is the logs of the
steps of the phase, in the order of the step records.

The answer has the content type `application/x-ndjson`: one JSON object on each
line. Each line of the log is one entry:

```json
{"seq": 1, "step": "plan", "text": "Initializing...\n"}
```

- `seq` counts the entries of the phase from 1. A line that is longer than
  16 KiB is in more than one entry. A byte that is not UTF-8 is in `text` as
  U+FFFD.
- A last line with no newline is sent when the log of its step closes.

When the phase runs, the stream stays open and sends new entries when they are
stored. When the phase ended and the stream sent all of the log, the last line is
`{"done": true, "last": 2}`, where `last` is the `seq` of the last entry. A stream
that ends with no `done` line is not complete.

To continue after a break, send the query value `after` with the `seq` of the last
entry that the client has. The stream starts with the next entry. The default is
`0`. A value that is not a whole number from 0 gets 400 with the code
`request.invalid-query`.

The server checks each chunk of the log before it sends the bytes of the chunk.
A stream has these limits:

- A stream lives at most 10 minutes. At that limit, the last line is
  `{"done": false, "last": 2, "resume": true}`. Open the stream again with
  `?after=2`, where `2` is the `last` value.
- When the server stops, the stream ends with the same `resume` line.
- The server sets a time limit of 30 seconds on each write. A client that does
  not read ends its stream at that limit.
- The server checks `namespace:read`, `job:read` and `job:read-logs` again at each
  poll. If the caller lost one of them, the stream ends with an error line with
  the reason `access.denied`.
- A principal can have 8 open streams, and the server can have 256. A stream over
  a limit gets 429 with the code `job.limit`. Close a stream and try again.

When a chunk fails its check, or the log was redacted, the stream ends with the line
`{"error": {"reason": "...", "message": "..."}}` and no `done` line. The status is
200 by then, so a client must read the last line. The `reason` is a code of the catalog. The server never sends the bytes
of a chunk that failed its check.

| Status | `errorCode` | Cause |
| --- | --- | --- |
| 400 | `request.invalid-query` | The `after` value is not valid |
| 403 | `access.denied` | The caller has `job:read` but lacks `job:read-logs` |
| 429 | `job.limit` | The principal has 8 open log streams, or the server has 256 |
| 404 | `job.not-found` | The namespace or the job does not exist in the path, the phase is not `plan`, `apply` or `deliver`, or the caller cannot read the job |
| 404 | `job.log-not-available` | The phase did not start, so it has no log yet |

## What the server does with a job on its own

The server runs the schedule `job.reconcile` once each minute. One instance
runs each tick. The schedule does these steps in this order:

1. It ends each job that is `dispatched`, `running` or `canceling` while the
   durable operation of its phase ended with no result for the job, or does not
   exist. A restore of a backup makes jobs of the second kind. A `dispatched`
   job or a `running` job ends `failed`. A `canceling` job ends `canceled`,
   unless a step that changes resources began. Then it ends `failed`. The
   `reason` of a job that a restore left is `restored`.
2. It ends each job that stays too long in one state, with the state `expired`
   and a `reason` that names the state and the time. A job does not start again
   after this. The limits are 7 days for `queued`, 1 day for `dispatched` and
   14 days for `awaiting-state`. The time starts when the job enters the state.
3. It dispatches each `queued` job whose wait ended. A job whose gate needs
   approval goes to `awaiting-approval`. A job whose plan is stale goes back to
   its plan phase. A job that still waits for a busy workspace, a change freeze
   or a manual lock stays `queued`, and the server writes nothing for it.

Each change that the schedule makes writes an audit entry. The subject of the
entry is `@server`, the principal kind is `service`, and the entry holds the
job ID. The reason starts with `job.reconcile:` and says why the schedule made
the change. A server with no durable store does not run the
schedule.

## The job document

| Field | Content |
| --- | --- |
| `id` | The job ID, which starts with `job-` |
| `namespace` | The name of the namespace |
| `unit` | `id` (the stable ID of the stack or workspace), `tool` (`Pulumi` or `OpenTofu`) and `name` (the name in its tool at the start) |
| `operation` | `plan`, `apply`, `destroy` or `refresh` |
| `executor` | `runner` or `write-back` |
| `state` | `queued`, `awaiting-approval`, `dispatched`, `running`, `canceling`, `awaiting-state`, `succeeded`, `failed`, `canceled`, `rejected` or `expired` |
| `wait` | Why a queued job does not dispatch: `unit-busy`, `frozen` or `locked`. The field is not there when nothing holds the job. |
| `reason` | Why a job that ended did end. The field is not there for a job that runs. |
| `location` | Where the program and the config of the stack or workspace are in git: `connection`, `repository`, `ref`, `directory` and `revision`. A stored `repository` that the server does not accept is answered empty, so the answer never holds credentials. |
| `pool` | The name of the runner pool of a runner job. A start request takes the same value. The field is not there when the pool no longer exists. It is not there when the pool belongs to another namespace. A `POST` that made the job cannot always read the pool. Then the answer is 201 with no `pool`. The server writes a warning with the job ID to its log. A `GET` that cannot read the pool answers 500. |
| `poolID` | The ID of the runner pool of a runner job. The field stays when the pool no longer exists. |
| `profile` | The name of the profile that gives the secrets to a runner job. The field is not there when the job has no profile. |
| `defaulted` | The values that the request did not give and that the server took by a rule. A `pool` comes from the only pool of the namespace that runs jobs. A `location` and a `profile` come from the settings of the unit. The list holds `pool`, `location` or `profile`, in that order. The field is not there when the request gave every value. The server fixes the list when the job starts. A later change of the settings changes neither the values nor the list. |
| `steps` | The steps of a runner job. Each step has `name`, `role`, `phase` and `changesResources`. |
| `phases` | The phases of the job, in order (see below) |
| `current` | The index of the phase that runs or runs next. It equals the number of phases after the last phase. |
| `startedBy`, `agent`, `agentMode` | Who started the job, and the agent that acted for them |
| `source` | Where the start came from: `api`, `agent`, `self-service`, `forge`, `schedule` or `trigger` |
| `breakGlass` | `reason` and `freeze`, when the job started through an active change freeze |
| `trigger` | `id` of the trigger that started the job, and `saver`, the subject of its last saver at the start. The field is not there when no trigger started the job. See the [triggers guide](../guides/run-triggers.md). |
| `delivery` | The result of the deliver phase of a write-back job |
| `closed` | The state version that ended a write-back job |
| `verification` | `pending`, `verified` or `unverified`, after the job closed |
| `version` | A number that grows with each change of the job record |
| `createdAt`, `updatedAt` | Times in RFC 3339 format |

The `unit.name` field keeps the name from the start of the job. A rename does
not change it. To find the current stack or workspace, use `unit.id`.

A step never shows its arguments or its variables. The document does not show
the idempotency key, the durable operation of a phase or
the item key of a plan or of a log.

### Phases

| Field | Content |
| --- | --- |
| `kind` | `plan`, `apply` or `deliver` |
| `claims` | How many claims the phase had. A takeover adds one. |
| `executor` | The runner or the instance that holds or held the last claim |
| `stateVersion` | The state version of the stack or workspace that a plan phase read |
| `steps` | The records of the steps that ran: `name`, `exitCode`, `startedAt`, `finishedAt` and the metadata of the log (`id`, `version`, `size`, `closed`, `redacted`) |
| `plan` | The metadata of the stored plan: `id`, `stateVersion`, `parts`, `createdAt`, `expiresAt` and `redactedAt`. A part has `kind`, `size`, `sha256` and `secret`. |
| `gates` | The gates in front of the phase: `type`, `approvals`, `groups`, `allowSelf` and `bindPlan` |
| `approvals` | The approvals and confirmations that the gates got: `gate`, `subject`, `agent`, `verdict`, `comment` and `at` |
| `startedAt`, `finishedAt` | Times in RFC 3339 format. A field is not there until the event happened. |

The document holds the metadata of plans and logs. It never holds their
content.
