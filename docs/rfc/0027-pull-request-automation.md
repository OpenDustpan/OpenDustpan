---
rfc: 27
title: Pull request automation
status: draft
created: 2026-10-10
---

# RFC 27. Pull request automation

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

RFC 12 gives forge connections, webhooks, statuses and merges. Atlantis,
Stategraph, OpenTaco, env zero, Scalr, Terramate, Gruntwork and Pulumi review
stacks give more in the review request itself, and this is how most OpenTofu
teams work today.

## First ideas

- One summary comment edited in place, with a table of `+ ~ -` per unit and
  links to the console; GitHub Checks as an alternative.
- Comment commands (`plan`, `apply`, `unlock`) with limits on who may use
  them.
- A stale-plan warning with the command that fixes it, and conflict detection
  after a deployment (Terramate).
- Apply requirements: approved, mergeable, not behind the base branch, code
  owners.
- Locks that a review request holds from plan until merge or close, for
  OpenTofu workspaces too (RFC 12 has a hold for Pulumi only).
- A temporary unit per review request (Pulumi review stacks), destroyed on
  close (RFC 43).
- A global switch that stops applies, for incidents and change freezes
  (Atlantis).

## Open questions

- Which of these need a runner, and which can the backend do from webhooks
  alone?
- How does comment noise stay low in a monorepo with many units?

## Owner direction (2026-10-30)

The project owner gave this direction on 2026-10-30, through the project
manager. This section decides nothing. An ADR records a decision (ADR 15).

- The first slice for a pull request is a plan, one commit status and one
  summary comment.
- An apply runs after the merge, from the default branch, by default.
- A pre-merge apply, from the branch of the pull request, is also an
  option. It is not the default, and the docs say that the project does not
  recommend it.
- Runners run both tools. The gates and approvals of ADRs 222 to 230 are
  the policy checks.

## Proposal for Milestone 5 (2026-10-30)

This section is ready to accept. Facts about other products are in the
research note [m5-precedents](../research/m5-precedents.md). Each fact there
has a mark that says whether its source was read on 2026-10-30.

### Decisions this proposal starts from

| Source | Decision |
| --- | --- |
| Owner, 2026-10-30 | An apply runs after the merge, from the default branch, by default. |
| Owner, 2026-10-30 | A pre-merge apply (from the branch of the review request) is an option. It is not the default, and the docs say that the project does not recommend it. |
| Owner, 2026-10-30 | The first slice is a plan, one status and one summary comment. |
| Owner, 2026-10-30 | The gates and approvals of ADRs 222 to 230 are the policy checks. |
| ADR 267 decisions 15 and 19 | A push to a review request starts a plan of each matching unit, with the source kind `forge`. A fork starts nothing. A newer push replaces the older plan. |
| ADR 271 decision 7 | `dustpan apply` and `unlock` are the comment commands. Only an account that a person linked to a principal can run one, and it runs as that principal. A comment never approves and never passes a gate. The server reads the review request and the comment through the API before it acts. |
| ADR 271 decision 4 | An event is a hint. The server reads the object through the API before it changes a record. |
| ADRs 190 and 191 | A change order holds the plans of many units, and its approvals bind to the digest of its plans. Apply is a request of a person (ADR 190 decision 10). |
| ADR 213 decision 2 | A plan of a branch that is not protected runs with no secrets of the unit. |
| ADR 213 decision 10 | Manual locks on a unit, a collection or a namespace. |
| ADR 103 decision 4 | Change freezes. They are the switch that stops applies. |

### The first slice, as this proposal reads it

A push to a review request starts a plan job for each matching unit
(ADR 267 decision 19). The server posts one commit status for the review
request and one summary comment, which it edits in place. Nothing applies.
The work items PR-1 to PR-3 below are this slice. Everything after PR-3 is
the rest of RFC 27.

### Questions and recommendations

