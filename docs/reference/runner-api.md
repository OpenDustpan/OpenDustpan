# Runner API

The runner API has two parts. A person or a client manages runner pools, join
tokens and runners with the management API. A runner uses the runner protocol
to join a pool, to claim work, and to keep its claims.

The server serves both parts only when the installation has a public URL. The
join challenge of a runner names that URL, without a trailing slash. A runner
must use the same text.

## The management API

The paths of the management API start with `/api/runners/{namespace}`. The
caller signs in as for the other APIs, with an access token or a console
session. A caller who cannot read the namespace gets 404. A caller who can read
it, but lacks the action of the route, gets 403.

| Method and path | Action | Answer |
| --- | --- | --- |
| `GET /pools` | `namespace:read-runners` | The pools of the namespace |
| `POST /pools` | `namespace:manage-runners` | A new pool |
| `GET /pools/{pool}` | `namespace:read-runners` | One pool |
| `PATCH /pools/{pool}` | `namespace:manage-runners` | The pool with the changed fields |
| `DELETE /pools/{pool}` | `namespace:manage-runners` | No body |
| `GET /pools/{pool}/join-tokens` | `namespace:read-runners` | The join tokens, with no secret |
| `POST /pools/{pool}/join-tokens` | `namespace:manage-runners` | A new join token, with its secret |
| `POST /pools/{pool}/join-tokens/{id}/revoke` | `namespace:manage-runners` | No body |
| `GET /pools/{pool}/runners` | `namespace:read-runners` | The runners of the pool that are not retired |
| `POST /runners/{runner}/drain` | `namespace:manage-runners` | No body |
| `POST /runners/{runner}/retire` | `namespace:manage-runners` | No body |

`{pool}` is the ID of the pool or its name. Each change writes one audit entry.

Errors have the body `{"code": 404, "message": "..."}`.

The source filter of a namespace has its own page: [Source filter API](source-filter-api.md).

### Pools

A pool belongs to one namespace. Its purpose is `jobs` or `sweeps`, and it
never changes. The body of `POST /pools` has these fields:

| Field | Content |
| --- | --- |
| `name` | 1 to 63 lowercase letters, digits and `-` |
| `purpose` | `jobs` or `sweeps` |
| `lease` | The lease of a claim, in milliseconds, from 30 000 to 600 000. Zero takes 90 000. |
| `concurrency` | The phases that one runner runs at a time, from 1 to 64. Zero takes 1. |
| `isolation` | The isolation kinds that a runner may use. An empty list allows every kind but `process`. |
| `gitCredentials` | `mode` (`none`, `forge` or `custom`) and `helper`. Only the mode `custom` takes a `helper`: a bare name that starts with `git-credential-`, or a clean absolute path, of letters, digits and the characters `.` `_` `/` `-`. Another helper fails with 400. |

`PATCH /pools/{pool}` takes the same fields, and every field is optional. A
field that the body leaves out keeps its value. A `purpose` that differs from
the pool fails with 409. The answer is the pool, and the change writes one audit
entry that lists each field that changed. A runner that joined with an
isolation that the new settings refuse keeps its key, but claims nothing until
an administrator retires it or changes the pool again.

A namespace has at most 50 pools. `DELETE` fails with 409 while a phase or a
sweep that did not end names the pool.

### Join tokens

`POST /pools/{pool}/join-tokens` has an optional body `{"ttl": <milliseconds>}`.
A token lives 1 hour by default and at most 7 days. The answer holds the
secret in `token` and a `command` for the runner. The secret starts with
`dpj_`. The server shows it once and keeps only its SHA-256. A pool has at most
100 open join tokens. A join token is good for one join. A used token cannot be
revoked.

### Runners

An entry of the runner list has the fields `id`, `pool`, `name`, `kind`,
`isolation`, `labels`, `release`, `status`, `draining`, `keyID`, `keyExpires`,
`lastSeenAt` and `joinedAt`. The status is `online`, `offline`, `draining` or
`must-rejoin`. A runner with the status `must-rejoin` also has
`rejoinCommand`, with the URL, the pool and the name filled in.

A draining runner claims no new work. A retired runner authenticates nothing.
The retire ends its claims in the same transaction, and the next claim of each
operation takes it over.

## The runner protocol

The paths of the protocol start with `/runner/v1`. The runner opens every
connection, over HTTPS. The server never connects to a runner. The bodies are
JSON. Every request has the header `Dustpan-Release` with the release of the
sender. Every answer has it with the release of the server. A runner may be up
to two minor releases older than the server. A newer release gets 426 with the
code `version-ahead`, and an older one gets 426 with `version-unsupported`.

