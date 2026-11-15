# 299. Review requests and change orders: one integration with the Git host

Status: accepted · 2026-11-01. Decided by the project owner on 31 October and 1 November 2026. The design details are decisions of the PM under the owner's delegation, marked (PM), and the owner accepts them with the merge. Accepts RFC 114. This pull request holds design only: the ADR, the ports and their conformance suites, and the glossary terms

Changed by:

- ADR 328 extends decision 1: modes 2 and 4 in milestone 5, modes 1 and 3
  in milestone 6.
- ADR 359 extends decision 1: the mirror of approvals between the Git host and
  the change order.
- ADR 359 extends decision 15: the apply timing decides if an apply can run
  before the merge.
- ADR 359 extends decision 25: the checks and the table in the plan comment.
- ADR 368 changes decision 19: a closure test replaces the rule that a
  replacement is never disjoint.

Changes:

- Supersedes ADR 103 decision 3: the four review modes replace "OpenDustpan
  approvals add to the forge".
- Changes ADR 100 decisions 12 and 15: the `job.review-watch` poll ends.
- Changes ADR 102 decision 8: the plan comment on a review request holds
  counts and resource addresses by default.
- Changes ADR 116 decision 9: in modes 1 and 2, the approvals of a change in
  git follow the review mode of its units.
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
- Extends ADR 271 decision 2: the Forge port gets merge, status, comment,
  reaction, review listing, branch rules and permission operations.

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

The owner decisions of 31 October 2026 are in RFC 114, "Decisions of the
owner". The owner decisions of 1 November 2026 (decisions 4, 5, 6 and 11
below) are recorded in RFC 114 by pull request #2159, and in the record of
owner decisions that the PM keeps. RFC 114 had nine open questions. The owner
answered question 9, the scope of the mode, with decisions 4 to 6. Decisions
36 to 43 answer the other eight.

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
     apply, and runs are recorded. Mode 4 can turn on an apply on merge,
     opt-in, and the apply runs only when the person who merged holds the
     apply grant on each unit.
2. **Mode 4 and the requirement sources, in both directions (PM).** Mode 4
   refuses to turn on while any requirement source of ADR 225 decision 2
   holds at its scope or at a wider scope. A later save of such a source on
   a scope that has mode 4 below it fails, and the error names the scopes.
3. **The limits of mode 1 (PM).** A review on the Git host binds at L0 at
   most. A GitLab approval has no commit in its listing. An agent that uses
   the token of a person cannot be detected. The API and the wizard show
   these limits as text when a person chooses mode 1. The approval floor
   still applies.
4. **The scope of the mode (owner, 1 November 2026).** The mode is a unit
   setting with the cascade installation, namespace, collection, unit. A
   level can force a value, and a lower level cannot override it. The API
   shows the effective value and its source. This replaces the options of
   RFC 114 question 9 (one mode for each repository and branch, and the code
   location as the scope of the mode).
5. **The strictest mode for a review request (owner, 1 November 2026. Order
   by the PM).** A review request that touches units with different modes
   uses the strictest mode, and says which unit set it. The order from the
   strictest is mode 3, mode 2, mode 1, mode 4, because the most restrictive
   rule wins where rules overlap. A review request that touches a mode 3 unit
   follows the rules of mode 2.
6. **Branch-wide settings (owner, 1 November 2026).** Before the server turns
   on a setting of the Git host that holds for the whole branch (the required
   status, "only OpenDustpan merges"), it lists each unit on that repository
   branch that the setting affects. It refuses until the effective mode of
   each affected unit allows it, or a forced value above them settles it. The
   refusal names each unit, its mode and source, the grant and the people who
   hold it.
7. **Who sets a mode (PM).** A change of mode needs `namespace:manage-gates`
   (or `installation:manage-gates`), writes an audit entry `change`, and
   needs a fresh authentication when the new mode asks for fewer approvals.

### The review UI

8. **One view of a change (owner).** The change order page has three panes:
   the IaC changes, the comments of people, and the comments of systems
   (bots and agents), and one timeline that merges them. On a phone, the
   panes are tabs, with the timeline first. The code diff that is not IaC is
   read from the Git host and shown as a lens, never stored as a copy. Code
   comments live on the Git host, and OpenDustpan writes them as the person
   with the token of decision 32. Plan comments live in OpenDustpan (ADRs
   193 and 198). The plan diff is structural, by resource and attribute, and
   holds 4,000 or more resources. The code diff is word by word.