1. **One change order for each review request.**
   - (a) The first plan of a review request makes a change order with the
     origin `review-request` (the connection, the repository and the
     number). Each later push attaches a new plan revision. Approvals of
     OpenDustpan go on the change order, and add to the approvals of the
     Git host (ADR 103 decision 3).
   - (b) Plan jobs only, with no change order. The approvals of the Git host
     are the only approvals.

   *Recommendation: (a).* A change order is the object that binds approvals
   to the digest of the plans (ADR 191), and that an apply after the merge
   can apply. (b) leaves the apply after the merge with no reviewed plan.
2. **Which commit a plan reads.**
   - (a) The test merge commit that GitHub makes for an open pull request
     (`merge_commit_sha`, the ref `refs/pull/<n>/merge`): the head merged
     into the current base. The job records the head and the test merge
     commit. When GitHub cannot make one (a conflict), the server plans
     nothing, and the status says "merge conflict".
   - (b) The head commit of the review request.

   Precedent: Atlantis `--checkout-strategy merge`, which its `undiverged`
   requirement needs. The GitHub merge queue tests the merged result.
   *Recommendation: (a).* The plan then reads the code that the default
   branch will hold after the merge, which question 3 needs.
3. **How the apply after the merge works.** *Owner decision.* The owner
   decided the default. The mechanism is open, because the code after the
   merge is a new commit.
   - (a) **Apply the reviewed plan when the tree is the same.** After the
     merge, the server reads the new head of the default branch. When its
     root tree ID is the root tree ID of the test merge commit that the
     approved revision planned, the change order applies its stored plans,
     on the new head. `job.CheckPlan` still refuses a plan whose state
     version is not current (ADR 102 decision 7). When the trees differ
     (the base moved and the merge brought other code), the server attaches
     a new revision on the new head, which needs approval again.
   - (b) **Plan again, approve again.** Each merge attaches a new revision
     on the merge commit. Its signatures never carry over, because the
     commit is an input of the digest (ADR 225 decision 12).
   - (c) **Plan again, carry the signatures over on the tree.** As (b), but
     ADR 225 decision 12 compares the root tree in place of the commit, and
     the part hashes of a plan in place of their bytes.

   Precedent: HCP Terraform and Spacelift plan again after the merge, and
   ask for a confirmation unless auto-apply is on. Pulumi Deployments runs
   `pulumi up` on a push to the branch. No product that we found applies
   the plan of the review request after the merge.

   Recommendation: (a). The apply is the change that people read and
   approved, with the same code, on the default branch. A tree ID of git is
   a hash of the whole content, so "the same tree" is exact. (b) asks every
   approver twice for each change. (c) changes an accepted ADR, and the
   plan bytes of OpenTofu hold a timestamp, so (c) needs a new digest of
   the resource changes. (a) needs an addition to ADR 191: an apply job of
   a change order can run on a commit other than the commit of its plan
   when the root trees are equal, and the job records both commits.

   A second check binds the code inside the plan. An OpenTofu plan file
   holds a snapshot of the configuration that it planned. Before the apply
   step, the launcher compares that snapshot with the files of the checkout
   of the commit, and fails the phase when one differs. So a plan file that
   changed on the runner after `tofu plan` cannot apply other code, also
   when its hash was recorded after the change. The e2e step of PR-6
   checks the format of the plan file of each OpenTofu version before the
   check lands. The Pulumi CLI has no such snapshot in its plan. Its apply
   runs the program of the checkout with `--plan`, which stops a change
   that the plan does not hold (ADR 102 decision 7).
