# 197. Change order actions, routes and audit entries

Status: accepted · 2026-10-18. Part of ADR 190. Route paths superseded in part by ADR 210. Decisions 5 and 6 superseded in part by ADRs 229 and 230

Changed by:

- ADR 210 changes: the route paths and the server: action names.
- ADR 222 changes decisions 5 and 6: errors, status codes and audit entries of
  gates.
- ADR 229 changes decision 6: the policy decisions stored audit entry.
- ADR 230 changes decisions 5 and 6: the errors and audit entries of gates.

## Context

[ADR 17](0017-api-authorization-and-policy-for-every-feature.md) says that
every feature has a REST API, that every endpoint declares its actions and
the object of each action, and that every change passes a policy check.
ADR 103 decision 8 gives the actions of jobs, and ADR 128 decision 15 the
audit entries of threads on a job. A change order covers many units of one
namespace (ADR 190).

## Decision

1. **The object of a change order action is each unit.** A change order
   has no object kind of its own. Each action applies to each unit of the
   current revision, and the caller needs it on each one. A list uses the
   namespace as its object, and then filters (decision 4). A unit of an
   attach that is not yet in the change order counts too: an attach needs
   the actions on each unit of the new revision.
2. **Actions.**

   | Action | Object | Allows |
   | --- | --- | --- |
   | `changeorder:read` | each unit, or namespace for a list | Read a change order, its revisions, its reviews, its threads, the metadata of its review view and its cause diff. |
   | `changeorder:comment` | each unit | Open a comment thread, and reply to a thread. |
   | `changeorder:author` | each unit | Create a change order, attach a revision, and close it as an author. The plan jobs also need `unit:run-plan`. |
   | `changeorder:approve` | each unit | Approve, request changes, reject and close, open a blocking thread, resolve, dismiss and reopen, for an eligible approver (ADR 192 decision 5). |
   | `changeorder:apply` | each unit | Ask for the apply of an approved change order. The apply jobs also need `unit:run-apply` or `unit:run-destroy` on each unit (ADR 103 decision 8). |

   The masked values of the review view need `job:read-plans` on their
   unit (ADR 194 decision 9). A break-glass of an apply needs
   `namespace:break-freeze` on the namespace (ADR 103 decision 6). A
   redaction of a comment needs `unit:redact` on each unit (ADR 198
   decision 3). `changeorder.Requirements` maps each route to its actions,
   and a table-driven test checks the map.
3. **Built-in roles.** Each built-in role with `unit:read` gets
   `changeorder:read` and `changeorder:comment`. Each built-in role with
   `job:approve` gets `changeorder:approve`. Each built-in role with
   `unit:run-plan` gets `changeorder:author`, and each built-in role with
   `unit:run-apply` gets `changeorder:apply`. A grant gives them to a
   custom role (ADR 52).
4. **A list shows what the caller can read.** A list returns only the
   change orders whose units the caller can read with `changeorder:read`,
   each one. It says when it left out change orders (ADR 17).
5. **Routes.** The routes are tool-neutral, under `/api/change-orders`.
   The paths are proposals for the route table, and the route test gets
   each one with its actions. `{ns}` is the namespace and `{id}` the
   change order ID.

   | Method and path | Actions | Answer |
   | --- | --- | --- |
   | `POST /api/change-orders/{ns}` | `changeorder:author` | `201` with the change order and revision 1. The header `Idempotency-Key` makes a retry safe. |
   | `GET /api/change-orders/{ns}` | `changeorder:read` | A page, with the filters `state`, `unit`, `author` and `reviewer`. |
   | `GET /api/change-orders/{ns}/{id}` | `changeorder:read` | The change order, its current revision, the requirements and the count that each still needs. |
   | `POST /api/change-orders/{ns}/{id}/revisions` | `changeorder:author` | `201` with the new revision. `409` for `ErrApplying` or `ErrConflict`. |
   | `GET /api/change-orders/{ns}/{id}/revisions/{rev}` | `changeorder:read` | The revision, its unit plans, its digest, its signatures. |
   | `GET /api/change-orders/{ns}/{id}/revisions/{rev}/view` | `changeorder:read`, and `job:read-plans` for the values of each unit | The review view of ADR 194, in its order. |
   | `GET /api/change-orders/{ns}/{id}/revisions/{rev}/cause-diff` | `changeorder:read` | The cause diff, with `truncated` or `no-base` when they apply. |
   | `GET`, `PUT`, `DELETE /api/change-orders/{ns}/{id}/draft` | `changeorder:comment` | The draft of the caller only. |
   | `POST /api/change-orders/{ns}/{id}/reviews` | `changeorder:comment` for `comment`, `changeorder:approve` for the other verdicts and for a `comment` review with a blocking thread | `201` with the review. The body names the verdict, the digest and the summary. |
   | `GET /api/change-orders/{ns}/{id}/threads` | `changeorder:read` | The threads, with the filters `open`, `blocking` and `outdated`. |
   | `POST /api/change-orders/{ns}/{id}/threads/{thread}/replies` | `changeorder:comment` | `201` with the comment. |
   | `POST /api/change-orders/{ns}/{id}/threads/{thread}/resolve` | `changeorder:approve`, or `changeorder:comment` for the principal that opened a comment thread | The thread. A dismissal needs `reason`. |
   | `POST /api/change-orders/{ns}/{id}/threads/{thread}/reopen` | `changeorder:approve`, or `changeorder:comment` for the principal that opened a comment thread | The thread. The body needs `reason`. |
   | `POST /api/change-orders/{ns}/{id}/apply` | `changeorder:apply`, and `namespace:break-freeze` on the namespace for `breakGlass` | `202` with the change order in `applying`. The body names the digest, and an optional `breakGlass` with a reason. |
   | `POST /api/change-orders/{ns}/{id}/close` | `changeorder:author` for a withdrawal | The change order. The body needs `reason`. |
   | `POST /api/change-orders/{ns}/{id}/threads/{thread}/comments/{comment}/redact` | `unit:redact` on each unit | The comment, with its text redacted. The body needs `reason`. |

   A reject and close is the verdict `reject-and-close` of the reviews
   route. It has no route of its own, so one rule checks each verdict. The
   status codes map the errors of ADR 196 decision 1: `400` for
   `ErrInvalid` and `ErrReasonRequired`, `404` for `ErrNotFound`, `409` for
   `ErrConflict`, `ErrTransition`, `ErrFinished`, `ErrApplying`,
   `ErrPlanChanged`, `ErrPlanStale`, `ErrNotReady`, `ErrNotApproved` and
   `ErrChangesRequested`, `403` for `ErrSelfApproval`, `ErrNotAuthor`,
   `ErrNotEligible`,
   `ErrAuthorThread`, `ErrSelfResolve`, `ErrAgentDenied` and
   `ErrPolicyDenied`, `409` also for `ErrPolicyUnreadable` and
   `ErrSummaryUnreadable`, `422` for `ErrLimit`, and `503` for
   `ErrPolicyFailed` and `ErrForgeUnavailable`. The errors use the error
   shape of the tool-neutral APIs, and each one names the object and the
   one action that helps.
