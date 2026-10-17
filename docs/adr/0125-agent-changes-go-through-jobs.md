# 125. Agent changes go through the job model as review requests or approve-first

Status: accepted · 2026-10-17. The decisions of the project owner and the
project manager below are accepted. The other decisions take effect when the
project lead accepts the pull request of this ADR.

## Context

The project owner decided on 2026-10-17:

- A change by Dusty goes in as a review request (write-back) or approve-first
  by default.
- A direct change needs a server flag, a namespace opt-in and a user opt-in.
- Changes run as jobs on the shared job model (ADRs 100 to 103).
- Change freezes and break-glass apply to Dusty as to a person.

The project manager decided on 2026-10-17, for the owner, how an agent
change fits the job model:

- A job request carries the requesting person as the principal, and the
  acting agent: the channel, and the run or the MCP session ID.
- The methods of `job.Service` take that actor.
- The job model records the agent on the job, on each approval, on the
  input of the policy decision and in each audit entry.
- An agent never approves a job and never uses break-glass.
- In approve-first mode, a job of an agent gets a "confirmation" gate that
  only the requesting person can pass: a request of that person, with a
  fresh MFA step. The gate holds also when no policy is installed.
- Review-request mode forces write-back as a review request, never a direct
  push.
- A takeover is the same durable operation with a new claim token.
- The job states are the 11 states of ADR 100.

[RFC 12](../rfc/0012-api-edits-and-write-back.md) describes write-back as a
review request. [RFC 13](../rfc/0013-policy-and-approvals.md) describes
approvals.

**This ADR supersedes the open question of ADR 100 that asks what Dusty is
and assumes a REST client with its own principal.** Dusty has no principal
(ADR 120 decision 15). A job of an agent belongs to the person who asked,
and names the agent next to that person.

The job model is in review in another pull request at the same time. Where
its names still move, this ADR describes the shape that the agent needs, and
that pull request decides the final names.

## Decision

### Modes and the plan of a change

1. **Three modes.**
   - `review-request`: the job model writes the change back to the code
     location of the unit as a review request, never as a direct push. A
     person merges it, and the team's own path applies it.
   - `approve-first`: the phases of the job that change wait at the
     confirmation gate (decision 5).
   - `direct`: a runner job applies with no confirmation, or a write-back
     job pushes directly after the confirmation of its delivery. Policies
     and change freezes still apply.
2. **The plan of a change.** `agent.ChangePolicy.Plan` turns a request into
   a `ChangePlan`: the mode, the executor, the delivery and the phases that
   wait for confirmation. A table-driven test covers it.

   | Request | Executor | Delivery | Confirmation gate |
   | --- | --- | --- | --- |
   | apply, unit with a code location, no mode | write-back | review request | deliver |
   | apply or destroy or refresh, no code location, no mode | runner | | apply |
   | plan, no mode | runner | | none: a plan changes nothing |
   | `review-request` | write-back only | review request | deliver |
   | `approve-first` on write-back | write-back | review request | deliver |
   | `approve-first` on a runner | runner | | apply |
   | `direct` on write-back | write-back | direct push | deliver |
   | `direct` on a runner | runner | | none |

   The deliver phase of a write-back job of an agent always waits for the
   confirmation, in every mode: nothing that an agent wrote is published to
   the forge before the person reads it (ADR 124 decision 13). `direct` is
   never a default. A destroy is never `direct`.
3. **Direct changes need three switches.**
   - the installation setting `directChanges`, off by default, with
     `server:manage-agent`,
   - the opt-in of the namespace of the unit, with
     `namespace:manage-agent`, typed "I accept" and a fresh MFA step,
   - the opt-in of the person, with `caller:manage-agent-opt-ins`, typed
     "I accept" and a fresh MFA step.

   Without one of them, a request for `direct` fails with
   `ErrDirectNotAllowed`. The loop does not fall back to another mode: it
   tells the model, and the model can ask for `approve-first`. Each switch
   change writes an audit entry. The status route answers whether direct
   changes are possible for the person.
4. **Unsupported plans fail.** A review request on a unit with no code
   location, a write-back of an operation other than apply, and a runner job
   on a unit with no runner pool fail with `ErrModeNotSupported`.

### The confirmation gate

5. **Only the person who asked passes it.** The job model puts a gate of
   the kind "confirmation" in front of each phase that `ChangePlan.Confirm`
   lists. It holds also when no policy is installed. Only the person who
   started the job passes it: a request of a console session of that
   person, with a fresh MFA step (ADR 56, ADR 71). An access token cannot
   pass it, so an agent that holds the token of its person cannot pass
   the gate of its own change. The console page shows the plan, the diff of the edits
   or the stored plan of the job, the reason of the agent and the agent
   (run or MCP session).
6. **The other gates stay.** A gate from a policy (ADR 103) adds to the
   confirmation gate. Its approvers need `job:approve` as for every job. An
   agent never approves: the port has no approve method, and the job model
   refuses an approval whose request carries an agent.

### The port to the job model