4. **Who asks for the apply after the merge.** ADR 190 decision 10 says
   that apply is a request of a person, and that the store checks that the
   request names the digest of the current revision. The digest is the
   proof that the person asked for the plans that people approved. A merge
   on GitHub names no digest.
   - (a) A **direct merge** is the request, when the account that merged is
     linked to a principal that holds `unit:run-apply` (or
     `unit:run-destroy`) on each unit. The server supplies the digest of
     the approved revision, and only when the tree check of question 3 (a)
     passes. A direct merge means: the server reads the timeline of the
     pull request through the API, and finds no auto-merge event and no
     merge queue event. With auto-merge, `merged_by` is the person who
     turned it on. With a merge queue, the merge happens after later pushes
     or a move of the base. In both, that person may not see the
     revision that applies, so neither counts. The server reads each page of
     the timeline before it decides that no such event exists. A timeline
     that the server cannot read, also a read that stops before the last
     page, does not count. In each case that does not count, the
     change order waits in `approved`, the summary comment says who can
     apply, and a person asks through the API, the console or the comment
     command `dustpan apply` on the merged review request.
   - (b) A person always asks after the merge, with the digest, as ADR 190
     decision 10 says today.
   - (c) The namespace service principal applies on its own.

   *Owner decision.* Recommendation: (a). It changes ADR 190 decision 10:
   for a direct merge, the server supplies the digest. (b) needs no change
   of an ADR, and costs one more request after each merge. (c) is an apply
   that no person asked for, which ADR 267 decision 9 refuses for triggers.
   A field `reviewRequests.applyOnMerge` of the unit settings (RFC 12)
   turns (a) off, so that each apply needs a request after the merge.
5. **The approval state at the merge.** A merge that happens before the
   change order is `approved` does not apply. The change order keeps
   waiting for its approvals. When it becomes `approved`, the apply needs a
   request of a person (question 4, second branch). *Recommendation:* this
   rule, with no option. A merge never replaces an approval of OpenDustpan.
6. **Guard rails of the pre-merge apply.** The owner decided that the option
   exists. These guard rails are the recommendation. Each one stops a wrong
   apply alone.
   - **Off, and hard to turn on.** The field
     `reviewRequests.applyBeforeMerge` of the unit settings is `off` by
     default. A change to `on` needs `unit:manage`, a fresh authentication
     (ADR 227) and writes an audit entry. A namespace and the installation
     can set `forbidden`, as ADR 225 decision 12 does for the carry-over of
     signatures. The console and the docs page show the warning of the
     owner each time a person turns it on.
   - **Requirements at the dispatch of each apply phase**, read through the
     API at that moment (`reviewrequest.CheckApply`, a table-driven test):
     1. The review request is open, targets the ref of the code location,
        and its head repository is the base repository, compared by the
        numeric repository ID that the API answers. A fork never applies.
     2. The head is the head that the approved revision planned, and the
        current test merge commit has the root tree that the revision
        planned. So the base did not move in a way that changes the code
        (Atlantis `undiverged`).
     3. GitHub reports the pull request as mergeable: `mergeable_state` is
        `clean`, `unstable` or `has_hooks`. Each other value, also
        `unknown`, refuses. So required reviews, code owners and required
        checks of the Git host apply.
     4. The change order is `approved`, and the gates of `change.apply`
        pass. The fact document of `change.apply` holds
        `source.preMerge = true`, so a gate can deny or ask for more
        approvals for a pre-merge apply only.
     5. The principal is a person, linked to the account, with
        `unit:run-apply` (or `unit:run-destroy`) on each unit.
     6. No hold of another review request, no freeze and no manual lock
        covers a unit.
   - **A hold from the first pre-merge apply until the merge.** In the
     transaction that dispatches the first pre-merge apply phase, the
     server puts a manual lock (ADR 213 decision 10) on each unit of the
     change order, with the holder kind `review-request`. It blocks the
     OpenTofu lock and the start of a Pulumi update from a workstation, as
     each manual lock does, and the message names the review request. Other
     review requests still plan, and their summary says that the unit is
     held. The merge releases the hold.
   - **The merge after the apply.** When each unit applied, the server
     reads the test merge commit again. When its root tree is not the tree
     that the revision planned (the base moved after the apply), the server
     does not merge, keeps the hold, and writes the notification
     `review-request.apply-blocked`. Else it merges through the API with
     the method of `reviewRequests.mergeMethod` (RFC 12 question 8) and the
     head commit that it applied, so GitHub refuses a merge of another
     head. A failed merge keeps the hold and writes the same notification.
   - **A partial apply** (one unit failed) does not merge. The hold stays.
     The summary comment shows each unit.

   Precedent: Atlantis takes its lock at the plan and has `approved`,
   `mergeable` and `undiverged`. Digger has the same requirements and
   `auto_merge`. Spacelift tracks a pull request by a push policy. The
   advisory GHSA-5vwh-xc92-8h64 of Atlantis (a plan file not bound to its
   review request) is the reason why the apply reads the stored plan of the
   change order by its ID and hash, never a file at a path.