### Approvals

9. **Three binding levels (owner).** A visible setting for each scope, with
   the default L1. L0 binds an approval to the commit. L1 binds it to the
   set of operations: each resource address and its kind of change. L2 binds
   it to the exact plan (an OpenTofu saved plan, and a Pulumi update plan
   only when the Pulumi CLI marks update plans stable). A save of L2 on a
   scope with a Pulumi unit fails until then, and the server never falls
   back to L1. In each level, the tree of the merged commit must be the tree
   that was planned, or the server plans again.
10. **The approval floor (owner).** At every level, a new delete, a new
    replace or any operation that the approved set does not hold stops the
    apply for a new approval with a fresh authentication. Each approval
    records the set of operations of the revision that the approver saw, at
    every level (PM).
11. **The reset at each level (owner, 1 November 2026).** Each level resets
    on what it binds to: L0 on any new commit, L1 on a change of the set of
    operations, L2 on any change of the plan. An approval by a principal who
    later becomes an author is void at once. The approvals of the Git host
    follow the rules of the Git host.
12. **The binding level takes over signature carry-over (owner. Details by
    the PM).** The binding level decides when an approval carries over. A
    parent scope, also the installation, can forbid weaker levels below it.
    A change to a weaker level needs the manage-gates action and a fresh
    authentication, and writes an audit entry `change`. The setting
    `signatureCarryOver` has no users and no stored data. It is deleted with
    no conversion.
13. **Refresh and drift (owner, 31 October 2026).** A refresh keeps the
    default of each tool. A scope can set plans with no refresh for OpenTofu
    (`-refresh=false`). Drift detection covers drift, and a gate can require
    a recent drift check.
14. **Authors (owner).** Whoever opened the review request, authored a commit
    in it or pushed to it is an author, and cannot approve it on either side.
    A `Co-authored-by` trailer does not make an author. There is no exception
    for a person alone: `authorMayApprove` does not apply to a review request.
    The requester of an agent or write-back job behind a push is an author.
    Commit author fields count only where the Git host verified the account
    (PM). While the opener or a pusher is not linked, no approval counts in
    modes 1 and 2 (PM).
15. **A merge and the apply (owner).** A merge starts an apply only when the
    person who merged is linked and holds `unit:run-apply` on each unit
    (`unit:run-destroy` on each unit with a destroy). Otherwise the change
    order waits for a person with the grant. For a merge queue, the person
    who merged is the person who queued the change. For an app merge in mode
    2, it is the person who asked for it in the console (PM).
16. **The order of applies (PM).** An apply runs only the latest commit that
    changes the unit. The approved set is the union of the approved sets of
    each change order merged into the unit since its last apply, and never
    holds the set of a change order that was not approved.
17. **A merge around the gate (PM).** A merge that passed around the gate
    marks the change order `merged-unapproved`, writes an audit entry, starts
    no apply, and marks the unit. The unit applies nothing until an approved
    change order starts from the last applied commit and holds the bypassed
    commit.
18. **The checks of each gate decision (owner. Details by the PM).** Each
    gate decision reads the head commit fresh from the Git host and compares
    it with the commit of the revision. A difference fails closed. A status
    with the name of an OpenDustpan status that another account or app
    posted fails the gate: the server matches a status by its creator, never
    by its name alone. GitLab, Gitea and Forgejo match a required status by
    name, so on them the console says that the gate of the Git host is
    advisory, and the apply reads only the record of OpenDustpan.

### Plans and credentials

19. **Plan conflicts in place of unit locks (owner).** Two review requests
    that touch the same unit can both merge while their code merges cleanly.
    The status stays red when the plans conflict. "No conflict" holds only
    when the check proves the two closures disjoint (PM). The first change
    order that is approved keeps green, decided in one transaction. An
    eligible approver can accept an overlap with a reason. A merge plans the
    others again (PM). The status on the Git host names no resource.
20. **One credential rule for every start of a plan (owner. Details by the
    PM).** A plan runs with credentials only when the opener and each pusher
    of its head are linked to a principal that holds `unit:run-plan`. The
    rule holds for a push, a base-branch push that plans again, "Plan again",
    `dustpan plan`, the catch-up read and a merge queue commit. Otherwise the
    plan runs with no provider credentials, no state, no secret outputs and
    no outbound network, and it can never be approved. Credentials never
    follow a trusted comment alone.
