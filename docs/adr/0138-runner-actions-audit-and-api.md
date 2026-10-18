# 138. Runner actions, audit entries and the REST API

Status: accepted · 2026-10-18. Adds to ADR 103 decisions 8 and 9. Route paths superseded in part by ADR 210

## Context

[ADR 17](0017-api-authorization-and-policy-for-every-feature.md) says that
every feature has a REST API, that every endpoint declares its actions and
the object of each action, and that every change passes a policy check.
[ADR 103](0103-job-gates-actions-and-audit.md) adds the actions
`namespace:manage-runners` (object: a namespace) and `runner:claim`
(object: a runner pool), and an audit entry for the claim of a phase that
changes resources.

ADRs 130 to 137 add pools, join tokens, runners, keys, handoff tokens, job
sessions and source policies. This ADR gives their actions, their audit
entries and their routes. The project owner approved every decision of this
ADR.

## Decision

1. **Two new actions.** They join the actions of ADR 103
   (`runner.ActionNamespaceReadRunners`, `runner.ActionRunnerJoin`):

   | Action | Object | Allows |
   | --- | --- | --- |
   | `namespace:read-runners` | namespace | Read the pools, the runners, their status and release, and the join tokens with no secret. |
   | `runner:join` | runner pool | Register a runner key in the pool. Only a join token holds it. |

   `namespace:manage-runners` of ADR 103 covers the create, the change and
   the delete of a pool, the create and the revoke of a join token, the
   drain and the retire of a runner, and the change of the source policy
   of the namespace. `runner:claim` of ADR 103 covers every request of a
   runner key and of a job session.
2. **Built-in roles.** Each built-in role that has `namespace:read` gets
   `namespace:read-runners`. Only the roles that administer a namespace get
   `namespace:manage-runners`, because the pools decide where the steps of
   the namespace run, with which identity.
3. **The object kind of a pool.** `runner:join` and `runner:claim` need the
   object kind of a runner pool in the `identity` package, as ADR 103
   decision 8 already asks. This design requests it (see the pull
   request).
4. **Audit entries.** Each of these writes one audit entry in the
   transaction of the change (`runner.AuditRules`):

   | Event | Subject | Action | Kind |
   | --- | --- | --- | --- |
   | `pool-created`, `pool-changed`, `pool-deleted` | the principal | `namespace:manage-runners` | `change` |
   | `join-token-created`, `join-token-revoked` | the principal | `namespace:manage-runners` | `change` |
   | `runner-joined` | the join token ID | `runner:join` | `change` |
   | `runner-rejoined` | the join token ID | `runner:join` | `change` |
   | `runner-key-rotated` | the runner | `runner:claim` | `change` |
   | `runner-drained`, `runner-retired` | the principal | `namespace:manage-runners` | `change` |
   | `credentials-issued` | the runner and the session | `runner:claim` | `reveal` |
   | `handoff-refused` | the runner or the session | `runner:claim` | `change`, outcome `denied` |

   The object is the pool, or the unit of the job for
   `credentials-issued`. The store of runners decides whether a join is a
   join again, in the transaction of the join, and the server picks
   `runner-joined` or `runner-rejoined` from that result
   (`runner.JoinEvent`). The entry `runner-rejoined` names the runner, the
   ID of the old key and of the new key, and the claims that the join
   again ended: the claims with no job session. An entry holds IDs, names, releases, key IDs,
   isolation kinds and the changed fields. It never holds a token, a
   private key, a credential value or the content of a credential file.
   The change of a pool records the old and the new isolation list and
   git credential mode, because they decide where and how steps run.
5. **No entry for each request of the protocol.** A poll, a heartbeat, a
   renewal, a step record and a log chunk write no audit entry. The job
   record holds what a person reads about a phase. The claim of a phase
   that changes resources writes its entry, as ADR 103 decision 9 says.
6. **Routes.** The tool-neutral OpenDustpan API gets these routes. Each
   route lands with its e2e step:

   | Method and path | Action |
   | --- | --- |
   | `GET /api/runners/{namespace}/pools` | `namespace:read-runners` |
   | `POST /api/runners/{namespace}/pools` | `namespace:manage-runners` |
   | `GET /api/runners/{namespace}/pools/{pool}` | `namespace:read-runners` |
   | `PATCH /api/runners/{namespace}/pools/{pool}` | `namespace:manage-runners` |
   | `DELETE /api/runners/{namespace}/pools/{pool}` | `namespace:manage-runners` |
   | `GET /api/runners/{namespace}/pools/{pool}/join-tokens` | `namespace:read-runners` |
   | `POST /api/runners/{namespace}/pools/{pool}/join-tokens` | `namespace:manage-runners` |
   | `POST /api/runners/{namespace}/pools/{pool}/join-tokens/{id}/revoke` | `namespace:manage-runners` |
   | `GET /api/runners/{namespace}/pools/{pool}/runners` | `namespace:read-runners` |
   | `POST /api/runners/{namespace}/runners/{runner}/drain` | `namespace:manage-runners` |
   | `POST /api/runners/{namespace}/runners/{runner}/retire` | `namespace:manage-runners` |
   | `GET /api/runners/{namespace}/source-policy` | `namespace:read-runners` |
   | `PUT /api/runners/{namespace}/source-policy` | `namespace:manage-runners` |

   The create of a join token answers its secret once, with the command to
   join (ADR 133 decision 7). The answer of a runner with the status
   `must-rejoin` holds the command to join again, with the URL, the pool
   and the name filled in. The paths under `/runner/v1` are the runner
   protocol (ADR 131). They are not part of this API, and the route test
   lists them as protocol routes with `runner:join` or `runner:claim`.

## Consequences

- An auditor sees who created each pool and token, which runner joined
  with which token, and when each step got credentials.
- The console shows pools and runners through this API only, as ADR 17
  needs.
- The integrator adds two actions and one object kind to the `identity`
  package, and the routes to the route test.

## Open questions

- Does a read of the runners of a pool show the labels and the release of
  each runner to every principal with `namespace:read-runners`, or only to
  administrators? Labels may hold names of hosts.
- Does the creator of a join token get a notification when a runner uses
  it?