6. **Audit entries.** Each of these writes one audit entry in the
   transaction of the change, with the change order ID and the revision
   number. The object is the namespace, and the entry lists the unit
   references with their unit IDs. An entry of an agent holds the agent
   block of ADR 126 decision 10, as ADR 128 decision 17 says.

   | Event | Subject | Action | Kind | Also holds |
   | --- | --- | --- | --- | --- |
   | Create | the principal | `changeorder:author` | `change` | the title, the units and their operations |
   | Attach | the principal | `changeorder:author` | `change` | the revision, the count of signatures that it voided |
   | Policy decisions stored | the instance | `changeorder:author` | `change` | the combined effect and the policy versions |
   | Review submitted | the reviewer, and the agent | `changeorder:comment` or `changeorder:approve` | `change` | the verdict, the digest and the IDs of the threads that it opened. For the summary, its comment ID and the SHA-256 of its text, never the text (ADR 192 decision 1). |
   | Reply | the author of the comment, and the agent | `changeorder:comment` | `change` | the thread ID |
   | Resolve by the principal that opened the thread | that principal | `changeorder:approve` or `changeorder:comment` | `change` | the thread ID |
   | Dismissal | the person who dismisses | `changeorder:approve` | `change` | the thread ID, and the reason in the field `reason` |
   | Reopen | the principal | `changeorder:approve` or `changeorder:comment` | `change` | the thread ID and the reason |
   | Signature voided by a loss of access | the instance | `changeorder:approve` | `change` | the subject of the signature and the cause |
   | Apply asked | the principal | `changeorder:apply` | `change`, or `override` with break-glass | the digest, and the freeze for a break-glass |
   | Close by an author | the author | `changeorder:author` | `change` | the reason |
   | Expiry | the instance | `changeorder:author` | `change` | the idle time |
   | Redaction of a comment | as ADR 151 decision 26 says | `unit:redact` | `change` | the comment ID and the reason |

   An entry never holds the text of a comment, the summary or the reason of
   a review, a value of a plan or the cause diff. So a redaction of a
   comment (ADR 198 decision 3) reaches each copy that the server controls,
   and the audit chain keeps a hash of the text. The change order record
   holds the text. The jobs of the change order write their own entries
   (ADR 103 decision 9). A read and a draft write write no entry.
   **One-way door:** the audit chain hashes these shapes.

   The limit: the reason of a dismissal, of a reopen and of a close by an
   author is in the field `reason` of its audit entry. These reasons are
   not comments, and a redaction cannot reach them. The console warns at
   the input of each of these reasons that the reason stays in the audit
   log, and asks the person not to paste a secret. The reason of a
   `reject-and-close` is different: it is the summary of its review, a
   comment that a redaction can reach (ADR 192 decision 1).
7. **Notifications.** The catalog of types of ADR 145 decision 5 gets:
   `change-order.review-requested` (a revision is `ready`, to the eligible
   approvers), `change-order.changes-requested` (to the authors),
   `change-order.approved` (to the authors), `change-order.closed`,
   `change-order.applied` and `change-order.partially-applied`. The store
   appends each one in the transaction of its change (ADR 145 decision 7).
   A notification holds names, IDs and links, never a comment text or a
   value of a plan (ADR 145 decision 4).
8. **Limits of requests.** A create or an attach names at most 100 units.
   A review submits at most 100 threads and replies. A principal can
   submit at most 60 reviews in a minute, then the answer is `429`. The
   constants of `pkg/changeorder` hold the numbers.

## Consequences

- Five actions join the action list of the `identity` package. The
  integrator adds them to the list, to the built-in roles and to the route
  test.
- A principal needs an action on each unit of a change order. A change
  order across teams needs a role on each unit for each reviewer.
- The change records export of ADR 147 reads the audit entries of a change
  order with its jobs (ADR 198 decision 6).

## Open questions

- Does a change order need its own object kind, so that a grant can give
  a person the review of one change order only?
- Does the author of a comment redact it with no `unit:redact`, for
  example in the first hour after the write?