7. **`agent.Changes` maps to `job.Service`.** The job model implements the
   port on its service. An agent calls it for each change. It never writes
   a unit, a code location or a setting in another way.

   | `agent.Changes` | `job.Service` | Notes |
   | --- | --- | --- |
   | `Support(ctx, actor, unit)` | reads the unit settings | whether the unit has a code location and a runner pool. It needs `job:read`. |
   | `StartActions(req, plan)` | `job.StartRequirements(spec, nil)` | `unit:write-back`, `unit:run-plan`, `unit:run-apply` or `unit:run-destroy`, never `namespace:break-freeze` |
   | `Start(ctx, req)` | `Start(ctx, actor, StartRequest)` | the person as the principal, the agent on the job |
   | `Get(ctx, actor, id)` | `Get(ctx, actor, id)` | `job:read`. `ErrChangeNotFound` also for a job that the person cannot read. |
   | `Cancel(ctx, actor, id, reason)` | `Cancel(ctx, actor, id, reason)` | `job:cancel` |
   | none | `Approve` | an agent never approves |

   A `ChangeID` is the job ID. A `ChangeState` is the job state, with the 11
   states of ADR 100: `queued`, `awaiting-approval`, `dispatched`,
   `running`, `canceling`, `awaiting-state`, `succeeded`, `failed`,
   `canceled`, `rejected` and `expired`. `Change.AwaitingConfirmation` is
   true while the current gate is the confirmation gate.
8. **What a request holds.** A `ChangeRequest` holds the key, the actor,
   the unit, the operation (`plan`, `apply`, `destroy` or `refresh`), the
   executor and the mode (empty for the default), the file edits of a
   write-back, and the reason in plain words. It has no field for
   break-glass. The job model sets `StartRequest.BreakGlass` to nil for
   every request of an agent.
9. **The actor.** `agent.Actor` holds the person (`identity.Principal`, as
   `Credentials` resolved it just before the call), the agent
   (`agent.Ref`: the channel, and the run or the MCP session ID) and
   whether the credential is a break-glass session. `Ref.Validate` refuses
   a ref with no ID or with the ID of the other channel.
10. **One change for one key.** The key comes from the run, the turn and
    the index of the call (ADR 120 decision 11), or from the MCP session
    and the JSON-RPC request ID (ADR 123 decision 5). It is the idempotency
    key of the start. A second start with one key and one request returns
    the first change. A second start with one key and another request fails
    with `ErrChangeKeyConflict`.
11. **A takeover changes nothing.** A turn that another instance takes
    over is the same durable operation with a new claim token. It starts
    the change with the same key, so the job model returns the same job.
12. **The same checks as for a person.** The job model applies to a change
    of an agent each check that it applies to the same change by the
    person: the actions of the start, the policy, the change freezes, the
    one-change rule of a unit and the gates. The agent adds checks: the
    confirmation gate, `agent:use`, and the refusal after a secret read
    (ADR 124 decision 12). It never removes one.

### What the job model records

13. **The shape that the agent needs from the job model.** The pull request
    of the job model decides the final names. The agent needs:
    - a `job.Actor` with the principal and an optional agent reference
      (channel, run ID, MCP session ID), taken by `Start`, `Get`, `Cancel`
      and `Approve`,
    - the agent reference on `job.Job`, on each `job.Approval` (the agent
      of the job that the approval decides), and on `job.DecisionInput`, so
      a policy can treat a change of an agent in its own way,
    - a gate kind "confirmation" on `job.Gate` that only `Job.StartedBy`
      passes, with a human request and a fresh MFA step, set from
      `ChangePlan.Confirm`,
    - the fields `agentRun`, `agentSession` and `breakGlass` (the session
      flag) in each audit entry of the job (ADR 126 decision 4),
    - an approval that carries an agent fails, also on a gate from a
      policy.

### Change freezes and break-glass

14. **A change freeze stops an agent.** The job model checks the change
    freezes for each start of an agent, in each mode. A frozen change fails
    with `ErrChangeFrozen`, and the agent tells the person.
15. **An agent never uses break-glass.** An agent never sets
    `StartRequest.BreakGlass`, never asks for `namespace:break-freeze`,
    never starts a break-glass session, never asks for a break-glass
    account and never sets an override switch (ADR 44). A person who must
    change a unit through a freeze starts the job in the console. A run
    that a person starts in a break-glass session (ADR 70) acts with the
    rights of that session. Each audit entry of the run says so (ADR 126),
    and the run ends when the session ends (ADR 120 decision 14). The
    session gives the agent no freeze exception.
16. **No kind that needs an override.** The job model offers to agents no
    change that needs `unit:write-forced` or `unit:write-unlocked` (ADR 44).

### Conformance

17. **A conformance suite.** `agenttest.RunChanges` runs the job model
    against these cases: the support of each unit, the start actions of
    each operation and executor, the default review request at the
    confirmation gate with no policy, approve-first on a runner, a
    confirmation by another person (denied) and by the person who asked, a
    start twice with one key, one key with another request, a review
    request with no code location, direct mode with no switch, a change
    freeze, a person without the action, a request that is not valid (no
    key, unknown operation, no reason, no agent), a get and a cancel. A
    test in the suite package runs the suite against a fake job model, so
    the cases run on each change.

## Consequences

- The job model must implement `agent.Changes`, run its suite, and give
  each job the agent fields of decision 13. If its design needs another
  shape, that design wins and this port follows it.
- A review request needs a code location for the unit (RFC 12). A unit
  with no code location gets a runner job with approve-first by default.
- A run does not wait for a confirmation. The person passes the gate in the
  console, and reads the state with `get_change` in a follow-up run.
- The person who asked is the only one who can pass the confirmation gate. A team that wants a
  second person adds a policy gate (ADR 103).

## Not decided here

- Batches of changes for a blueprint rollout.
- Whether a person can turn the confirmation of the deliver phase off for
  `direct` write-back. Today it cannot.