7. **A review request that closes with no merge after a pre-merge apply.**
   - (a) The hold stays. The unit shows the flag "applied code is not on
     the default branch", and the server writes the notification
     `review-request.hold-left-open`. A person releases the hold with
     `dustpan unlock` on the review request (the person who applied, or a
     holder of `unit:manage`) or through the API.
   - (b) The close releases the hold.

   Precedent: Atlantis releases its lock at the close, and its docs say that
   the main branch then no longer reflects the infrastructure.
   *Recommendation: (a).* A silent release hides that the infrastructure
   runs code that no branch holds.
8. **Comment commands.** ADR 271 decision 7 fixes the rules. The design:
   - **Grammar.** The first line of the comment is the whole command:
     `dustpan apply` or `dustpan unlock`, in ASCII, with single spaces and
     no argument. Any other text on the first line is not a command. A
     command applies to each unit of the current revision: a change order
     applies as a whole (ADR 190 decision 10). The parser is a fixed table,
     not a regular expression of the user.
   - **Only new comments.** The server acts on the event of a created
     comment only, never on an edit or a delete. It reads the comment
     through the API by its ID and acts only when the body, the author ID
     and the review request are the values of the event, and the comment
     shows no edit. The REST API of GitHub has no edit flag on an issue
     comment, so the check is `updated_at` equal to `created_at`. Spacelift
     also ignores edited comments.
   - **The account.** A link maps the numeric user ID of the Git host to a
     principal, through a link request as in ADR 146 decision 3. A login
     name is never the key, because a person can rename it. A bot account
     never runs a command.
   - **The plan that the person saw.** The command applies only when the
     summary comment did not change after the comment was written, and the
     head did not move since the revision. Else the server refuses and says
     to read the new summary.
   - **What each command does.**
     - `dustpan apply` on an open review request is a pre-merge apply, and
       needs the guard rails of question 6. With
       `reviewRequests.applyBeforeMerge` off, it refuses and names the
       setting.
     - `dustpan apply` on a merged review request is the request of the
       apply after the merge (question 4).
     - `dustpan unlock` releases the hold of this review request (question
       7).
   - **The answer.** A reaction on the comment (`eyes` at the start, then
     `+1` or `-1`), and one line in the summary comment. The server writes
     no new comment, so commands make no noise.
   - **The risk that ADR 271 decision 7 names.** A comment proves only that
     an account wrote a text. So the link decides the principal, the actions
     of the principal decide the apply, the server reads each value through
     the API, and a comment never approves.
9. **A third command, `dustpan plan`.** *Owner decision*, because ADR 271
   decision 7 names two commands. It plans the current head again, for
   example after an outage of a runner. Atlantis, Digger and env zero have
   a plan command. Recommendation: add it, with the rule below.

   A push starts its plan as the namespace service principal (ADR 267
   decision 14), and that plan of a branch that is not protected gets no
   decrypt (ADR 213 decision 2). A comment command runs as the linked
   principal (ADR 271 decision 7). When that principal holds
   `unit:read-secrets`, the first bullet of ADR 213 decision 2 would give
   the plan the secrets of the unit, while it runs code of a branch that
   nobody reviewed.
   - (a) `dustpan plan` starts the job as the namespace service principal,
     as a push does. This changes "runs as that principal" of ADR 271
     decision 7 for this command.
   - (b) A plan with the source kind `forge` never passes the first bullet
     of ADR 213 decision 2, whoever starts it. Only the second bullet (a
     commit of a branch in `protectedBranches`) can give it secrets.

   Recommendation: (b). It holds for each start of the source kind `forge`,
   also one that a later feature adds, and the job and the audit entry
   still name the person who asked. It narrows ADR 213 decision 2.