| Method and path | Body | Answer |
| --- | --- | --- |
| `POST /join` | join request | the runner ID, the pool, the lease |
| `POST /rotate` | the new key and its signature | 204 |
| `POST /heartbeat` | the claims of the runner | claims to cancel, lost claims, `rotateKey`, `draining` |
| `POST /poll` | the longest wait, at most 50 seconds | 200 with one phase or sweep, or 204 |
| `POST /claims/{operation}/handoff` | none | the handoff token |
| `POST /claims/{operation}/renew` | none | the new expiry, `canceled`, `sessionOpen` |
| `POST /claims/{operation}/release` | the reason | 204, or 409 when the claim has a job session |

Every path but the join needs the header `Dustpan-Proof`:
`v1 sub=<runner ID> t=<Unix milliseconds> n=<nonce in hex> sig=<signature in base64url>`.
The signature is an Ed25519 signature of the runner key. It covers the
subject, the method, the path with its query, the SHA-256 of the body, the time
and the nonce. The server refuses a time more than 60 seconds from its clock.
On `/rotate`, `/poll` and `/claims/{operation}/handoff`, it keeps the nonce for
two minutes and refuses a second proof with the same nonce. The exchange of a
handoff token for a job session (`POST /session`) is the fourth path that needs
a nonce.

The join has no proof of an existing key. The join token and the signature of
the new key over the join challenge authenticate it. A join with the name of a
runner that is online fails with 409. A key older than 30 days gets
`key-expired`, and the runner must join again with a new join token.

A poll takes the work from the pool of the runner. A runner never names the
kinds. The poll has the highest assignment version that the launcher of the
runner reads, and the server never claims a phase or sweep above that version.
A runner has one poll at a time. A second poll while the first runs gets 429.
This holds for each server instance: two polls of one runner on two instances
can both run. A
draining or retired runner gets 204 at once, also when the drain starts during
a long poll. When the lease of a claim lapses,
the next poll of any runner of the pool can take the work with a new claim
token. The runner that held the lapsed claim gets `claim-lost` on its next
renewal, and the claim is in `lost` in its next heartbeat. The runner renews a
claim after a third of the lease.

Before a poll gives a phase of a job to a runner, the server checks the gates
of the unit of the job again, as at the start of the job. An enforced gate at
`change.propose` or `state.write`, also one saved after the start, refuses the
claim. A gate in evaluate mode refuses nothing. When the server cannot read
the gates, it refuses the claim too. A refused claim fails the phase and the
job, with a reason that names each gate. The job does not wait for the gate
to go: a person deletes the gate and starts a new run. The runner gets no
phase from it,
and the poll looks for other work of the pool. The refusal writes an audit
entry with the action `runner:claim`, the outcome `denied`, the runner, the
unit, the job ID and the reason.

The launcher of a claim must open its job session within 30 minutes of the
claim. After that time, a renewal of a claim with no job session gives the
claim back through the job service, with the same rules as a release, and
answers `claim-lost`.

A release gives the claim back through the job service, with the same rules as
the end of a claim. When no step that changes resources began, the phase waits
for the next claim. Else the phase fails. A release of a claim that has a job
session fails with 409, because the launcher holds the claim.

## The job session

The launcher of a claim exchanges its handoff token for a job session, and then
calls these paths. They need a job session and never accept a runner key. A
runner key cannot call them.

| Method and path | Body | Answer |
| --- | --- | --- |
| `POST /session` | the handoff token, the new public key, the signature of the session challenge, the release | the session, its lease, and the assignment |
| `POST /session/renew` | none | the new expiry, `canceled` |
| `POST /session/steps/{step}/begin` | none | 204 |
| `POST /session/steps/{step}/end` | the exit code, the times, the number of chunks and the SHA-256 of the log | 204 |
| `PUT /session/steps/{step}/logs/{seq}` | one chunk of the log, at most 1 MiB | 204 |
| `PUT /session/plan/uploads/{part}` | one part of the plan, `plan-display` or `plan-apply` | 204 |
| `POST /session/plan/commit` | the kind, the size and the SHA-256 of each part | 204 |
| `GET /session/plan/parts/{part}` | none | the part, with its size and hash in the headers |
| `POST /session/finish` | `succeeded` and a reason | 204 |

