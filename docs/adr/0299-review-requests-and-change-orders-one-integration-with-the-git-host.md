# 299. Review requests and change orders: one integration with the Git host

Status: accepted · 2026-11-01. Decided by the project owner on 31 October and 1 November 2026. The design details are decisions of the PM under the owner's delegation, marked (PM), and the owner accepts them with the merge. Accepts RFC 114. This pull request holds design only: the ADR, the ports and their conformance suites, and the glossary terms

Changes:

- Supersedes ADR 103 decision 3: the four review modes replace "OpenDustpan
  approvals add to the forge".
- Changes ADR 102 decision 8: the plan comment on a review request holds
  counts and resource addresses by default.
- Extends ADR 125: a write-back review request carries a change order, and
  the review mode decides which approvals count.
- Changes ADR 190 decisions 4, 9, 10, 12 and 14: authors from the Git host,
  a new plan at the apply under the approval floor, the merge as the apply
  request, a close of the review request closes the change order, and a
  review request that an agent opens makes a change order.
- Changes ADR 191 decisions 4, 5 and 6: the three binding levels.
- Extends ADR 192: in mode 1, an approving review on the Git host counts.
- Changes ADR 193: code comments of a review request live on the Git host,
  and one blocking rule covers both sources.
- Changes ADR 225 decisions 1, 7 and 12: `authorMayApprove` does not apply to
  a review request, mode 1 counts reviews on the Git host, and the binding
  level replaces signature carry-over.
- Changes ADR 267 decisions 14 and 19: one credential rule for every start of
  a plan, and a fork plan with no credentials.
- Changes ADR 271 decision 4: webhooks with three recovery paths and no
  polling.
- Extends ADR 271 decision 7: the comment commands `dustpan plan` and the
  redact command, and a reaction on each received command.

## Context

A team that uses a Git host reviews a change in a review request. ADRs 190 to
198 give OpenDustpan a complete review product for a change, the change
order. ADR 103 decision 3 said that OpenDustpan approvals add to the
approvals of the Git host, with no link between the two. So one change had
two review places. Each approver approved two times, request changes in one
place did not block the other, a close of the review request did not close
the change order, and a merge could happen before the change order was
approved. The project owner called this state a mistake.

[RFC 114](../rfc/0114-review-requests-and-change-orders.md) holds the
research, the facts of each Git host (GitHub, GitLab Free, Premium and
Ultimate, Gitea, Forgejo), two rounds of adversarial review, and the owner
decisions. This ADR records them. The RFC keeps the reasons and the details
of each part. Where this ADR and the RFC differ, this ADR holds.

## Decision

### Modes and their scope

1. **Four review modes, with no default (owner).** The mode is a required
   field in the API, the IaC providers, the CLI and the setup wizard. The
   server asks for it when a Git host connection is first attached to a
   scope. The wizard shows each mode with a one-line trade-off and no answer
   selected.
   - Mode 1, merged approvals: approvals on the Git host and in OpenDustpan
     both count. The required status of OpenDustpan enforces the result. Not
     recommended.
   - Mode 2, OpenDustpan merges: the branch rules require the status of
     OpenDustpan. Optionally only the OpenDustpan app merges.
   - Mode 3, no review requests: change orders start from commits.
   - Mode 4, no change orders: no approval. Deny rules and policies always
     apply, and runs are recorded. Mode 4 refuses to turn on while any
     requirement source of ADR 225 decision 2 holds at its scope or at a
     wider scope. Mode 4 can turn on an apply on merge, opt-in, and the apply
     runs only when the person who merged holds the apply grant.
2. **The limits of mode 1 (PM).** A review on the Git host binds at L0 at
   most. A GitLab approval has no commit in its listing. An agent that uses
   the token of a person cannot be detected. The API and the wizard show
   these limits as text when a person chooses mode 1. The approval floor
   still applies.
3. **The scope of the mode (owner).** The mode is a unit setting with the
   cascade installation, namespace, collection, unit. A level can force a
   value, and a lower level cannot override it. The API shows the effective
   value and its source.
4. **The strictest mode for a review request (owner. Order by the PM).** A
   review request that touches units with different modes uses the
   strictest mode, and says which unit set it. The order from the strictest
   is mode 3, mode 2, mode 1, mode 4, because the most restrictive rule wins
   where rules overlap. A review request that touches a mode 3 unit follows
   the rules of mode 2.
