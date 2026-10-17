# 103. Job gates, actions and audit entries

Status: accepted · 2026-10-17

## Context

ADR 100 defines jobs and ADR 101 their executors.
[ADR 17](0017-api-authorization-and-policy-for-every-feature.md) says that
every endpoint declares its actions and the object of each action, and that
every change passes a policy check.
[RFC 13](../rfc/0013-policy-and-approvals.md) proposes edit policy and
approvals through the API.

The project owner decided:

- OpenDustpan approvals add to the approvals of the forge. They never gate
  a team that runs only in its own CI.
- Change freezes are opt-in for each namespace. The server records each
  break-glass.
- Plans have a `read plans` action (ADR 102).

This ADR records the design for review. The owner decisions above are the
owner's. The other decisions take effect when the project lead accepts the
pull request that adds this ADR.

## Decision

1. **A policy decision at start.** `job.Service.Start` asks the
   `job.Decider` port for a decision. The input is the principal, the spec
   and the key paths of the edits. It holds no secret value. The decision
   allows or denies the start, and gives a gate for each kind of phase. With
   no policy installed, the adapter allows every start and adds no gate. A
   denied start fails with `ErrPolicyDenied` and creates no job. A policy
   that fails is not a denial: the start fails and nothing changes.
2. **A gate in front of a phase.** A gate names the number of approvals, the
   groups of the approvers, whether the starter can approve, a timeout and
   whether the approvals bind to the plan. Before the dispatch of a phase
   with a gate, the job waits in `awaiting-approval`. Each subject counts
   once. One rejection rejects the job. The starter does not count unless
   the gate allows it. `Gate.Status` holds these rules, and a table-driven
   test checks them. A job that waits longer than the timeout expires. The
   default timeout is 7 days.
3. **OpenDustpan approvals add to the forge.** For a write-back job, a gate
   on the deliver phase waits before the commit. After the commit, the
   review request still needs the approvals that the forge asks for. This
   replaces the rule of RFC 13 that one edit has one place for approval. A
   change that a team makes in git and applies in its own CI is not a job,
   so no gate applies to it.
4. **Change freezes are opt-in.** A namespace has no freeze until a
   principal with `namespace:manage-freezes` creates one. A freeze has a
   reason, a start and an optional end. While a freeze is active, no phase
   that changes resources, state or the code in git dispatches. The store
   checks this in the transaction of the dispatch. Plans still run. A phase
   that runs when the freeze starts runs to its end. A queued job waits with
   the reason `frozen`, and dispatches after the freeze ends.
5. **A freeze never refuses a state write.** A tool writes the state of real
   resources. A refusal would lose the record of them. A freeze stops only
   the work that OpenDustpan starts.
6. **The server records each break-glass.** A start during an active freeze fails with
   `ErrFrozen`, unless the request has a break-glass with a reason. A
   break-glass needs `namespace:break-freeze` on the namespace. The job
   keeps the break-glass and the freeze ID. The audit entry of the start has
   the kind `override`. `namespace:break-freeze` is in another family than
   `namespace:manage-freezes`. So a principal that can end a freeze does not
   also get break-glass, and the reverse.
7. **Actions for each operation.** Each route of the job API declares these
   actions. The object of a job action is the unit of the job.

   | Action | Object | Allows |
   | --- | --- | --- |
   | `unit:run-plan` | unit | Start a runner job that only plans. |
   | `unit:run-apply` | unit | Start a runner job that applies or refreshes. |
   | `unit:run-destroy` | unit | Start a runner job that destroys. |
   | `unit:write-back` | unit | Start a write-back job. |
   | `job:read` | unit, or namespace for a list | Read jobs, phases, steps, approvals and the metadata of plans and logs. |
   | `job:read-logs` | unit | Read the content of logs. |
   | `job:read-plans` | unit | Read the readable plan, masked. |
   | `job:cancel` | unit | Cancel a job. |
   | `job:approve` | unit | Approve or reject a job that waits. |
   | `job:redact-logs` | unit | Redact a log and shred its older version. |
   | `namespace:manage-freezes` | namespace | Create and end freezes. |
   | `namespace:break-freeze` | namespace | Start a job through a freeze. |
   | `namespace:manage-runners` | namespace | Manage runner pools and runners. |
   | `runner:claim` | runner pool | Claim and report on the phases of the pool. |

   An unmasked plan also needs `unit:read-secrets`. `job.StartRequirements`
   maps a spec to its start actions, and a table-driven test checks the
   map. `runner:claim` needs a new object kind, the runner pool, in the
   `identity` package.
8. **Audit entries.** Each of these writes one audit entry in the
   transaction of the change:

   | Event | Subject | Action | Kind |
   | --- | --- | --- | --- |
   | Start | the principal | the start action | `change`, or `override` with break-glass |
   | Approval or rejection | the approver | `job:approve` | `change` |
   | Cancel | the principal | `job:cancel` | `change` |
   | Claim of a phase that changes resources | the runner or the instance | `runner:claim` | `change` |
   | Delivery of a write-back | the instance | `unit:write-back` | `change` |
   | Redaction, and each shred | the principal, or the instance for retention | `job:redact-logs` | `change` |
   | Unmasked plan read | the principal | `job:read-plans` | `reveal` |
   | Freeze created or ended | the principal | `namespace:manage-freezes` | `change` |

   The object is the unit reference, with the unit ID. The entries hold the
   job ID, the reason and, for a delivery, the commit and the review
   request. They never hold a secret value or redacted text. The end of a
   job by an executor writes no entry. The job record holds it.
9. **What a caller sees.** `job.Service.Get` returns `ErrNotFound` also when
   the caller may not read the job, so that a caller cannot learn that a job
   exists.

## Consequences

- Fourteen actions join the action list of the `identity` package, and the
  built-in roles need them. The integrator adds them to the list and to the
  route test.
- A gate makes a runner job wait without a runner. A runner is busy only
  while a phase runs.
- A team can freeze OpenDustpan for an incident with one request, and
  undo it with one request. Its own CI does not freeze.
- The default with no policy allows every job with no approval. RFC 13
  proposes "approve first" for apply jobs as the default. That choice waits
  for RFC 13.

## Open questions

- Which defaults does the decider give with no policy installed? This ADR
  gives none. RFC 13 proposes one approval by another principal for apply
  jobs.
- Does a freeze cover a whole namespace only, or also a collection or a
  unit?
- Can a queued job get break-glass after its start, or does a person cancel
  it and start a new one with break-glass?
- Does the audit log need a channel for agents such as Dusty, or is `api`
  enough?
- Does a freeze also hold the approvals that end during it, or only the
  dispatch?
