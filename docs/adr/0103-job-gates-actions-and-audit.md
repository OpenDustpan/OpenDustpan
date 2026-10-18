# 103. Job gates, actions and audit entries

Status: accepted · 2026-10-17 · decision 10 superseded in part by ADR 126 · decision 9 superseded in part by ADR 141. Decisions 1 and 2 superseded in part by ADR 190

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

- Dusty runs inside the server and acts as the user who asked it. The audit
  log records both the user and the agent.
- An agent job in approve-first mode needs a confirmation by the person who
  asked, with a fresh authentication. An agent in review-request mode
  changes code only through a review request. A direct change by an agent
  needs three switches: the server, the namespace and the user.

The owner approved the decisions of this ADR on 17 October 2026. On the
same day, the project manager decided for the owner how the person
authenticates a confirmation (decision 2), and kept the rule for runner
phases in review-request mode (decision 12).

## Decision

1. **A policy decision at start.** `job.Service.Start` asks the
   `job.Decider` port for a decision. The input is the principal, the agent
   that acts for it, if any, with its mode, the spec and the key paths of the edits. It
   holds no secret value. So a policy can deny or gate a start by agent. The decision
   allows or denies the start, and gives a gate for each kind of phase. With
   no policy installed, the adapter allows every start and adds no gate. A
   denied start fails with `ErrPolicyDenied` and creates no job. A policy
   that fails is not a denial: the start fails and nothing changes.
2. **Gates in front of a phase, of two types.** A phase can have more than
   one gate, and it dispatches only when every gate is open
   (`job.GatesStatus`). Before that, the job waits in `awaiting-approval`.
   One rejection of any gate rejects the job. A job that waits longer than
   the timeout of a gate expires. The default timeout is 7 days.
   `Gate.Status` holds the rules of each type, and table-driven tests check
   them.
   - **Approval** (`job.GateApproval`) comes from the policy decision. It
     names the number of approvals, the groups of the approvers, whether
     the starter can approve, a timeout and whether the approvals bind to
     the plan (ADR 102 decision 7). Each subject counts once. The starter
     does not count unless the gate allows it. A user who started the job
     through an agent is the starter too.
   - **Confirmation** (`job.GateConfirmation`) is in front of each phase
     that changes, for a job that an agent started in approve-first mode
     (decision 12). Only the person who asked the agent can pass it: one
     confirmation by the starter, in a request of their own with no agent,
     with a fresh authentication (`job.ConfirmAuth`):
     - a new MFA step, where built-in MFA covers the person
       (`job.ConfirmAuthMFA`, ADR 56).
     - otherwise a new authentication at the SSO identity provider of the
       person, asked with a maximum age (`job.ConfirmAuthSSO`, ADR 71).
       The time is the authentication time that the provider gives.

     The authentication is at most 15 minutes old (`job.ConfirmFreshness`),
     the same number as ADR 56 decision 2 and ADR 71 decision 1. The
     approval records its kind and its time (`Approval.Auth`,
     `Approval.AuthAt`). A confirmation with no fresh authentication fails
     with `ErrAuthStale`. When the server can ask the person for neither,
     for example in an installation with static tokens only, approve-first
     mode is not available for the agent jobs of that person
     (decision 12). The confirmation binds to the plan when a plan phase
     comes before. It applies also with no policy installed. A policy
     never gives it and never removes it, and the rule of the starter of an
     approval gate does not apply to it. The service records it with
     `job.Service.Confirm`, which needs the start action of the job, not
     `job:approve`.
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
7. **Agents never approve, give a confirmation or break a freeze.** An
   agent such as Dusty acts as the user who asked it (ADR 100 decision 17).
   The job, each approval and the decision input record the agent and its
   mode. An agent
   cannot record an approval or a confirmation, and cannot ask for
   break-glass: each fails with `ErrAgentDenied`. An agent can record a
   rejection, because a rejection only stops a change.
8. **Actions for each operation.** Each route of the job API declares these
   actions. The object of a job action is the unit of the job.

   | Action | Object | Allows |
   | --- | --- | --- |
   | `unit:run-plan` | unit | Start a runner job that only plans. |
   | `unit:run-apply` | unit | Start a runner job that applies or refreshes. |
   | `unit:run-destroy` | unit | Start a runner job that destroys. |
   | `unit:write-back` | unit | Start a write-back job. |
   | `unit:define-steps` | unit, or blueprint | Change the custom steps of a unit or a blueprint. |
   | `job:read` | unit, or namespace for a list | Read jobs, phases, steps, approvals and the metadata of plans and logs. |
   | `job:read-logs` | unit | Read the content of logs. |
   | `job:read-plans` | unit | Read the readable plan, masked. |
   | `job:cancel` | unit | Cancel a job. |
   | `job:approve` | unit | Approve or reject a job that waits for an approval gate. |
   | `namespace:manage-freezes` | namespace | Create and end freezes. |
   | `namespace:break-freeze` | namespace | Start a job through a freeze. |
   | `namespace:manage-runners` | namespace | Manage runner pools and runners. |
   | `runner:claim` | runner pool | Claim and report on the phases of the pool. |

   An unmasked plan also needs `unit:read-secrets`. A redaction of a plan or
   of a log, also a redaction of a log by substitution (ADR 102 decision
   11), needs `unit:redact` of the history model (ADR 151). The job model
   has no redact action of its own. A confirmation needs the start action
   of the job (decision 2). Each built-in role that has `unit:read` also
   gets `job:read-plans`. `job.StartRequirements` maps a spec to its start
   actions, and a table-driven test checks the map. `runner:claim` needs a
   new object kind, the runner pool, in the `identity` package.