10. **Profile credentials in a plan of a review request.** *Owner
    decision.* RFC 5 opens each profile of the spec with the access `read`
    for a plan phase. The read role of a cloud is enough to copy data out,
    and the code of a branch that is not protected runs in the plan.
    - (a) A plan with the source kind `forge` of a branch that is not
      protected opens no profile. A plan that needs a credential fails with
      `job.ErrSecretsWithheld`, and the error names the setting of (c).
    - (b) It opens each profile with the access `read`, as any plan does.
    - (c) A namespace setting `reviewRequestPlans.profiles`: `none` (the
      default, which is (a)) or `read` (which is (b)). A change to `read`
      needs `namespace:manage`, a fresh authentication (ADR 227) and writes
      an audit entry. The fact document of `profile.open` holds the source
      kind and the protection of the branch, so a gate can narrow `read`
      to some profiles.

    Precedent: GitHub Actions gives secrets to a `pull_request` workflow of
    a branch in the same repository, and none to a fork. HCP Terraform runs
    no speculative plan for a fork, "to avoid executing malicious code or
    exposing sensitive information", and plans a branch of the same
    repository with the variables of the workspace. Atlantis plans with the
    credentials of its server, and its docs say that approvals of an apply
    do not protect a plan.

    Recommendation: (c), with the default `none`. This keeps the rule of
    ADR 213 decision 2 (no secrets for code that nobody reviewed) for the
    credentials of a profile too. The cost: with the default, a plan of a
    pull request of a unit that needs cloud credentials fails until a
    person sets `read` for the namespace. A team that sets `read` gets what
    GitHub Actions and HCP Terraform give to a branch of the same
    repository.
11. **The summary comment and the status after the first slice.**
    - One comment for each review request, edited in place, with a table of
      the units: the tool word ("plan" or "preview"), the counts of add,
      change and destroy, the state, and a link to the console. Units with
      no change collapse into one line. A comment stays under 60,000
      characters, below the limit of GitHub of 65,536. Above it, the table
      lists the units with changes first, and links to the change order
      for the rest.
    - The comment holds no plan text and no value. The readable plan stays
      behind `job:read-plans` in the console (ADR 102 decision 8).
    - One commit status for the plan (the first slice). With the pre-merge
      option on, no apply status: a required apply status would block the
      merge that the apply waits for (Atlantis, Digger and env zero each
      document this loop and a bypass of it).

    Precedent: env zero edits one comment on each commit. Spacelift and HCP
    Terraform offer one aggregated status.
    *Recommendation:* this design.
12. **A stale plan.** The summary marks a unit stale when the base branch
    moved and changed the test merge tree, or when another run wrote a final
    state version of the unit. The server does not plan again on a push to
    the base. The next push to the review request, a `dustpan plan`, or an
    apply request plans again. *Recommendation:* this rule. A push to a busy
    default branch would else start a plan of each open review request.
13. **Which parts need a runner.** Only plans and applies. Statuses,
    comments, holds, commands, merges and stale marks run in the server from
    webhooks and API reads.
14. **A temporary unit for each review request** (Pulumi review stacks).
    *Owner decision.* It depends on ephemeral units (RFC 43), which the
    roadmap does not put in Milestone 5. Recommendation: after Milestone 5,
    with RFC 43.
15. **A switch that stops applies.** A freeze (ADR 103 decision 4) and a
    manual lock on a namespace (ADR 213 decision 10) stop the jobs of one
    namespace. ADR 282 decision 7 leaves a switch for the whole
    installation with this RFC.
    - (a) A manual lock with the scope `installation`. An operator with a
      new action `installation:lock` puts it with a reason. While it holds,
      no phase that changes dispatches in any namespace, and no pre-merge
      apply starts. Plans still run. It blocks the lock of a tool from a
      workstation as each manual lock does. That check is on the hot paths
      of the OpenTofu lock and of the start of a Pulumi update, so the
      server reads the installation lock in the same query as the manual
      locks of the unit, its collections and its namespace, with no second
      round trip to the database. Each lock and each removal
      writes an audit entry of the installation and notifies the
      administrators of each namespace.
    - (b) No switch. An operator locks each namespace.

    Precedent: Atlantis has a global apply lock in its UI, which fails
    closed when its lock store cannot answer. GitLab has deploy freeze
    periods for a project.
    *Owner decision.* Recommendation: (a). An incident across namespaces
    then needs one request, not one for each namespace. It extends ADR 213
    decision 10 with the scope `installation`. The level `forbidden` of
    question 6 stays the switch for pre-merge applies only.

