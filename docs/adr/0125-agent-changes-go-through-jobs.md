# 125. Agent changes go through the job model as review requests or approve-first

Status: proposed · 2026-10-17

## Context

The project owner decided on 2026-10-17:

- A change by Dusty goes in as a review request (write-back) or approve-first
  by default.
- A direct change needs a server flag, a namespace opt-in and a user opt-in.
- Changes run as jobs on the shared job model. Another design (ADRs 100 to
  109) designs the job model now. This ADR defines the port that the agent
  needs from it, and does not design the job model.
- Change freezes and break-glass apply to Dusty as to a person.

[RFC 12](../rfc/0012-api-edits-and-write-back.md) describes write-back to a
source as a review request. [RFC 13](../rfc/0013-policy-and-approvals.md)
describes approvals.

## Decision

### Modes

1. **Three modes.**
   - `review-request`: the job model writes the change back to the source of
     the unit as a review request. A person merges it, and the usual path
     applies it.
   - `approve-first`: the job model makes a job that waits for the approval
     of a person before it applies.
   - `direct`: the job model makes a job that applies with no approval by a
     person. Policies and change freezes still apply.
2. **The default mode depends on the kind of change.** For a kind that can
   write back to a source, the default is `review-request`. For any other
   kind, the default is `approve-first`. `direct` is never a default. The
   function `agent.ChangePolicy.Mode` holds this rule, and a table-driven
   test covers it.
3. **Direct changes need three switches.** A direct change needs all of
   these:
   - the installation setting `agentDirectChanges`, off by default, with
     `server:manage-agent`,
   - the opt-in of the namespace of the unit, with
     `namespace:manage-agent` and a fresh MFA step,
   - the opt-in of the person, with `caller:manage-agent-opt-ins` and a
     fresh MFA step.

   Without one of them, a request for `direct` fails with
   `ErrDirectNotAllowed`. The loop does not fall back to another mode: it
   tells the model, and the model can ask for `approve-first`. Each switch
   change writes an audit entry. The status route answers whether direct
   changes are possible for the person.
4. **An approval is a person.** The person who started a run can approve a
   change of that run, unless a policy of the namespace asks for another
   approver (RFC 13). The approval records that an agent proposed the
   change, and for whom.

### The port to the job model

5. **`agent.Changes`.** The job model implements this port. An agent calls
   it for each change. It never writes a unit, a source or a setting in
   another way.
   - `Support(ctx, unit, kind)`: what the kind of change supports for the
     unit. Today this is whether it can write back. It returns
     `ErrUnknownChangeKind`.
   - `Propose(ctx, ChangeRequest)`: it makes the change, or returns the
     change that the key already names. It returns `ErrInvalid`,
     `ErrUnknownChangeKind`, `ErrModeNotSupported`, `ErrChangeFrozen`,
     `ErrChangeKeyConflict`, and an error that wraps `identity.ErrDenied`.
   - `Get(ctx, actor, id)`: the change. It returns `ErrChangeNotFound`, also
     for a change that the actor cannot read.
   - `Cancel(ctx, actor, id)`: it cancels a change that did not start to
     apply. A second cancel returns the canceled change. It returns
     `ErrChangeNotFound`, `ErrChangeFinished` and an error that wraps
     `identity.ErrDenied`.
6. **What a request holds.** A `ChangeRequest` holds the key, the actor
   (the person, the channel, the run or the MCP session, and the
   break-glass flag), the unit, the kind, the mode, the spec as JSON and a
   reason in plain words. The job model shows the reason to the reviewer.
7. **What a change holds.** A `Change` holds the ID, the key, the unit, the
   kind, the mode, the state, the address of the review request or of the
   approval page, the subject of the proposer, the run and the time. The
   states are `waiting`, `queued`, `running`, `applied`, `failed`,
   `rejected` and `canceled`.
8. **The same checks as for a person.** The job model applies to a change
   by an agent each check that it applies to the same change by the
   person: the action of the kind of change, the policies, the change
   freezes and the approvals. The agent adds checks, never removes one.
9. **One change for one key.** The key comes from the run, the turn and
   the index of the call (ADR 120 decision 11), or from the MCP session and
   the request ID (ADR 123 decision 5). A second propose with one key and
   one request returns the first change. A second propose with one key and
   another request fails with `ErrChangeKeyConflict`.
10. **The kinds of change belong to the job model.** The agent package
    defines no kind of change and no spec. `describe_unit` answers the
    kinds that the job model offers for a unit, with the shape of each
    spec (ADR 122 decision 9).
11. **A conformance suite.** `agenttest.RunChanges` runs the job model
    against these cases: the support of each kind, a propose, a propose
    twice with one key, one key with another spec, a review request for a
    kind with no write-back, a change freeze in `approve-first` and in
    `direct`, a propose by an actor without the action, an unknown kind,
    an empty key, a get and a cancel. A test in the suite package runs the
    suite against a fake job model, so the cases run on each change.

### Change freezes and break-glass

12. **A change freeze stops an agent as it stops a person.** The job model
    checks the change freezes for each propose, in each mode. A frozen
    change fails with `ErrChangeFrozen`, and the agent tells the person.
13. **Break-glass applies through the person only.** An agent never starts
    a break-glass session, never asks for a break-glass account and never
    sets an override switch (ADR 44). A run that a person starts in a
    break-glass session acts with the rights of that session, as each
    request of the session does. Each audit entry of the run says that the
    session is break-glass (ADR 126). The run ends when the session ends
    (ADR 120 decision 14). An exception to a change freeze that the
    person holds in the session applies to a change of the run only when
    the job model gives the same exception to the same change by the
    person.
14. **No kind that needs an override.** The job model offers to agents no
    kind of change that needs `unit:write-forced` or
    `unit:write-unlocked` (ADR 44). A later ADR can change this.

## Consequences

- The job model of ADRs 100 to 109 must implement `agent.Changes`, run its
  suite, and give each change the fields of decision 7. If its design
  needs another shape, that design wins and this port follows it.
- A review request needs a source connection for the unit (RFC 12). A unit
  with no source gets `approve-first` by default.
- A run does not wait for an approval. The person reads the state of a
  change with `get_change` in a follow-up run, or in the console.

## Not decided here

- Change freezes themselves: who sets them and how. The job model or a
  policy design owns them.
- Whether an agent can approve a change. This ADR gives it no tool for it.