21. **Forks (owner).** A review request from a fork gets no plan until a
    trusted person comments. The comment names the head commit and binds to
    it, and a new push voids it. The plan of a fork has no credentials (decision
    20).
22. **Bots (PM).** A bot whose pushes no repository user controls (by the app
    ID of the Git host, never by its login) can be linked to a service
    principal with a stored `unit:run-plan` grant. A bot that pushes for the
    users who start it, for example `github-actions[bot]`, is refused. For
    each bot push, the server checks the person behind it with the logic of
    decision 14, and a push with no known person plans with no credentials.
    A linked self-hosted Renovate must have `postUpgradeTasks` off, or read
    its configuration only from the default branch. The server cannot check
    this, so the link form states the rule and the person who links the bot
    accepts it.
23. **"Plan with credentials as me" (PM).** For a review request of the same
    repository, never a fork, a trusted person can plan with credentials as
    themselves for the current head. The plan binds to that head, a new push
    voids it, it writes an audit entry, and the person becomes an author.
24. **Declared paths (PM).** Each unit has declared paths. They only add
    files to the computed file closure. A unit whose closure is unknown stays
    pending until a person with `unit:manage` records an acceptance with a
    reason and a fresh authentication. The unit and each `approvals` success
    that rests on the acceptance show "closure not proven". The acceptance
    stays until the closure becomes provable or the declared paths change.

### The Git host

25. **The plan comment (owner).** One comment on the review request, edited
    in place, with a summary and a link. By default the summary holds counts
    and resource addresses with their kind of change, and never a value. The
    full diff is opt-in.
26. **Leaks (owner).** A comment command and a console button redact a plan.
    A redaction invalidates the plan, and an apply needs a new plan. The
    server deletes the comment and posts a new one with a notice. Later
    comments of that review request hold the summary only, until a person
    turns the diff on again. The UI lists the addresses whose values leaked.
    The docs say that the history of the Git host cannot be cleaned.
27. **Details of a redaction (PM).** A redaction voids the signatures of the
    revision and shreds its plan parts and logs. The redact command can also
    hide resource addresses, because an address can hold data.
28. **Who may redact (PM).** A linked principal that can read the unit, and
    each run writes an audit entry. The command does not also need
    `job:read-plans`. Benchmark: least privilege for an action that only
    deletes data.
29. **No polling (owner).** Webhooks, a "Plan again" button and the command
    `dustpan plan`, one catch-up read at start, and manual redeliveries that
    the server decides from the state of the effect. Federation keeps its
    poll (ADR 271 decision 9). A received command gets a reaction with an
    emoji that the installation sets, default "eyes".
30. **The review watch poll goes (PM).** The `job.review-watch` poll of ADR
    100 decisions 12 and 15 ends. The webhook, the catch-up read at start and
    "Plan again" record the merge and the close of a write-back review
    request.
31. **Webhooks and identity (owner).** The server verifies each signature,
    refuses a replay, and never decides on the payload alone. Identity links
    use the immutable user ID of the Git host.
32. **No user token stored on the backend (owner).** These rules hold apart
    from any other ADR. The user token of a Git host lives in a sealed cookie
    of its own. The server never writes it to the database, a log, an audit
    entry, a backup, a runner or an error. It uses the token only inside the
    request of the person, checks at each use that the account of the token
    is the account linked to the person, never refreshes the token, and ends
    the cookie at sign-out. ADR 297 gives the sealed cookies of the
    console sessions, and the cookie follows it.
33. **Failures (owner).** A Git host that says "not mergeable" or does not
    answer fails the command with its own answer. A partial apply records "N
    of M applied" truthfully, with no blocking and no rollback, and a
    one-click retry from the same commit while it is still the latest commit
    of the unit.
34. **Marks of Git hosts.** ADR 289 holds the decision, and the views of a
    review request follow it.
35. **Agents never approve, on either side (owner).**

### The questions that the RFC left open (PM)

The RFC gave a recommendation for each of its other eight open questions.
The PM decides each one under the decision rule of the owner: each decision
has a benchmark, and each one can be reversed by a later ADR.

36. **A change of mode** applies only to change orders that start after it.
    Each change order keeps the mode that it had at its creation. Benchmark:
    a change of a GitHub branch protection rule applies to new reviews.
37. **One operation at L1** is the address (or the URN) and the kind of
    change. Each order of a replace is its own kind. Benchmark: an OpenTofu
    plan lists each change as a resource address and an action.