### Ports

- `pkg/reviewrequest` (new domain package, tool-neutral):
  - `ReviewRequest`: the connection, the repository ID, the number, the
    head, the test merge commit and its root tree, the change order, the
    summary comment ID, the state and the hold.
  - `Store`: `Get`, `Put` with a version, `ByChangeOrder`, with the fence
    and the audit entries in each transaction, and the suite
    `reviewrequesttest`.
  - `ParseCommand`, `CheckApply` and `Summary` (the rendering of the
    comment), each a pure function with a table-driven test.
- Additions to `forge.Reviews`, with `forgetest` cases first:
  `ReadReview` also answers the base, the head repository ID, the test
  merge commit, the mergeable state and the author ID. New methods:
  `SetStatus` and `MergeReview(repo, id, head, method)`.
- `forge.Comments` (new group, new capability `comment`): `Upsert(repo,
  review, marker, body)`, `ReadComment(repo, id)`, `React(repo, id,
  reaction)`.
- `forge.Files.Tree(repo, commit)`: the root tree ID of a commit.
- `ParseEvent` reads `pull_request`, `push` and `issue_comment`.
- The account links of ADR 146, extended to the accounts of a Git host
  (`chatops.Links` with a link kind, or a new `gitaccount.Links`. The
  slice decides by the shape of `chatops.Links`).

### End-to-end steps

The steps use `e2e/githubfake` (RFC 12), a test runner and the real CLIs.

1. A push to a pull request: a plan job for each matching OpenTofu
   workspace, one status and one summary comment (the first slice).
2. A second push: a new revision, the comment is edited, the older plan is
   superseded.
3. An approval in OpenDustpan, then a merge in the fake by a linked
   account: the stored plan applies on the new head of the default branch,
   and the summary shows the apply.
4. The base moves with a change to the module of the unit before the merge:
   the trees differ, and the server attaches a new revision that waits for
   approval.
5. A merge by an account that is not linked: the change order waits, and
   `dustpan apply` by a linked person on the merged pull request applies.
6. Pre-merge apply on: `dustpan apply` puts the hold (the real `tofu plan`
   on a workstation gets the lock message), applies, merges with the head
   commit, and releases the hold.
7. Pre-merge refusals, each with its message: the setting off, the level
   `forbidden`, an account with no link, a fork, a push between the comment
   and the dispatch, an edited comment, `mergeable_state` `blocked`.
8. A close with no merge after a pre-merge apply keeps the hold, and
   `dustpan unlock` releases it.
9. Steps 1, 3 and 6 for a Pulumi stack with the real Pulumi CLI. The hold
   refuses `pulumi up` from a workstation.
10. A merge through auto-merge, and a merge through the merge queue of the
    fake, by a linked account: neither applies, and the change order waits
    for a request.
11. `dustpan plan` by a linked person who holds `unit:read-secrets`, on a
    branch that is not protected: the plan gets no decrypt and opens no
    profile with the default of question 10. A plan that reads a secret
    fails with `job.ErrSecretsWithheld`.
12. An operator puts the installation lock: an approved apply in two
    namespaces waits with the reason `locked`, a plan still runs, and the
    removal lets both dispatch.

### Security tests (ADR 250)