9. **Audit entries.** Each of these writes one audit entry in the
   transaction of the change:

   | Event | Subject | Action | Kind |
   | --- | --- | --- | --- |
   | Start | the principal, and the agent | the start action | `change`, or `override` with break-glass |
   | Approval or rejection | the approver, and the agent of a rejection | `job:approve` | `change` |
   | Confirmation or its rejection | the person who asked, and the agent of a rejection | the start action | `change` |
   | Cancel | the principal, and the agent | `job:cancel` | `change` |
   | Change of custom steps | the principal, and the agent | `unit:define-steps` | `change` |
   | Claim of a phase that changes resources | the runner or the instance | `runner:claim` | `change` |
   | Delivery of a write-back | the instance | `unit:write-back` | `change` |
   | Redaction of a plan or a log | as ADR 151 decision 17 says | `unit:redact` | `change` |
   | Unmasked plan read | the principal | `job:read-plans` | `reveal` |
   | Freeze created or ended | the principal | `namespace:manage-freezes` | `change` |

   The object is the unit reference, with the unit ID. The entries hold the
   job ID, the reason and, for a delivery, the commit and the review
   request. They never hold a secret value or redacted text. The end of a
   job by an executor writes no entry. The job record holds it.
10. **The audit entry records the agent.** `audit.Entry` gets a field
    `Agent`: the agent that acted for the subject, or empty. The entry hash
    covers it. The hash encoding adds the field after the fence version
    only when it is not empty, so the hash of every entry with no agent
    stays the same, and existing chains stay valid. This supersedes the
    list of entry fields in decision 13 of
    [ADR 41](0041-the-server-reads-state-and-masks-secrets.md), and the
    field list of the hash encoding of the `audit` package. An agent needs
    no audit channel of its own: the channel stays where the request came
    from.
11. **What a caller sees.** Each method of `job.Service` that takes a job
    ID returns `ErrNotFound` when the caller lacks `job:read` on the unit of
    the job, so that a caller cannot learn that a job exists. With
    `job:read`, a missing action of the method returns `identity.ErrDenied`.
12. **Agent modes.** The user sets the mode of the agent that acts for
    them. `job.CheckAgent` holds the rule at start, and a table-driven test
    checks it. A runner job that only plans changes nothing and needs no
    mode.
    - **Approve-first** is the default. Each phase that changes gets a
      confirmation gate (decision 2), with or without a policy. The policy
      gates still apply, with their own rules. The server sets the fresh
      authentication that it can ask of the person (`Actor.ConfirmAuth`)
      from the sign-in of the person. When it can ask for neither an MFA
      step nor an SSO authentication, the start fails with
      `ErrAgentDenied`, and the agent of that person must use
      review-request mode.
    - **Review-request.** The agent changes a unit only through a
      write-back job that delivers as a review request. The review of the
      forge is the check of a person, so the job gets no confirmation gate.
      A direct push from an agent in this mode fails with `ErrAgentDenied`.
      A runner job whose phases change resources or state (`apply`,
      `destroy`, `refresh`) fails in the same way. A runner job that only
      plans starts in this mode, as in every mode.
    - **Direct.** The agent changes a unit with no confirmation. This needs
      three switches, each off by default: an installation setting of the
      server, a setting of the namespace of the unit, and a setting of the
      user who asks (`job.DirectSwitches`). When one is off, the start fails
      with `ErrAgentDenied` and names the switches.

## Consequences

- Fourteen actions join the action list of the `identity` package, and the
  built-in roles need them. Each built-in role with `unit:read` gets
  `job:read-plans`. The integrator adds them to the list and to the route
  test.
- A gate makes a runner job wait without a runner. A runner is busy only
  while a phase runs.
- A team can freeze OpenDustpan for an incident with one request, and
  undo it with one request. Its own CI does not freeze.
- The default with no policy allows every job of a person with no
  approval. RFC 13 proposes "approve first" for apply jobs as the default.
  That choice waits for RFC 13. A job of an agent gets the confirmation of
  its mode with or without a policy.
- A confirmation needs a fresh MFA step or a fresh SSO authentication.
  Built-in MFA (ADR 46) does not exist yet, so a person who signs in through
  an SSO provider gives a confirmation with a new SSO authentication. In an
  installation with static tokens only, approve-first mode is not
  available: an agent there works in review-request mode, and a person
  starts a runner change without the agent.

## Open questions

- Which defaults does the decider give with no policy installed? This ADR
  gives none. RFC 13 proposes one approval by another principal for apply
  jobs.
- Does a freeze cover a whole namespace only, or also a collection or a
  unit?
- Can a queued job get break-glass after its start, or does a person cancel
  it and start a new one with break-glass?
- Does a freeze also hold the approvals that end during it, or only the
  dispatch?
- ADR 71 lets an operator set the freshness window of SSO sessions to 0,
  which turns off its check for tokens. This ADR keeps 15 minutes
  (`job.ConfirmFreshness`) as the maximum age of a confirmation in every
  case. Does a window of 0 also apply to a confirmation?
