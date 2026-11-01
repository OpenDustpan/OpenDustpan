---
rfc: 114
title: Review requests and change orders, one integration with the Git host
status: accepted
adr: 299
created: 2026-10-31
---

# RFC 114. Review requests and change orders, one integration with the Git host

Status: accepted (ADR 299). The project owner decided the parts under
"Decisions of the owner" on 31 October and 1 November 2026. ADR 299 records
them and takes the recommendation of each open question. This RFC amends the open parts of RFC 27 (questions 1, 4, 5,
6 and 11).

## Problem

A team that uses a Git host reviews a change in a review request. ADRs 190 to
198 give OpenDustpan a complete review product for a change: the change
order, with plan revisions, threads, draft reviews, reviews with four
verdicts, signatures bound to a digest, and an apply request. ADR 103
decision 3 and RFC 27 question 1 (a) say that OpenDustpan approvals "add to"
the approvals of the Git host, with no link between the two. So, for one
change from a review request, the design before this RFC has these faults:

- Each approver approves two times, in two products.
- A reviewer reads two lists of threads.
- "Request changes" in one place does not block the other.
- The two places have different rules for a new push.
- A close of the review request does not close the change order.
- A merge can happen before the change order is approved, so the default
  branch is ahead of the infrastructure.
- The checks of the repository are not an input of the change order, and the
  requirements of the change order do not gate the merge.

The project owner calls this state a mistake.

There are also two security gaps:

- **The author of a change order of a review request is not defined.** A push
  starts its plan as the namespace service principal (ADR 267 decision 14).
  If that principal attaches the revision, the person who pushed the code is
  not an author (ADR 190 decision 4), and can sign their own change.
- **A close of the review request does not close its change order.** It stays
  in review until the idle expiry of 30 days (ADR 190 decision 12).

RFC 13 (accepted) had the rule "One edit has one place for approval:
OpenDustpan or the forge, never both". ADR 103 decision 3 replaced it.

## How other products do it

Facts read on 31 October 2026 from the documentation of each product. Recheck
an exact quote before an ADR uses it.

- **Atlantis, Digger, Terrateam:** one place, the review request. Comment
  commands plan and apply, before the merge by default. Approvals are the
  approvals of the Git host. The Atlantis docs say that "approvals are not
  tied to a plan". Each product has a loop between a required apply check and
  the `mergeable` requirement, and a flag that bypasses it. Atlantis has an
  open request for an apply after the merge with about 136 votes.
