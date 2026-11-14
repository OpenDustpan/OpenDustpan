# 223. Access limits: Cedar forbid policies for each scope

Status: accepted · 2026-10-18. Part of ADR 222. Supersedes ADR 52 decision 9. Extends ADR 54 decision 5. Decision 4 superseded in part by ADR 240

Changed by:

- ADR 362 changes decisions 4 and 11: a Cedar entity for a group uses the stable ID of the group, not its name.
- ADR 240 changes decision 4: the principal kind takes person, service and
  workload.
- ADR 313 extends: grant conditions.

## Context

Access is allow-only. A principal can do an action on an object when a
grant gives a role with that action (ADR 52 decision 5). ADR 52 decision 9
plans conditions on the attributes of a grant, and does not say how a
condition stops a grant. A team wants rules such as "nobody outside `sre`
destroys a unit of the collection `prod`" or "no agent reads a secret
value", whatever the grants give.

The spike evaluated each access limit after the grants allow, with one
fixed `permit (principal, action, resource);` and the `forbid` policies of
the limit set. A `permit` in a limit set was refused at save.

## Decision

1. **Forbid only (owner).** An access limit is one Cedar `forbid` policy. A
   limit set holds `forbid` policies only. The server refuses at save a
   policy with the effect `permit`, with `gate.ErrInvalid`. A limit is a
   ceiling: it can only take away what a grant gives. It never gives an
   action.
2. **For each scope.** An access limit has a scope: the installation, a
   namespace, a collection or a unit. A limit holds for each object in its
   scope. A limit at a narrower scope adds to the limits of the wider
   scopes. No limit replaces another.
3. **After the grants.** The `identity.Authorizer` checks the grants first.
   When the grants allow, it evaluates the limits of each scope that holds
   the object, in one Cedar request, with the fixed policy
   `permit (principal, action, resource);`. A `forbid` that matches refuses
   the call with `gate.ErrLimited`. The error names the limit ID.
4. **The request.** The principal is `OpenDustpan::Principal::"<subject>"`
   with its groups as parents. The action is `OpenDustpan::Action::"<action>"`,
   with the action names of ADR 210. The resource is the object of the
   action. The context holds the facts of the access schema (ADR 226
   decision 2): the principal kind (`person`, `service` or `agent`), the credential kind (`session`, `access-token`, `job-token` or
   `trust-token`, owner decision of 2026-10-18), the authentication age, the
   unit labels and the collection.
5. **Fail closed.** A limit that gives a Cedar error refuses the call with
   `gate.ErrRuleFailed`, never with an allow. The save type-checks each
   limit against the access schema (ADR 224 decision 8), so this error is
   rare.
6. **Every credential.** A limit holds for a session, an access token, a
   job token and a token that a trust rule issued (ADR 54). A trust rule
   still decides whether the server issues a token. It never passes a
   limit.
7. **Break-glass accounts are outside the limits.** A session of a
   break-glass account (ADR 70) is not subject to an access limit, so that
   no limit can block a working path that ADR 70 decision 24 counts. The
   audit entries of such a session say so. Gates, approval rules and fixed
   controls hold for each change of such a session.
8. **A refusal writes a decision record.** An access limit that refuses a
   call writes a decision record (ADR 229). An allow writes none, because
   the server checks access on each request. Refusals of the same
   principal, the same action and the same limit within 60 seconds share
   one record, with a counter and the time of the last refusal. A record of
   a refusal lives for the audit retention (ADR 68).
9. **Changes.** A change of a limit needs the action of its scope (ADR 230
   decision 2), a fresh authentication (ADR 227), and writes an audit
   entry. The console shows the limits of each scope next to the grants
   of the access review (ADR 52).
10. **No limit locks out the gate configuration.** The server refuses with
    `gate.ErrInvalid`:
    - a limit of a namespace, a collection or a unit that can match the
      action `installation:manage-gates`.
    - a limit that matches `namespace:manage-gates` for every principal,
      that is a limit with no condition on the principal.

    A limit that reads a unit label holds only while the label is there. A
    holder of the label-edit action of the unit can remove the label, and
    with it the limit. So a limit for a set of units names
    `unit.collection`, which only a holder of the collection action can
    change. The console shows this advice when a limit reads a label.
11. **Labels that limits and gates read (PM).** A label change is not a
    state write and meets no `state.write` (ADR 224 decision 16). Holders
    of the label-edit action of a unit can change each label of it, also a
    label that an access limit or a gate reads. The label-edit action is
    `unit:edit-labels`. Each route that changes labels needs it, also
    `PATCH .../tags` of the Pulumi Cloud-compatible API, which needs
    `unit:write` today. A create of a unit sets its first labels with the action
    of the create (`unit:manage` at `POST /api/stacks/{namespace}/{project}`)
    and needs no `unit:edit-labels`, because the Pulumi CLI sends tags at each
    create. So a limit on `unit:edit-labels` does not hold for the labels of a
    create, and a rule that must hold for each new unit names `unit.collection`.
    Each built-in role that holds `unit:write` also
    holds `unit:edit-labels`. A scope that wants to protect its labels
    saves an access limit that forbids the label-edit action for everyone
    except a named group:

    ```cedar
    forbid (
      principal,
      action == OpenDustpan::Action::"unit:edit-labels",
      resource
    )
    unless { principal in OpenDustpan::Group::"platform" };
    ```

    Decision 10 holds for this limit, because it has a condition on the
    principal and does not match a manage action of the gates.

    Benchmark: AWS service control policies protect the tags that
    attribute-based access control reads. A policy denies
    `ec2:CreateTags` and `ec2:DeleteTags` on the tag keys of the access
    rules, except for one administrator role.

Benchmark: AWS Organizations service control policies and Google Cloud deny
policies are ceilings that only take away access. They never give it.

## Consequences

- ADR 52 decision 9 is replaced: a grant keeps the shape of ADR 52 decision
  3, and a condition lives in a limit.
- The access-review export (ADR 52) lists the limits next to the grants.