5. **Branch-wide settings (owner).** Before the server turns on a setting of
   the Git host that holds for the whole branch (the required status, "only
   OpenDustpan merges"), it lists each unit on that repository branch that
   the setting affects. It refuses until the effective mode of each affected
   unit allows it, or a forced value above them settles it. The refusal
   names each unit, its mode and source, the grant and the people who hold
   it.
6. **Who sets a mode (PM).** A change of mode needs `namespace:manage-gates`
   (or `installation:manage-gates`), writes an audit entry `change`, and
   needs a fresh authentication when the new mode asks for fewer approvals.

### Approvals

7. **Three binding levels (owner).** A visible setting for each scope, with
   the default L1. L0 binds an approval to the commit. L1 binds it to the
   set of operations: each resource address and its kind of change. L2 binds
   it to the exact plan (an OpenTofu saved plan, and a Pulumi update plan
   only when the Pulumi CLI marks update plans stable). A save of L2 on a
   scope with a Pulumi unit fails until then, and the server never falls
   back to L1. In each level, the tree of the merged commit must be the tree
   that was planned, or the server plans again.
8. **The approval floor (owner).** At every level, a new delete, a new
   replace or any operation that the approved set does not hold stops the
   apply for a new approval with a fresh authentication. Each approval
   records the set of operations of the revision that the approver saw, at
   every level (PM).
9. **The reset at each level (owner, 1 November 2026).** Each level resets on
   what it binds to: L0 on any new commit, L1 on a change of the set of
   operations, L2 on any change of the plan. An approval by a principal who
   later becomes an author is void at once. The approvals of the Git host
   follow the rules of the Git host.
10. **The binding level takes over signature carry-over (owner. Details by
    the PM).** The binding level decides when an approval carries over. A
    parent scope, also the installation, can forbid weaker levels below it.
    A change to a weaker level needs the manage-gates action and a fresh
    authentication, and writes an audit entry `change`. The setting
    `signatureCarryOver` has no users and no stored data. It is deleted with
    no conversion.
11. **Refresh and drift (owner).** A refresh keeps the default of each tool.
    A scope can set plans with no refresh for OpenTofu (`-refresh=false`).
    Drift detection covers drift, and a gate can require a recent drift
    check.
12. **Authors (owner).** Whoever opened the review request, authored a commit
    in it or pushed to it is an author, and cannot approve it on either side.
    A `Co-authored-by` trailer does not make an author. There is no exception
    for a person alone: `authorMayApprove` does not apply to a review request.
    The requester of an agent or write-back job behind a push is an author.
    Commit author fields count only where the Git host verified the account
    (PM). While the opener or a pusher is not linked, no approval counts in
    modes 1 and 2 (PM).
13. **A merge and the apply (owner).** A merge starts an apply only when the
    person who merged is linked and holds `unit:run-apply` (`unit:run-destroy`
    for a destroy). Otherwise the change order waits for a person with the
    grant. For a merge queue, the person who merged is the person who queued
    the change. For an app merge in mode 2, the person who asked for it in
    the console (PM).
14. **The order of applies (PM).** An apply runs only the latest commit that
    changes the unit. The approved set is the union of the approved sets of
    each change order merged into the unit since its last apply, and never
    holds the set of a change order that was not approved. A merge that
    passed around the gate marks the change order `merged-unapproved`, starts
    no apply, and the unit applies nothing until an approved change order
    starts from the last applied commit and holds the bypassed commit.

### Plans and credentials

15. **Plan conflicts in place of unit locks (owner).** Two review requests
    that touch the same unit can both merge while their code merges cleanly.
    The status stays red when the plans conflict. "No conflict" holds only
    when the check proves the two closures disjoint (PM). The first change
    order that is approved keeps green, decided in one transaction. An
    eligible approver can accept an overlap with a reason. A merge plans the
    others again (PM). The status on the Git host names no resource.
16. **One credential rule for every start of a plan (owner. Details by the
    PM).** A plan runs with credentials only when the opener and each pusher
    of its head are linked to a principal that holds `unit:run-plan`. The
    rule holds for a push, a base-branch push that plans again, "Plan again",
    `dustpan plan`, the catch-up read and a merge queue commit. Otherwise the
    plan runs with no provider credentials, no state, no secret outputs and
    no outbound network, and it can never be approved. Credentials never
    follow a trusted comment alone.
17. **Bots and "Plan with credentials as me" (PM).** A bot whose pushes no
    repository user controls (by the app ID of the Git host) can be linked to
    a service principal with a stored `unit:run-plan` grant. A bot that pushes
    for the users who start it, for example `github-actions[bot]`, is
    refused. For a review request of the same repository, never a fork, a
    trusted person can plan with credentials as themselves for the current
    head, and becomes an author.
18. **Declared paths (PM).** Each unit has declared paths. They only add
    files to the computed file closure. A unit whose closure is unknown stays
    pending until a person with `unit:manage` records an acceptance with a
    reason and a fresh authentication, which shows "closure not proven".

### The Git host

19. **The plan comment (owner).** One comment on the review request, edited
    in place, with a summary and a link. By default the summary holds counts
    and resource addresses with their kind of change, and never a value. The
    full diff is opt-in.
20. **Leaks (owner).** A comment command and a console button redact a plan.
    A redaction deletes the comment and posts a new one with a notice, voids
    the signatures of the revision and shreds its plan parts and logs (PM).
    The UI lists the addresses whose values leaked. The docs say that the
    history of the Git host cannot be cleaned.
21. **No polling (owner).** Webhooks, a "Plan again" button and the command
    `dustpan plan`, one catch-up read at start, and manual redeliveries that
    the server decides from the state of the effect. Federation keeps its
    poll (ADR 271 decision 9). A received command gets a reaction with an
    emoji that the installation sets, default "eyes".
22. **Webhooks and identity (owner).** The server verifies each signature,
    refuses a replay, never decides on the payload alone, and reads the head
    commit fresh before each gate decision. Identity links use the immutable
    user ID of the Git host.
23. **No user token stored on the backend (owner).** The user token of a Git
    host is in a sealed cookie of its own, as ADR 297 gives sessions (in
    review in its own pull request). The server uses it only inside the
    request of the person, never refreshes it, and checks the linked account
    at each use.
24. **Failures (owner).** A Git host that says "not mergeable" or does not
    answer fails the command with its own answer. A partial apply records "N
    of M applied" truthfully, with no blocking and no rollback, and a
    one-click retry from the same commit while it is still the latest commit
    of the unit.
25. **Marks of Git hosts.** ADR 289 holds the decision, and the views of a
    review request follow it.
26. **Agents never approve, on either side (owner).**

### The questions that the RFC left open (PM)

The RFC gave a recommendation for each of its last eight questions. The PM
decides each one under the decision rule of the owner: each decision has a
benchmark, and each one can be reversed by a later ADR.

27. **A change of mode** applies only to change orders that start after it.
    Each change order keeps the mode that it had at its creation. Benchmark:
    a change of a GitHub branch protection rule applies to new reviews.
28. **One operation at L1** is the address (or the URN) and the kind of
    change. Each order of a replace is its own kind. Benchmark: an OpenTofu
    plan lists each change as a resource address and an action.
29. **An apply before the merge** keeps the hold of RFC 27 question 6: the
    units stay held from the first pre-merge apply until the merge. Benchmark:
    Atlantis locks a directory from the plan until the merge or the close.
30. **The status on GitLab Free and Premium** is tested in both directions
    before the code depends on it: does it block a merge, and can it unblock a
    merge whose CI pipeline failed. If a test fails, a documented CI job of the
    team asks the server for the state of `approvals`. Until the tests pass,
    the wizard warns about both risks. Benchmark: test both directions, and
    fail closed.
31. **A trusted person** is a linked principal with `unit:run-plan` on each
    unit and write access on the Git host. Benchmark: GitHub asks for write
    access to approve and run a workflow of a pull request from a fork.
32. **The scope of the plan conflict check** is each namespace that reads an
    output of a changed unit, limited to the units that the reader of the
    check can see. A reader that the check cannot see counts as a conflict.
    Benchmark: fail closed when a reader is unknown.
33. **A Git host that is down.** A person with a new action
    `changeorder:detach` and a fresh authentication can move the change to a
    review in OpenDustpan only, with a reason. The break-glass review of ADR
    142 follows. Benchmark: a break-glass with a fresh authentication and a
    review after use, as ADR 245 gives.
34. **A change across many repositories** is one change order for each review
    request. A later RFC can group linked review requests. Benchmark: Atlantis
    and Spacelift run one plan for each pull request.

### Ports

35. **A domain package `pkg/reviewrequest` (PM).** It is tool-neutral and
    forge-neutral, and depguard holds it to the rules of the domain
    packages. It holds the mode, the binding level, the kinds of operation
    and the approval floor of a kind, the authors and their sources, and four
    ports, each with its errors:
    - `Store`: the records of the review requests, with the fence and the
      audit entry in each write.
    - `GitHost`: the narrow port over the Forge port: read a fresh snapshot,
      post a status, write and delete the plan comment, react, merge with a
      fixed head. It never asks the Git host to compute the merge state again,
      and never merges as a bypass actor.
    - `ClosureResolver`: the file closure of a unit at a commit.
    - `ConflictChecker`: the check of two plans, proven disjoint or a
      conflict.
36. **Conformance suites first (PM).** Package `reviewrequesttest` lists the
    cases of each port. Each case skips until the first adapter of its port
    gives it a body, in the pull request of that adapter.

## Consequences

- One change has one record that binds approvals from both sides, and each
  side's gate is an input of the other, with no loop.
- The service, the adapters, the routes, the setting keys, the migrations,
  the console screens and the user docs come in later pull requests, each
  with its e2e steps and the bodies of its conformance cases.
- The code of `signatureCarryOver` is deleted in its own pull request.
- The facts that the RFC marks unverified need a test against the real Git
  host before the code depends on them, for example whether a status from
  the API blocks a merge on GitLab Free and Premium.
- `GLOSSARY.md` gets the terms Review mode, Binding level, Approval floor,
  Set of operations, Plan conflict, Declared paths and Plan comment, and the
  entries Review request and Comment command change.