- **HCP Terraform:** "HCP Terraform adds a link to the run in the pull request
  itself, along with an indicator of the run's status." It posts no comment.
  It runs a speculative plan for each pull request, but not for a pull request
  from a fork ("to avoid executing malicious code or exposing sensitive
  information"). After the merge, a new run plans again and waits for "Confirm
  & Apply" in its own UI, unless auto-apply is on. "HCP Terraform does not
  update the status checks on a pull request with the status of an associated
  apply." Its docs also say that a speculative plan might not show the result
  of the merge when the base branch moved.
- **Spacelift, env0, Scalr:** approval in a policy of the product. Users ask
  to approve on the Git host, and the vendors add bridges (a Spacelift push
  policy reads `approved` and `mergeable`).
- **Pulumi Deployments:** review stacks and comments on the review request,
  `pulumi up` after the merge, no approval of its own.
- **Argo CD and Flux:** Git is the record. The Flux OpenTofu controller
  approves a plan with a commit that names the plan ID.

## What the Git hosts allow

The facts that decide the design. "Unverified" means that the documentation
does not state it, and a test against the real Git host must settle it before
an ADR depends on it.

| Need | GitHub | GitLab Free | GitLab Premium, Ultimate | Gitea | Forgejo |
| --- | --- | --- | --- | --- | --- |
| A required status bound to a commit | Check run of the app, required by app ID | "Pipelines must succeed". That a status from the API counts is unverified | Same. Ultimate also has external status checks with `sha` | Status contexts (glob patterns) | Same as Gitea |
| Approvals block the merge | Yes, branch rules | No: approvals are optional | Yes, approval rules, code owners | Yes, official reviews | Yes |
| A review names its commit | `commit_id` | No commit in the listing. The approve call takes `sha` | Same | `commit_id` | `commit_id` |
| Dynamic required reviewers through the API | No | No | Merge request approval rules | No | No |
| Merge with a fixed head | `sha` (409 on a mismatch) | `sha` | `sha` | `head_commit_id` | `head_commit_id` |
| Merge queue or train | Merge queue (synthetic commit) | No | Merge trains (synthetic commit) | No | No |
| Bypass paths to detect | Bypass actors, admin override | Bot with "allowed to push" | Same, policy bypass lists | Admin override unless blocked | Admin force merge. `apply_to_admins` off by default |
| Resolve a thread through the API | GraphQL only | Yes | Yes | From 1.26 | No |
| User token for an action as the person | App user access token: Pull requests write. Contents write for merge. 8 h | OAuth with `api` (no narrower scope). 2 h | Same | OAuth2 scopes from 1.23 | OAuth2 with no scopes: full power of the user |
| Merge requirements | GraphQL `mergeStateStatus`, `reviewDecision`, check rollup with `isRequired`, `reviewThreads`. No single field. `mergeable` is null while a background job runs | `detailed_merge_status`. A read of a stale merge request starts a check, and a recheck "can be an expensive operation" | Same | `mergeable` is false for each cause alike. The server computes the list from the protection fields | Same |
| Webhook signature | HMAC-SHA256 | A plain secret header. Signing tokens (HMAC) from 19.1 | Same | HMAC-SHA256 | HMAC-SHA256 (header name unverified in the source) |
| Rate limit | Installation token up to 12,500 requests/h. A `304` does not count | GitLab.com 2,000 requests/min for each user. Self-managed off by default | Same | None documented | None documented |

## Decisions of the owner (31 October 2026)

### 1. Four modes, with no default

The mode is a required field in the API, the IaC providers, the CLI and the
setup wizard. The server asks for it when a Git host connection is first
attached to a scope (installation, namespace, collection or unit). The wizard
shows each mode with a one-line trade-off and no answer selected. The
effective mode of a unit shows in the API with the scope that set it. The
scope of the mode is decision 10.

| Mode | What it means | Trade-off in one line |
| --- | --- | --- |
| 1. Merged approvals | Approvals on the Git host and in OpenDustpan both count toward the requirements. The required status of OpenDustpan enforces the result. The approval rules and code owners of the Git host stay in force. | Two places to approve, joined by a status. **Not recommended.** |
| 2. OpenDustpan merges | The branch rules require the status of OpenDustpan. Optionally, only the OpenDustpan app can merge. The approval rules of the Git host are optional or mirrored. An apply after the merge is allowed. | One review place for the plan. The Git host keeps its code review. |
| 3. No review requests | Change orders start from commits. OpenDustpan reads the repository to show the code diff. | No review request flow for IaC. The review is in OpenDustpan only. |
| 4. No change orders | Like the Pulumi CLI with a backend: no change order and no approval. Deny rules and policies always apply. Runs are still recorded. Mode 4 refuses to turn on while an approval rule exists in its scope. Mode 4 can turn on an apply on merge (opt-in), and the apply runs only when the person who merged holds the apply grant. Mode 4 posts the plan comment of decision 3 and the `plan` status. | No approval step. Policies are the only gate. |

Notes on mode 1: GitHub has no API to add a required reviewer for one pull
request, so the combined rule lives in the OpenDustpan status, not in the
GitHub rules. GitLab Premium and Ultimate can add a merge request approval
rule through the API.

**The limits of mode 1** (decided by the PM for the owner, 31 October 2026).
Mode 1 keeps these limits, with no extra mechanism. Atlantis has the same
limits for its "approved" apply requirement, and documents them.

- A review on the Git host binds to a commit only. So it binds at L0 at most,
  also when the scope sets L1 or L2.
- A GitLab approval has no commit in its listing. The server cannot tell which
  commit an approval on GitLab covers.
- An agent that uses the personal token of a person submits a review that
  looks like the review of that person. The server cannot detect it.

The API and the wizard show these limits as text when a person chooses
mode 1. Mode 1 stays "not recommended". The approval floor of decision 5 still
applies in mode 1: a new delete, a new replace or an extra operation at the
apply needs a new approval with a fresh authentication, in OpenDustpan.

### 2. The review UI in OpenDustpan

- Three panes: IaC changes, comments from people, and comments from systems
  (bots and agents), plus one unified timeline. On a phone, the panes are tabs,
  with the timeline first.
- Non-IaC code diffs are read from the Git host and shown. The view is a
  lens, not a copy.
- Code comments live on the Git host. OpenDustpan writes them as the person.
  Plan comments live in OpenDustpan. The timeline merges both.
- The diff viewer: a structural plan diff by resource and attribute with the
  glyphs `+`, `~` and `-`, virtualized for 4,000 or more resources, and a
  word-level code diff. The target is the fastest viewer that we can build.

### 3. The comment on the Git host

The plan or preview goes to the review request as one comment, edited in
place, with a summary and a link to OpenDustpan (the shape that Atlantis and
Pulumi use). By default, the summary holds the counts and the resource
addresses with their kind of change, and never a value. The full diff in the
comment is opt-in for each scope. A redaction deletes the comment and posts it
again (decision 4). This differs from HCP Terraform, which posts a status with
a link and no comment.

### 4. Leaks

A value in a plan comment that should not be there is handled like any leak
from a misconfiguration:

- A comment command redacts the plan: the server deletes the diff from the
  Git host and says, in a comment, that it redacted it. A console button
  redacts it in the UI. On GitHub, an edit keeps each earlier version in the
  edit history. The GitHub docs do not say that a delete of the comment deletes
  that history: a test must confirm it. The server deletes the comment and
  posts a new comment with the redaction notice. The user docs give the manual
  step "Delete revision from history" for a full diff that a team posted before
  it opted out. On
  GitLab, Gitea and Forgejo, the behavior of the edit history is not verified,
  and the server does the same until a test shows that an edit is enough.
- A redaction invalidates the plan. An apply needs a new plan.
- The UI lists the resource addresses whose values leaked, so that the team
  rotates them.
- After a redaction, the comments of that review request hold the summary
  only, until a person turns the diff on again. Each later comment hides the
  redacted addresses.
- A redaction voids each signature on the revision, at every level, and shreds
  the item keys of the stored plan parts and of the job logs of that plan. The
  redact command can also hide resource addresses, because an address can hold
  data (for example `aws_iam_user.u["alice@example.com"]`).
- The history of the Git host that OpenDustpan cannot reach cannot be
  cleaned: e-mail notifications that the Git host already sent, chat
  integrations, and the edit history where a delete does not delete it. The
  docs say so.
- Who can run the redact command: a linked principal that can read the unit.
  A leak needs speed more than rank. Each run writes an audit entry. (Whether
  the command also needs `job:read-plans` is a question for the owner.)

### 5. Approval binding: three levels

A visible setting for each scope. The default is level 1.

| Level | An approval binds to | At the apply |
| --- | --- | --- |
| L0 | The code (the commit) | The apply plans again and applies. |
| L1 | The set of operations: each resource address and its kind of change | The apply plans again. The same set applies with no new approval. A new operation asks for approval again. |
| L2 | The exact plan: an OpenTofu saved plan. A Pulumi update plan only when the Pulumi CLI marks update plans stable | The stored plan applies, or the apply fails as stale. |

In each level, the tree of the merged commit must be the tree that was
planned, or the server plans again. At every level, each approval records the
set of operations of the revision that the approver saw, so that the approval
floor below has a set to compare with. Under the floor, L0 and L1 differ in
when an approval resets ("Reset at each level" below).

**The approval floor.** At every level, when the plan at the apply holds a new
`delete`, a new `replace` or any operation that the approved set does not
hold, the apply stops. It needs a new approval with a fresh authentication.
So no level applies a delete or a replace that no person approved.

**Refresh and drift.** A refresh keeps the default of each tool. A scope can
set plans with no refresh for OpenTofu (`-refresh=false`), so that changes
outside the code do not creep into a plan. Drift detection covers drift, and a
gate "the last drift check is older than N hours" can require it. The docs
explain the trade-off of each level and of the plans with no refresh.

**ADR 225 decision 12 is amended openly.** That decision voids each signature
at a new plan by default, and carries a signature over only identical digest
inputs, behind a setting. L1, the default, carries an approval over a new plan
with the same set of operations. This RFC amends ADR 225 decision 12 so that
the binding level decides when an approval carries over, with the approval
floor above. The binding level takes over the rules of that decision:

- a parent scope, also the installation, can forbid weaker levels below it.
- a change to a weaker level needs `namespace:manage-gates` (or
  `installation:manage-gates`) and a fresh authentication, and writes an audit
  entry `change` with the old and the new value.
- the effective level shows in the API with the scope that set it.

The setting `signatureCarryOver` exists in the code, but it has no users and
no stored data, and no tag shipped it. So the binding level replaces it, and
the setting is deleted with no conversion. The change
of the code follows in its own pull request.

### 6. Plan conflicts in place of unit locks

- Two review requests that touch the same unit can both merge while their
  code merges cleanly.
- OpenDustpan keeps its status red when the plans conflict: the changed
  resources and their dependency closure overlap, also across units through
  outputs. The console names the resources that collide, as far as the reader
  can see them. The status on the Git host names no resource: it says
  "Conflicts with another change" and links to the console.
- "No conflict" holds only when the check proves the two closures disjoint.
  Each other case is a conflict. These cases are never proven disjoint:
  provider changes, replacements, renames through `moved` blocks or aliases,
  data sources, deletes, and changes of a parent resource.
- The check has its own conformance tests, with a fixture for each case.

### 7. Security rules

- **S1. Authors.** Whoever opened the review request, authored a commit in it
  or pushed to it is an author, and cannot approve it on either side. A
  `Co-authored-by` trailer does not make an author. There is no exception for
  a person alone (`authorMayApprove` does not apply to a review request). A
  team that does not want OpenDustpan approvals turns them off with mode 4.
- **Reset.** OpenDustpan approvals reset only when a push changes the set of
  operations of the unit. The approvals of the Git host follow the rules of the
  Git host.
- **Webhooks.** The server verifies each signature, refuses a replay, and never
  decides on the payload alone. It reads the current state of the review
  request through the API before each gate decision. Each gate decision
  compares the head commit from a fresh read with the commit of the revision,
  and fails closed on a mismatch. With no polling, this rule covers a lost
  push event.
- **Identity links** use the immutable user ID of the Git host, never the
  login.
- **No user token of the Git host is stored on the backend, and no session is
  stored on the backend.** Console sessions are sealed cookies (ADR 297, in
  review in its own pull request). The user token of a Git host is in a sealed
  cookie of its own. Background work runs as the OpenDustpan app, and the
  record names the person for whom it runs.
- **Agents never approve**, on either side.
- **Who gets a plan with credentials.** A plan is code execution. A push to a
  review request starts a plan with credentials only when the opener and each
  pusher (by immutable user ID) are linked to a principal that holds
  `unit:run-plan` on the unit. Otherwise the plan waits for a trusted comment,
  as for a fork.
- **One rule for every start of a plan.** The rule above holds for each start,
  whatever starts it: a push, a base-branch push that plans again, "Plan
  again", `dustpan plan`, the catch-up read at start, and a merge queue commit.
  It checks the opener and each pusher of the head that the plan runs. When the
  check fails, the plan runs with no credentials, as for a fork. A
  `dustpan plan` or a "Plan again" by a trusted person is a trusted comment,
  and binds to the head that it names. Credentials never follow a trusted
  comment alone. Each path has its own test.
- **Bot accounts.** A bot account of the Git host can be linked to a service
  principal that holds a stored, visible and revocable grant of
  `unit:run-plan`. Its review requests then plan with credentials. An approval
  still needs a person.
  - Only a bot whose pushes no user of the repository controls can be linked:
    for example the Dependabot app or the Renovate app. The server identifies
    the bot by the app ID of the Git host, never by its login.
  - A self-hosted Renovate can run commands from its configuration
    (`postUpgradeTasks`). So a linked self-hosted Renovate must have
    `postUpgradeTasks` off, or read its configuration only from the default
    branch. The server cannot check this. The link form states the rule, and
    the person who links the bot confirms it.
  - The server refuses to link `github-actions[bot]` or any bot that pushes on
    the request of a user (a workflow, a comment command, a chat bot). The
    error says why: "This bot pushes for the users who start it, so its pushes
    cannot plan with credentials."
  - For each push by a bot, the credential rule checks the person behind it with
    the logic of S1. When the Git host cannot tell who is behind the push, the
    plan runs with no credentials.
  - A conformance fixture covers a workflow that a contributor changes so that
    `github-actions[bot]` pushes their code: the plan runs with no
    credentials. (The benchmarks: Atlantis and Spacelift give bots their
  own identity, and GitHub Actions gives Dependabot runs fewer secrets.)
- **"Plan with credentials as me".** For a review request from the same
  repository, never from a fork, a trusted person with `unit:run-plan` can
  choose this action for the current head. The plan binds to that head, writes
  an audit entry, and a new push voids it. The person becomes an author under
  S1, and the button says so before the press.
- **The docs give one row for each kind of opener:** a linked person, a linked
  bot, an unlinked account, and a fork.
- **A plan with no credentials can never be approved.** It cannot refresh or
  read data sources, so its set of operations means nothing. To get a plan
  with credentials, a linked person with `unit:run-plan` pushes the commits to
  a branch of the repository. That person becomes a pusher, and so an author
  (S1), and another person approves. The docs say so.
- **Forks.** A review request from a fork gets a plan only after a trusted
  person comments. The trusted comment names the head commit, and binds to
  it. A new push voids it. A fork plan gets no provider credentials, no state,
  no secret outputs and no outbound network.

### 8. Failures

- When the Git host says "not mergeable" or does not answer, the command
  fails with the answer of the Git host (as Atlantis does).
- **Webhooks, with no polling.** Three recovery paths:
  - a "Plan again" button in the console and the comment command
    `dustpan plan`,
  - one catch-up read when the server starts,
  - manual redeliveries: the server decides from the state of the effect, not
    from the delivery ID alone. A redelivery of a delivery whose effect did
    not succeed runs once. A redelivery of a delivery whose effect succeeded
    changes nothing. (That GitHub keeps the same delivery ID on a redelivery is
    unverified.)

  Federation keeps its poll (ADR 271 decision 9), as the one listed
  exception.
- **A received comment command** gets a reaction at once, with an emoji that
  the installation can set. The default is "eyes".
- **A merge starts an apply** only when the person who merged is linked and
  holds `unit:run-apply` (`unit:run-destroy` for a destroy) on each unit.
  Otherwise the change order waits for a person with the grant to ask for the
  apply.
- **Partial applies.** No blocking and no rollback. The record says "N of M
  applied", truthfully. A one-click retry of the failed units from the same
  commit, when permissions and configuration allow it (recommended on). Under
  L1, the retry needs no new approval when the operations are the same. After
  that, a team fixes forward or reverts, and each is a new review request.

### 9. Marks of Git hosts

The console shows a mark of a Git host only where it names the integration.

- **GitHub:** the GitHub mark, unchanged, in black or white, smaller than the
  OpenDustpan mark. The GitHub logo policy allows the mark to link to GitHub
  and to show an integration.
- **Forgejo:** the Forgejo logo, under its license CC BY-SA 4.0, with the
  attribution.
- **GitLab and Gitea:** the name in text, with a generic "opens another site"
  icon. The GitLab trademark guidelines generally refuse its logo in the UI of
  an integration. We found no logo policy for Gitea.
- The console never uses the colors of a Git host.

### 10. The scope of the mode

Decided by the project owner on 1 November 2026.

- **A unit setting with a cascade.** The review mode is a setting of a unit,
  with the cascade installation, namespace, collection, unit. A level can
  force a value, and a lower level cannot override a forced value. The API
  shows the effective value and its source, as the settings resource does.
- **The strictest mode for a review request.** A review request that touches
  units with different modes uses the strictest mode of those units. The
  review request says which unit set it.
- **Branch-wide settings of the Git host.** Mode 1 and mode 2 can need a
  setting of the Git host that holds for the whole branch: the required
  status, or "only OpenDustpan merges". Before the server turns such a setting
  on, it lists each unit on that repository branch that the setting affects.
  It refuses to turn the mode on until the effective mode of each affected
  unit allows it, or a forced value above them settles it.

This replaces the earlier options of one mode for each repository and branch,
and of the code location as the scope of the mode.

## Design that follows from the decisions

This section proposes the details. Each part can change in review.

### The parts that each mode with change orders uses (modes 1 to 3)

- **One change order for each review request** (modes 1 and 2), or for each
  commit range (mode 3), with one revision for each push. The change order is
  the record that binds each approval to a revision, at the binding level of
  decision 5, and holds the audit trail. In the UI of a review-request change,
  the label is the label of the Git host ("Pull request #123", "Merge request
  !45"), and the change order ID shows under "Provenance".
- **Two statuses** on the head commit: `plan` and `approvals`. `approvals` is
  success only when the change order of that commit is approved and no plan
  conflicts (decision 6). On a merge queue or merge train commit (GitHub
  `merge_group`, a GitLab merge train), the server plans that commit and
  posts success only when the operations of each unit in it match the
  approval at the level of the scope. A root tree check alone would fail each
  entry after the first one, because the queue commit holds the entries ahead
  of it. Each queue commit needs a plan with credentials, so a queue needs one
  runner slot for each entry that it tests at the same time. The docs give this
  sizing. When no runner is free, the status stays pending and says "Waiting
  for a runner (N queued)". A queue that times out the status drops the entry,
  and the message says why.
- **No loop.** The server reads the rules of the Git host without its own
  statuses. So "the Git host rules are met" never waits for "OpenDustpan
  approvals", and the loop between a required apply check and `mergeable`
  that Atlantis, Digger and env0 document does not happen.
- **The merge requirements of the Git host** show in the change order, in the
  terms of the Git host: "GitHub needs: 1 approving review from code owners,
  check `ci/test` (failed), 2 open conversations. Read 2 min ago." On Gitea
  and Forgejo, the server computes the list, and the console says so.

### What each mode sets

| Part | Mode 1 | Mode 2 | Mode 3 | Mode 4 |
| --- | --- | --- | --- | --- |
| Change order | For each review request | For each review request | For each commit range | None |
| Approvals that count | Git host reviews of linked person accounts and console approvals | Console approvals. Optional mirror to the Git host as the person | Console approvals | None (policies only) |
| Git host approval rules | In force | Optional or mirrored | Not used | Not used |
| `approvals` status required | Yes | Yes | Not applicable | No. The `plan` status and the plan comment |
| Who merges | People on the Git host | OpenDustpan, or people, as the scope sets | No review request | People |
| Apply | After the merge | After the merge | At the apply request | As the tool runs today, or on merge when the scope turns it on |
| Order of strictness (decision 10) | Third | Second | First (strictest) | Fourth |

### Mode 2: "only the OpenDustpan app merges", with no bypass

The merge goes through the normal merge API with the head of the approved
revision. The app is never a bypass actor. Where the Git host can restrict who
merges, the scope can name the app as the only merger: GitHub "restrict who
can push" on the branch (organization repositories), GitLab "allowed to
merge" set to the bot only, Gitea and Forgejo the merge allowlist. The server
reads the rules and refuses this option when the app is on a bypass list or
when the Git host cannot restrict the merger. The console says why.

On GitHub, "restrict who can push" names the app as a push actor, so the app
can push to the branch. Before each merge, the server reads the branch rules
again and refuses to merge when they no longer require a review request and
its status. The server pushes to the branch only through the merge API. A
compromise of the server could push with the app identity. The docs say so,
and the audit log of the Git host is the second record.

### Mode 3: change orders from commits

A push to a branch that no review request uses starts a change order. The
server reads the commits, the diff and the files through the API of the Git
host to show the code diff. After the approval and the apply request, the
server merges the branch through the API (a review request as a carrier where
the branch rules need one). Non-IaC files in the same repository keep their
own flow on the Git host.

The wizard shows mode 3 as off, with the reason, when the branch rules require
approving reviews on the Git host: the carrier review request could never
merge.

### Comments and the timeline

- Code comments: the Git host is the only store. The console reads them
  (event-driven) and writes them as the person with the token cookie of decision 7.
- Plan comments (on a resource change): OpenDustpan is the only store (ADRs
  193 and 198, with sealing and redaction).
- The timeline merges both, with the source of each item. The pane "comments
  from systems" holds bots, apps and agents of both sources.
- One blocking rule over both sources: an unresolved thread of an eligible
  approver or of an agent, a "request changes" review of an eligible approver,
  and an open blocking plan thread. A thread on the Git host that an author
  resolved still blocks: the server reads `resolvedBy`.
- Forgejo has no resolve API: the console shows the state and the resolve
  control is off with the reason.
- On a desktop, the IaC pane has the focus first. In "comments from systems",
  a filter that is off by default hides CI bots (coverage, dependency bots).
  On a phone, the timeline is the first tab (decision 2), and it starts with
  the items that need the reader (the verdict and the blocking threads).
- In mode 1, or with an approval mirror, an approval needs a session on the
  Git host. The console says so before the person approves on a phone.

### The setup wizard

The mode stays required, with no default (decision 1). The wizard asks it in
the words of the Git host:

1. "Must a second person approve infrastructure changes?" No: mode 4.
2. If yes: "Where do they approve? On GitHub, and OpenDustpan enforces it
   (mode 1). In OpenDustpan, and OpenDustpan merges (mode 2). In OpenDustpan,
   with no pull requests (mode 3)."

- Each answer shows one typical team ("one person", "a team that reviews on
  GitHub", "an audited platform team").
- The wizard lists mode 2, mode 3 and mode 4 before mode 1, which stays marked
  "Not recommended". The order is not part of the decision.
- The wizard says: "You can change this later. Open changes keep their mode."
- Before the person confirms, the server reads the branch rules and shows
  what will block: a merge queue, required reviews on the Git host for mode 3,
  a status that GitLab may not enforce, and the effect of an outage of the
  server on all merges of the repository (below).
- A scope with no Git host connection has no mode. The field is required only
  when a connection is attached to the scope.
- For mode 4 with apply on merge, the wizard asks the person to link their Git
  host account in the same flow, because a merge by an account that is not
  linked waits for a person with the apply grant.

### The order of strictness, and the errors of decision 10

- For decision 10, the order of the modes from the strictest is: mode 3, mode
  2, mode 1, mode 4 (a decision of the PM for the owner, 1 November 2026). The
  reason: where rules overlap, the most restrictive rule wins, as with an
  explicit deny in AWS IAM and the layers of GitHub rulesets. Mode 3 has no review request flow of its own. So a review
  request that touches a unit in mode 3 follows the rules of mode 2 (the
  review in OpenDustpan, and OpenDustpan merges), and the review request says
  so.
- The refusal of a branch-wide setting names each affected unit, its effective
  mode and its source, the grant that can change the mode of each scope
  (`namespace:manage-gates`) and the people who hold it.
- The wizard shows the same list before the person confirms.

### Who may set a mode, and mode 4

- A change of mode needs `namespace:manage-gates` (or
  `installation:manage-gates`), writes an audit entry `change` with the old
  and the new value, and needs a fresh authentication when the new mode asks
  for fewer approvals.
- A level can force a mode below it (decision 10), and a parent scope can
  forbid modes below it, as with the binding level.
- Mode 4 refuses to turn on while any requirement source holds at the scope or
  at a wider scope: an approval rule that a gate names, an approval default,
  or an ownership rule in `enforce` mode. A later save of such a source on a
  scope that has mode 4 below it fails and names the scopes.

### Plan conflicts in detail

- **Direction.** The first change order that is approved (or that merges)
  keeps a green status. The server decides "first" in one transaction, so two
  approvals at the same time cannot both stay green. A later change order that
  conflicts with it shows red, with an actionable message. So two review
  requests never block each other.
- **The message.** When the other review request is in the same repository and
  its readers can read this one, the status says: "Conflicts with Pull request
  #122. Merge #122 first, or ask an approver to accept the overlap. This plan
  runs again when #122 merges." Else it says: "Conflicts with another change.
  Open OpenDustpan to see it, or ask an approver to accept the overlap." The
  status never names a resource (decision 6).
- **Plan again.** When a conflicting review request merges, the base-branch
  push event plans each other open review request of the unit again.
- **Accept the overlap.** An eligible approver can accept a reported overlap
  for one revision, with a reason. The action is on the record, and a new
  revision voids it. An overlap with a reader in another namespace can only be
  accepted by a person with `unit:read` on that reader.
- **A reader in another namespace.** When the check cannot see a reader of an
  output, the message says who can clear it: "A unit in namespace `payments`
  reads this output. A person with `unit:read` there must review it."
- **After an apply,** the server checks again each open change order of the
  unit.
- The conformance fixtures also cover inline rules against separate rule
  resources, `for_each` over a data source, `terraform_remote_state` of an
  unmanaged unit, Pulumi dynamic providers, and drift.

### Authors in detail (S1)

- The authors are the opener, each pusher, and the requester of each agent or
  write-back job behind a push (the person who started the job). The server
  takes a pusher from the event sender and confirms it with a read through
  the API, by immutable user ID.
- The author and committer fields of a commit are strings that anyone can
  write. They add an author only where the Git host verified the account. A
  `Co-authored-by` trailer never makes an author. So a commit cannot name an
  approver as an author to block the approval, and a reviewer whose suggested
  change the author applied can still approve.
- A bot, an app, the "Update branch" button of the Git host or a merge queue
  that pushes does not hide the person behind it: the server records the
  person who started the action where the Git host tells it, and fails closed
  where it cannot tell.
- While the opener or any pusher is not linked, no approval counts in modes 1
  and 2. The console names the account that is not linked.
- The server never relies on the Git host to reset its own approvals.
- An approval by a principal who becomes an author (for example a reviewer who
  then pushes a fix) is void at once, at every level.
- In mode 1, a review on the Git host counts only while the Git host still
  shows it as valid (not dismissed, not stale by the rules of the Git host),
  and only while the reset rule of the level keeps it, from a fresh read.
- Conformance fixtures cover each bot path.

### Reset at each level

Decided by the project owner on 1 November 2026: each level resets on what it
binds to. The owner's earlier rule ("reset only when a push changes the set of
operations of the unit") is the rule of L1, the default.

| Level | A push resets the approvals when |
| --- | --- |
| L0 | It makes a new commit of the unit (L0 binds the code) |
| L1 | It changes the set of operations of the unit |
| L2 | It changes the plan digest (L2 binds the exact plan) |

An approval by a principal who later becomes an author is void at once, at
every level (S1). At L0, the approval floor still compares the plan at the
apply with the recorded set of operations, and L0 still resets on a new
commit.

### Reviews on the Git host that do not count

When the server sees a review on the Git host that does not count, it names
it in the summary of `approvals` and in the plan comment: "@alice approved on
GitHub. This approval does not count: approve in OpenDustpan (link)", or
"... the account is not linked: link it (link)". The change order shows
"GitHub needs" and "OpenDustpan needs" side by side.

### Merges and the order of applies

- After a merge, an apply runs only the latest commit of the default branch
  that changes the unit. The server supersedes an older commit and records
  why.
- **The approved set at the apply** is the union of the approved sets of each
  change order merged into the unit since its last apply. The apply record
  names each change order. Each superseded review request gets "Applied with
  #123" in its comment and its `apply` status. The approval floor still stops
  any operation that no merged change order approved. So two quick merges that
  do not conflict apply together with no second approval.
- The union never holds the set of a change order that was not approved. A unit
  marked `merged-unapproved` applies nothing until a change order is approved
  whose plan starts from the last applied commit and holds the bypassed commit.
  A conformance fixture covers this case: a bypassed merge changes an IAM
  policy to `"Action": "*"`, a later approved change order of the same unit
  holds other operations only, and the apply stops.
- A plan after the merge that needs a new approval puts the change order in
  the visible state "merged, not applied", with an alert.
- **The person who merged** (decision 8): for a direct merge on the Git host,
  the account that merged. For a merge queue or a merge train, the account that
  put the change in the queue. In mode 2, when the app merges, the person who
  asked for the merge in the console. The server reads that account through the
  API, by immutable user ID.
- **A merge with no approval.** A merge that passed around the gate (an admin
  override, a bypass actor, a status posted by another account) puts the
  change order in `merged-unapproved`, starts no apply, writes an audit entry
  and marks the unit. The next change order of that unit shows the code diff
  from the last applied commit, not from the base of its own review request.
  So its approvers see the code that bypassed the gate.
- On GitLab, Gitea and Forgejo, a commit status matches by name, and a CI job
  token can post one. The server reads the creator of each status of its own
  name. A status that another account posted fails the gate, and the console
  says that the gate on that Git host is advisory.

### The apply result on the Git host

- The server posts an `apply` status on the merge commit. It is not required.
- It edits the plan comment: "Applied 3 of 5 units. 2 failed: `prod-db`,
  `prod-dns`. Retry: (link)". The notification holds the same link.
- After a partial apply, each failed unit shows a banner, and the plan comment
  of each other open review request of that unit says: "This plan includes
  changes from #128, which did not apply."

### Retry of a partial apply

- A person in a session with `unit:run-apply` (`unit:run-destroy` for a
  destroy) on each unit asks for the retry. The app, a service principal and
  an agent never retry.
- A retry is allowed only while its commit is still the latest commit that
  changes the unit. Else the retry is off with the reason ("`prod-db` changed
  in Pull request #130 after this commit. Retry is off. Fix forward.").
- "The same operations" means each operation of the retry plan was in the
  approved set. A subset is the same. A new operation is not: for example,
  OpenTofu taints a failed create, so the retry plan shows `replace`, which is
  a new operation and needs an approval. The console shows who can approve,
  and links the break-glass path (ADR 142).

### A status for review requests with no unit

In each mode where `approvals` is required, it applies to each review request
of the branch, because no Git host scopes a required status by path. The
server posts `approvals` = success "no OpenDustpan unit changed" only when no
unit reads a changed file.

- **Declared paths.** Each unit has visible, declared paths: its directory and
  any extra paths (the benchmarks are Atlantis `when_modified` and the
  Spacelift project root with additional paths).
- **Declared paths only add.** The server computes the file closure of each
  unit from its module sources and imports. The declared paths add files to
  that closure and never take files out of it. A computed file outside the
  declared paths shows as a warning on the unit and counts as changed.
- **Unknown closure.** A unit whose closure the server cannot compute keeps the
  status pending, also with declared paths. A person with `unit:manage`
  can record an acceptance with a reason and a fresh authentication. The
  acceptance writes an audit entry, and the unit then shows "closure not
  proven". Each `approvals` success that rests on the acceptance also shows
  "closure not proven". The acceptance is recorded one
  time for each unit. It stays until the closure becomes provable or the
  declared paths change, and each change of the declared paths voids it. After
  the acceptance, the declared paths decide "no OpenDustpan unit changed" for
  that unit. The status names the unit and
  the grant. The wizard shows the count of such units before the person
  confirms.
- Each change of the declared paths writes an audit entry `change` with the old
  and the new value. A change of `modules/network/`, a lock file, a symlink or the CI
file that runs the units is in the closure of the units that use it. While the
server is down, the merges of the repository wait (fail closed). The wizard
says so in each mode.

### GitLab

- Until both tests of open question 4 pass, the wizard shows modes 1 and 2 on
  GitLab with a warning that names both risks: "GitLab may not block a merge on
  this status. A success of this status may also let a merge pass while your
  CI pipeline failed."
- Where the API allows it, the server posts its status on the CI pipeline of
  the merge request (the `pipeline_id` parameter of the commit status API.
  Unverified), not on a separate external pipeline.
- Mode 1 on GitLab Premium and Ultimate creates the merge request approval
  rule through the API.
- A GitLab user token expires after 2 hours, which is shorter than a console
  session. The console shows the expiry, so that the controls do not go off
  in the middle of a review with no warning.

### The plan comment: size and signal

- A plan of 4,000 resources does not fit in one GitHub comment (65,536
  characters). The comment then holds the summary, the counts and "N more in
  OpenDustpan", and the server never splits it into many comments.
- An edit does not send a notification on GitHub. The change of the status
  carries the signal of a new plan.

### Comment commands in each mode

The server reacts to each received comment command at once (the emoji of
decision 8), and answers it with a reply comment, never with silence. For example, `dustpan apply` in mode 2 gets "Not used in mode 2. The
merge applies." The commands of ADR 271 decision 7 (`dustpan apply`,
`dustpan unlock`), `dustpan plan` and the redact command have a row for each
mode in the docs.

### Webhook trust and identity

- Events have no order. Each effect reads the state fresh.
- GitLab without signing tokens sends a plain secret header. The server
  compares it in constant time, and asks for signing tokens where the version
  of GitLab has them.
- A user ID is unique for one Git host connection, not across Git hosts. A
  link is made only by an OAuth proof, never by a typed login. Two
  connections with the same numeric user ID are two people.

### Evidence for an auditor

The view of the Git host is a lens, not a copy. So each gate decision record
stores the facts that counted: the review ID on the Git host, the immutable
user ID, the commit, the verdict and the read time, with no comment text. An
auditor keeps the evidence when a comment changes or the repository is
deleted.

An apply at L1 records "N attributes differ from the approved plan" and links
both plans, so that a reader sees the values that nobody read.

### Audit entries

| Kind | Holds |
| --- | --- |
| Review counted or not counted | The Git host review ID, the user ID, the commit, the reason |
| Author set | Each author and the source of each (opener, pusher, job requester, verified commit author) |
| Conflict decision | The two change orders, proven disjoint or not, an accepted overlap with its reason |
| Status post | The status, the commit, the result |
| Comment command | The command, the account, the result |
| Use of a user token | The person, the action, the Git host object ID |
| Mode or binding level change | The scope, the old and the new value |
| Bypass detected | The merge commit, the merger, what the gate missed |

A person can ask why an approval counted, and get the record.

### L2 on a Pulumi scope

Until the Pulumi CLI marks update plans stable, a save of L2 on a scope that
holds a Pulumi unit fails, and the error gives the reason. The server never
falls back to L1.

### Pulumi users

Review stacks (one stack for each review request) and push to deploy are out
of the scope of this RFC. The docs say what is missing for users of Pulumi
Deployments.

### The user token in a sealed cookie

ADR 297 (in review in its own pull request) makes console sessions sealed
cookies, with no session stored on the server. The user token of a Git host
follows it. If ADR 297 changes before it is accepted, these rules stay: no
token on the server, use only in the person's request, no refresh, and an end
at sign-out.

- When a person authorizes the OpenDustpan app on a Git host in a session, the
  server seals the user token into a cookie of its own, one for each Git host
  connection. The cookie has the attributes `__Host-`, `HttpOnly`, `Secure` and
  `SameSite=Strict`, as the session cookies of ADR 297 have (only the cookie of
  a sign-in request is `SameSite=Lax` there), and a `Path` limited to the routes that use the token (on
  Forgejo the token has the full power of the user). A bump of the session
  generation ends each token cookie before its expiry, so a stolen cookie stops
  working at the next sign-out or change of the session. The cookie holds the token, the connection ID, the Git host user
  ID, the sign-in ID, the session generation and the expiry.
- The server never writes the token to the database, a log, an audit entry, a
  backup, a runner or an error. It discards the refresh token of the Git host
  at once, and never refreshes the user token. When the token expires, the
  person authorizes again.
- The token is used only inside the HTTP request of that person's action:
  write a code comment, mirror an approval (mode 2), approve on the Git host
  (mode 1), merge as the person. The access cookie of the same request must
  have the same sign-in ID and generation.
- At each use, the server checks that the Git host user ID of the cookie is the
  account linked to the person.
- **The OAuth flow.** The callback of the Git host is a cross-site navigation,
  so a `SameSite=Strict` cookie does not come with it. The server keeps the
  state of the flow in a `__Host-` cookie with `SameSite=Lax`, as the
  sign-in request cookie of ADR 297 does. The callback page checks that state,
  then sends a same-site post-back that carries the Strict session cookies.
  Only that post-back binds the token into its cookie. A test covers each case
  of `SameSite` (the callback with no Strict cookie, the post-back with them,
  and a post-back from another site, which fails).
- **The first click from a plan comment link** is also a cross-site navigation.
  It lands on a page that needs no Strict cookie to show the sign-in prompt.
  The page then loads the change with a same-site request.
- Two layers of authorization: OpenDustpan checks its own action first, and
  the Git host checks the rights of the person.
- With no token cookie, each control that needs one is off with the reason
  ("Sign in to GitHub from here to comment as yourself"), never hidden. The
  console shows the expiry of the token (2 hours on GitLab, 8 hours on
  GitHub).
- On Forgejo, where an OAuth2 token has the full power of the user, the
  console says so before the person authorizes it.

### Cost of reads on the Git host

The reads of merge state cost the Git host. A GitHub read can start a
background job and answer `null`. A GitLab read of a stale merge request starts
a mergeability check, which the owner's operations experience shows can harm
an instance.

1. Events start a refresh. A page view reads the stored snapshot only.
2. One refresh in flight for each review request, with a quiet window of 10 s. Events in the window join it.
3. The snapshot stores its read time, and the console shows it ("read 2 min
   ago").
4. Fresh reads only at a gate decision, and at an explicit refresh (one for
   each review request in 30 s, ten for each person in a minute).
5. Never a forced recheck (GitLab `with_merge_status_recheck`), except when a
   person asks, at most one time in 5 minutes for each merge request, with an
   audit entry. Never the list endpoint for merge state.
6. `null`, `UNKNOWN`, `checking` and `unchecked` are "unknown" at a gate, and a
   gate fails closed on "unknown".
7. A request budget for each connection, at most half of the published limit. On a self-managed Git host with no limit, the budget is the only protection.
8. A circuit breaker for each connection, which honors `Retry-After`. While it
   is open, gates fail closed and the console shows its state.
9. Conditional requests on GitHub, whose `304` answers do not count.

### Off states

A feature that cannot work, because of the Git host, its tier, a missing
session token or a parent setting, shows as off with the reason
(`aria-disabled` and the text), never hidden. The API answers the same
reasons.

## Security (defense in depth)

| Step | Test with hostile input sent straight to the step |
| --- | --- |
| Check at the boundary | A webhook with a bad signature has no effect. A replay of a delivery whose effect succeeded has no effect, and a replay of a delivery whose effect failed runs once. An event that says "approved" while the API answers "commented" has no effect. A review on an older commit has no effect. A check of the same name from another app has no effect. A console action with an old digest or head has no effect. |
| Contain | The immutable user ID, never a login. A renamed account that takes the login of a linked person: no effect. The session token never reaches a log, an audit entry, a store or a runner. A comment from the Git host reaches the UI escaped. |
| Limit | The smallest token permission. The token only in the person's request and only for the session. A fork plan gets no provider credentials, no state, no secret outputs and no outbound network. A plan of a push by a person with no link gets the same. Each has its own test. A trusted comment on head `abc` and a push of `def` before the plan starts: no plan of `def`. The request budget and the circuit breaker. |
| Authorize at use | OpenDustpan checks its action, then the Git host checks the person. At the approval and again at the apply. An author never counts, also an author found through a bot push or an agent job. A resolve by an author does not unblock. After a push, an approval on the older commit is void as the level says. At L0, each new commit voids it. At L1, a new set of operations voids it. At L2, a new plan digest voids it. Each case has its own test. An approval by a principal who later pushes is void at once, at every level. A plan that starts again for a review request of an unlinked pusher runs with no credentials. A status of the name `approvals` that another account posted fails the gate. |
| Fail closed | A timeout, `null` or `checking`, an expired token, an open breaker, a Git host that says "not mergeable": no approval, no apply, and an audit entry. |

## What this changes in accepted decisions

| ADR or RFC | Effect |
| --- | --- |
| ADR 103 decision 3 ("approvals add to the forge") | Replaced by the four modes |
| ADR 190 decision 4 (authors) | S1 |
| ADR 190 decision 10 (apply is a request of a person) | Modes 1 and 2, and mode 4 with apply on merge: the merge is the request. It counts only when the person who merged is linked and holds the apply grant |
| ADR 190 decision 12 | A close of the review request closes the change order |
| ADR 191 decisions 4 to 6 (the digest binds the exact plan. A new plan voids each signature) | The three binding levels. L2 keeps the digest of today |
| ADR 192, ADR 193 | Plan comments and verdicts stay in OpenDustpan. Code comments live on the Git host |
| ADR 102 decision 8 and RFC 27 question 11 (no plan value on the Git host) | By default the plan comment holds counts and addresses, never a value. The full diff is opt-in. Leaks are handled by decision 4 |
| RFC 27 question 6 (a hold from the first pre-merge apply) | Plan conflicts replace unit locks for review requests. See open question 3 |
| ADR 271 decision 4 ("each consumer that must not miss an event keeps a poll") | Webhooks for review requests, with "Plan again", one catch-up read at start and manual redeliveries |
| ADR 225 decision 7 ("an approver is a person in a session") | Mode 1 counts Git host reviews of linked person accounts |
| ADR 190 decision 9 (an apply job applies the stored plan of its plan job) | L0 and L1 plan again at the apply, under the approval floor of decision 5 |
| ADR 190 decision 14 (an agent never creates a change order) | A review request that an agent opens creates a change order. The agent is not an author of record. The person who asked the agent is the author |
| ADR 191 decision 5 (a person never signs a plan that they did not see) | L0 and L1 apply a new plan with the same set of operations. A new delete, replace or other operation needs a new approval (the approval floor) |
| ADR 225 decision 5 (fresh authentication for each destroy) | Kept: the approval floor asks for a fresh authentication, also in mode 1. The limits of a review on the Git host in mode 1 are stated consequences of mode 1 |
| ADR 225 decision 12 (signature carry-over) | Amended openly: the binding level decides when an approval carries over (decision 5) |
| ADR 225 decisions 1 and 7 (`authorMayApprove`) | Does not apply to a review request. Mode 4 is the path for a team with no OpenDustpan approvals |
| ADR 267 decision 14 (a push plan runs as the namespace service principal) and decision 19 ("A push from a fork starts nothing") | A plan with credentials needs linked pushers with `unit:run-plan`. A fork gets a plan with no credentials after a trusted comment |
| ADR 271 decision 9 (a replica drops an event that it cannot serve, and a poll catches the change) | Kept: federation keeps its poll, as the one listed exception |
| ADR 125 (agent changes go through jobs) and ADR 103 decision 3 | A write-back review request has the gate before the commit and a change order. The modes decide which approvals count |

## Open questions

Each question has the context, the options, the consequences, a
recommendation and a plain summary.

1. **A change of mode on a scope with open change orders.**
   - Options: (a) the mode is copied onto each change order at its creation,
     and a change applies only to new change orders. (b) the change closes
     each open change order.
   - Consequences: (a) two modes can be live in one scope for a while. (b) work
     in review is lost.
   - Recommendation: (a).
   - Plain summary: a new mode applies to new changes.

2. **What counts as one operation at L1.**
   - Context: decision 5. A replace can be create-then-delete or
     delete-then-create. Pulumi has URNs and aliases.
   - Options: (a) the address (or URN) and the kind (`create`, `update`,
     `delete`, `replace`), where each replace order is its own kind. (b) the
     same, with the changed attribute names.
   - Consequences: (a) a new value of the same attribute applies with no new
     approval. (b) more approvals, closer to L2.
   - Recommendation: (a). The docs give an example of each.
   - Plain summary: the approval covers "what changes and how", not each
     value.

3. **Apply before the merge.**
   - Context: RFC 27 question 6 put a hold on the units from the first
     pre-merge apply to the merge. Decision 6 replaces unit locks with plan
     conflicts for review requests.
   - Options: (a) no pre-merge apply in modes 1 and 2. (b) a pre-merge apply
     with the plan conflict check and no hold. (c) the hold of RFC 27 for
     pre-merge applies only.
   - Consequences: (a) simple. Teams that use Atlantis lose their flow. (b) a
     second pre-merge apply on the same resources is refused by the conflict
     check. (c) keeps a lock for the case where infrastructure runs code that
     no branch holds.
   - Recommendation: (c).
   - Plain summary: if you apply before you merge, the unit stays held until
     the merge.

4. **The status on GitLab Free and Premium.**
   - Context: the docs do not confirm that a status from the API counts for
     "Pipelines must succeed". A GitLab issue (535437, an unverified user
     report) says that a success status set through the commit status API let
     a merge request merge while its CI pipeline was broken.
   - Options: (a) test both directions first: does the status of OpenDustpan
     block a merge, and can it unblock a merge whose CI pipeline failed? If
     either test fails, a documented CI job asks the server for the state of
     `approvals`. (b) the gate on GitLab is advisory there, and a merge with no
     approval never applies.
   - Recommendation: (a), then (b) with a visible warning. No test runs before
     the owner decides on accounts and network.
   - Plain summary: we test GitLab before we rely on it.

5. **Who is a trusted person for a fork.**
   - Context: the design asks for a linked principal with `unit:run-plan` on
     each unit, for a push plan with credentials and for a trusted comment.
   - Options: (a) only that. (b) that, and write access on the Git host.
   - Consequences: (b) also stops a person who lost access on the Git host
     but whose link stays.
   - Recommendation: (b).
   - Plain summary: a person that both OpenDustpan and the Git host trust.

6. **The scope of the plan conflict check.**
   - Context: outputs can cross namespaces (RFC 15). A change order is in one
     namespace.
   - Options: (a) the namespace of the change order only. (b) each namespace
     that reads an output of a changed unit.
   - Consequences: (b) needs a read across namespaces, with the output sharing
     rules.
   - Recommendation: (b), limited to the units that the reader of the check can
     see. An unseen reader counts as a conflict.
   - Plain summary: a change that can break a reader in another namespace
     shows as a conflict.

7. **A Git host that is down.**
   - Context: decision 8 fails the command with the answer of the Git host.
   - Options: (a) wait. (b) a person with a new action and a fresh
     authentication moves the change to an OpenDustpan-only review, with a
     reason and a break-glass review (ADR 142).
   - Recommendation: (b).
   - Plain summary: in an outage, a person can move the review into
     OpenDustpan, on the record.

8. **A change across many repositories.**
    - Options: (a) one change order for each review request now, and a later
      RFC for a group of linked review requests. (b) one change order for many
      review requests now.
    - Recommendation: (a).
    - Plain summary: one review request, one change order for now.

## Checklist

- [x] Tolerates asynchronous replication between installations: the change
  order, its approvals and the snapshot of the Git host are namespace data
  under the fence of their namespace. A repository can hold units of more than
  one namespace. One webhook then writes to each namespace of the units that
  it touches, and each write carries the fence of its own namespace. No write
  waits for another namespace or installation.

## Outcome

Accepted. [ADR 299](../adr/0299-review-requests-and-change-orders-one-integration-with-the-git-host.md)
records the decisions.