38. **An apply before the merge** keeps the hold of RFC 27 question 6: the
    units stay held from the first pre-merge apply until the merge. Benchmark:
    Atlantis locks a directory from the plan until the merge or the close.
39. **The status on GitLab Free and Premium** is tested in both directions
    before the code depends on it: does it block a merge, and can it unblock a
    merge whose CI pipeline failed. If a test fails, a documented CI job of the
    team asks the server for the state of `approvals`. Until the tests pass,
    the wizard warns about both risks. Benchmark: test both directions, and
    fail closed.
40. **A trusted person** is a linked principal with `unit:run-plan` on each
    unit and write access on the Git host. Benchmark: GitHub asks for write
    access to approve and run a workflow of a pull request from a fork.
41. **The scope of the plan conflict check** is each namespace that reads an
    output of a changed unit, limited to the units that the reader of the
    check can see. A reader that the check cannot see counts as a conflict.
    Benchmark: fail closed when a reader is unknown.
42. **A Git host that is down.** A person with a new action
    `changeorder:detach` and a fresh authentication can move the change to a
    review in OpenDustpan only, with a reason. The break-glass review of ADR
    142 follows. Benchmark: a break-glass with a fresh authentication and a
    review after use, as ADR 245 gives.
43. **A change across many repositories** is one change order for each review
    request. A later RFC can group linked review requests. Benchmark: Atlantis
    and Spacelift run one plan for each pull request.

### Records

44. **Audit entries (PM).** Each automatic action and each decision writes an
    audit entry, so that a person can ask why an approval counted and get the
    record:

    | Kind | Holds |
    | --- | --- |
    | Review counted or not counted | The review ID on the Git host, the account ID, the commit, the reason |
    | Author set | Each author and its source |
    | Conflict decision | The two change orders, proven disjoint or not, an accepted overlap with its reason |
    | Status post | The status, the commit, the result |
    | Comment command | The command, the account, the result |
    | Use of a user token | The person, the action, the object ID on the Git host |
    | Mode or binding level change | The scope, the old and the new value |
    | Merge around the gate (`merged-unapproved`) | The merge commit, the merger, what the gate missed |
    | Redaction | The person, the revision, the hidden addresses |
    | Closure acceptance | The person, the unit, the reason |
45. **The evidence of a gate decision (PM).** Each gate decision record stores
    the facts that counted: the review IDs on the Git host, the immutable
    account IDs, the commits, the verdicts and the read time, with no comment
    text. An apply at L1 records how many attributes differ from the approved
    plan.

### Ports

46. **A domain package `pkg/reviewrequest` (PM).** It is tool-neutral and
    forge-neutral, and depguard holds it to the rules of the domain
    packages. A unit is an `identity.Object` (tool, namespace and the tool's
    own name). The package holds the mode, the binding level, the kinds of
    operation and the approval floor of a kind, the authors and their
    sources, and four ports, each with its errors:
    - `Store`: the records of the review requests, with the fence and the
      audit entry in each write.
    - `GitHost`: read a fresh snapshot (with the creator of each status, the
      merger, the queuer and the merge commit), read the branch rules and
      the bypass list, read the permission of an account, post a status,
      write and delete the plan comment, react, and merge with a fixed head.
      It never asks the Git host to compute the merge state again. It
      returns Git host account IDs only: the service, never the adapter,
      looks up the links of accounts to principals. Merge refuses when the
      app of the connection can bypass the branch rules.
    - `ClosureResolver`: the file closure of a unit at a commit, which never
      reads outside the repository.
    - `ConflictChecker`: the check of two plans, proven disjoint or a
      conflict, with an input for readers that the check cannot see.
47. **The Forge port gets the operations that GitHost needs (PM).** The
    adapter of GitHost uses the Forge port of ADR 271. The Forge port gets
    new operations, each behind a capability and each with its cases in the
    Forge conformance suite first: merge with a fixed head, set a status, the
    plan comment (write, edit, delete), react, list the reviews, read the
    branch rules with the bypass list, and read the permission of an
    account. This pull request adds none of them. They come with the first
    adapter.
48. **Conformance suites first (PM).** Package `reviewrequesttest` lists the
    cases of each port, and the cases of the service of the review requests,
    with the decision that each case tests in its name. Each case skips until
    the first adapter of its port, or the service, gives it a body.

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
