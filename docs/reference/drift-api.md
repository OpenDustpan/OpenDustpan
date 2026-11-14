# Drift API

A drift check is a job that looks for a difference between the state of a unit
and the real resources that the state names. The drift API lets a person or a
client start a drift check of an OpenTofu workspace and read the latest drift
result of a workspace. The guide `docs/guides/drift-checks.md` shows how to use
it.

A runner cannot record a drift result yet. A workspace that no check finished has
the status `unknown`.

## Paths

The paths of the API start with `/api/dustpan/v1`. Each path in this page is
relative to that prefix. The caller signs in as for the other APIs, with an
access token or a console session. Both paths name the workspace by its unit ID.

| Method and path | Action | Answer |
| --- | --- | --- |
| `POST /namespaces/{namespace}/units/{unit_id}/drift-checks` | `namespace:read` on the namespace, `unit:read` and `unit:run-plan` on the workspace | The job that runs the check, with status 202 |
| `GET /namespaces/{namespace}/units/{unit_id}/drift` | `namespace:read` on the namespace, and `unit:read` on the workspace | The latest drift result, with status 200 |

The server serves the `POST` route only when it has the stores of runners, the
inventory and the gates. Without them, the server serves the `GET` route only.
The server serves no route of this API without the drift store.

A caller who cannot read the workspace gets the same 404 as for a workspace that
does not exist. This holds for a unit ID that is not valid, a workspace of
another namespace than the one in the path, a workspace that the server deleted,
and a caller who lacks `namespace:read` or `unit:read`. A caller who can read
the workspace and lacks `unit:run-plan` gets 403 for the start.

## Start a drift check

The body is optional. An empty body is the same as `{}`. A body is one JSON
object of at most 64 KiB. A field that is not in the table fails the request with
400 and the code `request.body-invalid`.

| Field | Content |
| --- | --- |
| `pool` | Optional. The name of a runner pool of the namespace. The pool runs jobs, not sweeps. With no pool, the server uses the only pool of the namespace that runs jobs. A request that names a pool also needs `namespace:manage-runners` on the namespace. |
| `location` | Optional. Where the program and the config are in git: `repository` and `ref`, and the optional `connection`, `directory` and `revision`. The `repository` is a path such as `acme/infra`, or an `https://` URL with no user name, password, query or fragment. The host of the URL is a DNS name. An IP address such as `10.0.0.5` or `[fd00::1]`, a number such as `2130706433`, and `localhost` are refused. Any other form fails with 400 and the code `job.invalid`. The error does not repeat the value. Other forms include `ssh://`, `file://`, `git@host:r.git`, and an `https://` URL with a user name or a password. The credential helper of the runner pool gives the credentials for the code. A request with no location fails with 422 and the code `job.unsupported`, because the server keeps no code location of a workspace yet. A request that names a location also needs `unit:define-steps` on the workspace. |
| `reason` | Optional text for the audit entry. It has at most 500 characters, and no control character, format character, line separator (U+2028) or paragraph separator (U+2029). |

The route takes no `Idempotency-Key` header, and a request with the header gets
400. The server makes one job for two starts of one workspace in the same minute.

The job service checks the start action again and applies the same rules as for
any job: the access limits of the caller, the gates, the approval defaults, the
manual locks, the change freezes and the limits of open jobs. A drift check has
the plan phase alone, so it never waits for an approval. A manual lock queues
the job with the wait reason `locked`. A change freeze does not hold it.

The answer has these fields:

| Field | Content |
| --- | --- |
| `jobId` | The ID of the job. Read the job with the jobs API. |
| `unit` | The `id`, the `tool` and the `name` of the workspace. |

The `Location` header names the job.

| Status | Code | Meaning |
| --- | --- | --- |
| 400 | `request.body-invalid` | The body or the header is not valid. |
| 400 | `job.invalid` | The pool or the location is not valid, or the reason is not valid. |
| 403 | | The caller lacks an action. |
| 403 | `access.ip-not-allowed` | The client address is outside the IP allowlist of the namespace. The read of the drift status answers this code too. |
| 403 | `job.policy-denied`, `job.gate-enforced`, `job.agent-denied` | A rule of the server refuses the start. |
| 409 | `job.frozen`, `namespace.fenced` | A freeze or a failover refuses the start. |
| 409 | `drift.start-conflict` | The same caller started a drift check of this workspace in the same minute with another pool, location or reason. Wait one minute, or send the same request again. |
| 422 | `job.unsupported` | The server cannot start this job: no pool, more than one pool, no code location, or a tool with no refresh-only plan. |
| 429 | `job.limit` | The namespace has too many open jobs. |

A start writes one audit entry in the same transaction as the job. Each refusal of
a start by a caller who may read the workspace writes an entry of the refusal.

## Read the latest drift result

The answer has these fields:

| Field | Content |
| --- | --- |
| `unit` | The `id`, the `tool` and the `name` of the workspace. |
| `status` | `drifted`, `in-sync` or `unknown`. |
| `checkedAt` | The end of the plan phase of the job that made the result. Absent for the status `unknown`. |
| `jobId` | The job that made the result. Absent for the status `unknown`. |
| `resources` | The resources that differ, at most 1,000. Each has an `address` and a `kind`: `changed` or `deleted`. An empty list for a result with no difference and for the status `unknown`. |
| `truncated` | True when the check found more than the 1,000 addresses that the list holds. |

A client must keep a status or a kind that it does not know, because the sets can
grow. The result never holds an attribute name or an attribute value.

The server shows the status `unknown` and no other field for a workspace that no
check finished. It never shows `in-sync` for such a workspace.

## The drift section of the context document

`GET /namespaces/{namespace}/units/{unit_id}/context?include=drift` answers the
latest drift result in the section `drift`. The section is not a default
section. It needs `unit:read` on the workspace, the object `target`. A caller
who lacks the action gets the 404 of an object that the caller cannot read, as
for every section of the unit.

The `data` of the section has these fields:

| Field | Content |
| --- | --- |
| `status` | `drifted`, `in-sync` or `unknown`. |
| `checkedAt` | The end of the plan phase of the job that made the result. Absent for the status `unknown`. |
| `jobID` | The job that made the result. Absent for the status `unknown`. |
| `addresses` | The addresses of the resources that differ, at most 100. An empty list for a result with no difference and for the status `unknown`. |
| `truncated` | True when the result has more addresses than the list holds. |

The `status` of the section is `ok`, or `partial` when the list is cut. It is
`unavailable`, with no `data`, when the server has no drift store. The section
for a resource of a workspace lists the address of the resource alone, and only
when the latest result lists it. The section never holds an attribute name or an
attribute value, and it holds no `kind`. Read the `kind` of each address from the
drift read above.

## The jobs API

The job document of the jobs API has the field `purpose`. For a drift check it
is `drift-check`. For any other job the field is absent.
