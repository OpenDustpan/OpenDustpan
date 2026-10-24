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
handoff token for a job session is the fourth path that needs a nonce. It comes
with the session paths.

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

A release gives the claim back through the job service, with the same rules as
the end of a claim. When no step that changes resources began, the phase waits
for the next claim. Else the phase fails. A release of a claim that has a job
session fails with 409, because the launcher holds the claim.

The paths of the job session (the launcher), the steps, the logs and the plans
are not part of this API yet.

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
| `version-unsupported`, `version-ahead` | 426 | The release of the runner is outside the skew. |
| `rate-limited`, `unavailable` | 429, 503 | Wait for `Retry-After`. |