The exchange has no proof of an existing key. The handoff token and the
signature of the new key authenticate it. Its `Dustpan-Proof` subject is the
SHA-256 of the handoff token in hex, and the new key signs the proof. A second
exchange with one token gets `token-used`. The exchange of a claim that is not
live, or that the launcher asks for more than 30 minutes after the claim, gets
`claim-lost`. The assignment holds the steps of the phase, the unit, the tool
and the git credential mode of the pool. It holds no secret. It has no source
and no plan yet.

Every other path of the session has a `Dustpan-Proof` whose subject is the
session ID, signed with the key of the session. A session that does not exist,
and a proof that does not match its key, get the same `bad-proof` answer. A
session whose claim is no longer live gets `claim-lost`. This holds for a lease
that passed, for a claim that another runner took, and for a claim that the
launcher ended. A runner that an administrator retired gets `retired`.

A renewal moves the expiry of the claim to the lease of the pool from now. It
also works during a cancel, and then it answers `canceled: true`. The launcher
stops its step with the grace period of the step, and still sends its last log
chunks and its finish. During a cancel, the request for a new step gets `canceled`.

A log chunk has a number from 0. If the launcher sends a chunk again with the same
number and the same body, the server stores it once. A known number with another body
gets `conflict`. The header `Dustpan-Content-SHA256` has the SHA-256 of the body
in hex, and the proof signs that value. The server compares it with the hash of
the bytes that it received before it stores any of them, and answers
`bad-proof` for another body. A step has at most 20,000 chunks.

The end of a step names the number of chunks and the SHA-256 of the whole log.
The server compares both with the log that it stored, and answers `conflict`
when they differ. A step end that repeats the first one changes nothing. A
finish ends the phase once. A second finish gets `claim-lost`, because the
phase has no live claim.

A plan phase stores its plan in two kinds of call, as a multipart upload does.
Each `PUT /session/plan/uploads/{part}` seals one part with the key of the plan
and stores it. The header `Dustpan-Content-SHA256` has the SHA-256 of the part in
hex, the proof signs that value, and the server deletes the part and answers
`bad-proof` when the bytes that it received have another hash. A part has at
most 256 MiB. The server holds the part in memory only while it streams it. It
keeps no plaintext on disk, so any instance of the server can serve any call of
the session. A part that the launcher sends again with the same content changes
nothing.

A part is not readable until the commit. `POST /session/plan/commit` names the
parts of the plan (one or two). The server reads each stored part back and
compares its size and its SHA-256 with the named ones. When one differs, the
server answers `conflict` and stores no plan. The staged parts stay, and the
launcher can send the right commit. When all match, the server stores the plan
with the state version of the phase, in one transaction. A plan that read an
old state version fails the phase, and the answer is `plan-stale`. A second
commit with the same parts changes nothing and answers 204, so a launcher can
send its commit again when it lost the answer. A commit with other parts, and
an upload after the commit, get `conflict` and change nothing.

A part that no plan names is not kept. The artifact sweep deletes the parts of
a plan that never committed when the claim that uploaded them is no longer live,
and it deletes each part that the committed plan does not name. It deletes only
blobs that are older than one hour, and it keeps the parts of a claim that is
live.

`GET /session/plan/parts/{part}` is for an apply phase. It streams one part of
the plan that the job stored in its plan phase. The plan comes from the job of
the session, never from the request, so a launcher cannot read the plan of
another job. The headers `Content-Length` and `Dustpan-Content-SHA256` have the
size and the hash that the server stored, so the launcher checks the bytes
that it received. A plan that a redaction deleted, and a part that the plan does
not have, get `not-found`.

The paths for the source, the credentials and the report of a sweep are not
part of this API yet. A session of a sweep has no steps, no logs, no plan and no
finish.

## Errors of the protocol

Each answer with a status of 400 or more has the body
`{"code": "...", "message": "..."}`.

| Code | Status | Meaning |
| --- | --- | --- |
| `invalid` | 400 | The request has a bad or unknown field. |
| `bad-proof` | 401 | The proof does not match, is too old, or repeats a nonce. |
| `key-expired` | 401 | The runner must join again. |
| `token-used`, `token-expired` | 401 | The join token or the handoff token cannot be used. |
| `retired` | 403 | An administrator retired the runner. |
| `not-found` | 404 | The token, the pool or the claim is not known. |
| `claim-lost` | 409 | The claim is not live. The runner stops the work. |
| `conflict` | 409 | The request conflicts with the state of the server. |
| `plan-stale` | 409 | The plan read an old state version. The phase failed. |
| `version-unsupported`, `version-ahead` | 426 | The release of the runner is outside the skew. |
| `rate-limited`, `unavailable` | 429, 503 | Wait for `Retry-After`. |