| Step | Test with hostile input sent straight to the step |
| --- | --- |
| Check at the boundary | The parser gets 64 KiB, Unicode spaces, look-alike letters, `dustpan  apply`, `dustpan apply --target x`, a command on line 2 and a NUL byte. Only the two exact commands parse. An event with a bad signature or an unknown hook ID changes nothing. |
| Contain | A branch name, a title and a comment author with Markdown, HTML and `@` mentions reach the summary comment escaped. No value of the event reaches a step argument, a log line unescaped or a SQL text. |
| Limit | One command in flight for each review request. A delivery ID seen in the last 24 hours is dropped. The summary stays under 60,000 characters with 100 units. |
| Authorization at the point of use | `dustpan plan` by a principal with `unit:read-secrets` on a branch that is not protected. The job token gets no `unit:decrypt` and no unmasked state read. No profile opens with the default `none`. A start of the source kind `forge` with a forged starter that holds every action: the same. A merge with an auto-merge or a merge-queue event in the timeline, and a timeline read that fails: no apply request. The fake answers the comment through the API with another body, another author or an edit: refused. The event names one repository ID and the API another: refused. The linked principal lost `unit:run-apply` between the comment and the dispatch: refused at the dispatch. A fork whose head repository has the same name as the base (the advisory GHSA-6mjf-4m52-m5cq of Atlantis): refused by the numeric ID. |
| Fail closed | `mergeable_state` `unknown`, an API timeout, and a tree read that fails each refuse the apply, keep the hold, and write the audit entry and a notification. |

### Work items

M5-1 is the work item that builds the runner launcher routes, in progress
on 2026-10-30. An item that depends on M5-1 waits until it merges. Each
item that adds a migration is in the conflict group `store-migrations`. So
no two of them collide on the migration number or on the hashes of
`TestFrozenMigrations`.

| ID | Item | Size | Conflict group | Depends on |
| --- | --- | --- | --- | --- |
| PR-1 | GitHub adapter: `ReadReview` fields, `SetStatus`, `forge.Comments`, `Tree`, `ParseEvent` for the three events, forgetest cases (first slice) | L | forge-github | WB-7 |
| PR-2 | The webhook route `POST /webhooks/{hook ID}`, the dedupe of delivery IDs, the durable effect operations (first slice). This item owns the route. WB-10 adds the merge and close effects to it. | M | forge-events | PR-1, WB-8 |
| PR-3 | `review-request-push` triggers (RFC 24 slice 4): plan jobs, one status, one summary comment. The rule of question 9 (b) for each start of the source kind `forge`. No profile open for a branch that is not protected (question 10 (a), until the owner answers). E2e steps 1 and 11, docs page "Pull request automation" (first slice) | L | trigger-dispatch | PR-2, M5-1 |
| PR-4 | `pkg/reviewrequest`: model, store, conformance, SQL in both dialects | M | store-migrations | PR-3 |
| PR-5 | The change order of a review request, revisions for each push, the test merge commit, stale marks, the full summary, e2e step 2 | L | changeorder-wiring | PR-4 |
| PR-6 | The apply after the merge: the tree check and the check of the plan file snapshot. The request by a direct merge. A new revision when the trees differ. E2e steps 3, 4 and 10 | L | changeorder-wiring | PR-5, owner answers 3 and 4 |
| PR-7 | Links of Git host accounts to principals (ADR 146 link requests), with its migration | M | store-migrations | none |
| PR-8 | Comment commands: parser, intake, the reread through the API, `dustpan apply` on a merged review request, `dustpan unlock`, `dustpan plan` if the owner says yes, e2e step 5 | M | forge-events | PR-6, PR-7, WB-10 |
| PR-9 | Pre-merge apply: the setting, its levels, the fresh authentication, `CheckApply`, the hold and the merge. E2e steps 6 to 8. Docs page "Apply before merge" with the warning | L | changeorder-wiring | PR-8, WB-7 |
| PR-10 | The Pulumi steps of e2e step 9 | M | e2e-steps | PR-9 |
| PR-11 | The setting `reviewRequestPlans.profiles` (question 10), if the owner picks (c) | S | profile-providers | PR-3, PF-6 |
| PR-12 | The installation lock (question 15), with its migration, e2e step 12. The lock check reads it in the one query of the manual locks. A query-count test or a benchmark of the OpenTofu lock path shows no added query. | M | store-migrations | none |

## Outcome

None yet.
