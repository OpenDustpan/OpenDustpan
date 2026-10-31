---
rfc: 114
title: Review requests and change orders, one integration with the Git host
status: discussing
created: 2026-10-31
---

# RFC 114. Review requests and change orders, one integration with the Git host

Status: discussing. The project owner decided the parts under "Decisions of
the owner" on 31 October 2026. An ADR records them when the open questions
close (ADR 15). This RFC amends the open parts of RFC 27 (questions 1, 4, 5,
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
effective mode of a unit shows in the API with the scope that set it.

| Mode | What it means | Trade-off in one line |
| --- | --- | --- |
| 1. Merged approvals | Approvals on the Git host and in OpenDustpan both count toward the requirements. The required status of OpenDustpan enforces the result. The approval rules and code owners of the Git host stay in force. | Two places to approve, joined by a status. **Not recommended.** |
| 2. OpenDustpan merges | The branch rules require the status of OpenDustpan. Optionally, only the OpenDustpan app can merge. The approval rules of the Git host are optional or mirrored. An apply after the merge is allowed. | One review place for the plan. The Git host keeps its code review. |
| 3. No review requests | Change orders start from commits. OpenDustpan reads the repository to show the code diff. | No review request flow for IaC. The review is in OpenDustpan only. |
| 4. No change orders | Like the Pulumi CLI with a backend: no change order and no approval. Deny rules and policies always apply. Runs are still recorded. Mode 4 refuses to turn on while an approval rule exists in its scope. | No approval step. Policies are the only gate. |

Notes on mode 1: GitHub has no API to add a required reviewer for one pull
request, so the combined rule lives in the OpenDustpan status, not in the
GitHub rules. GitLab Premium and Ultimate can add a merge request approval
rule through the API.

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
Pulumi use). A team can turn off the diff in the comment, and then the comment
holds the summary and the link only. This differs from HCP Terraform, which
posts a status with a link and no comment.

### 4. Leaks

A value in a plan comment that should not be there is handled like any leak
from a misconfiguration:

- A comment command redacts the plan: the server edits the comment on the Git
  host and says, in the comment, that it redacted it. A console button redacts
  it in the UI.
- A redaction invalidates the plan. An apply needs a new plan.
- The UI lists the resource addresses whose values leaked, so that the team
  rotates them.
- The history of the Git host (for example the edit history of a comment)
  cannot be cleaned by OpenDustpan. The docs say so.

### 5. Approval binding: three levels

A visible setting for each scope. The default is level 1.

| Level | An approval binds to | At the apply |
| --- | --- | --- |
| L0 | The code (the commit) | The apply plans again and applies. |
| L1 | The set of operations: each resource address and its kind of change | The apply plans again. The same set applies with no new approval. A new operation asks for approval again. |
| L2 | The exact plan: an OpenTofu saved plan. A Pulumi update plan only when the Pulumi CLI marks update plans stable | The stored plan applies, or the apply fails as stale. |

In each level, the tree of the merged commit must be the tree that was
planned, or the server plans again. A refresh keeps the default of each tool.
A gate "the last drift check is older than N hours" covers drift. The docs
explain the trade-off of each level.

### 6. Plan conflicts in place of unit locks

- Two review requests that touch the same unit can both merge while their
  code merges cleanly.
- OpenDustpan keeps its status red when the plans conflict: the changed
  resources and their dependency closure overlap, also across units through
  outputs. The status names the resources that collide.
- When the server is not sure, it counts a conflict. These cases are not
  sure: provider changes, replacements, renames through `moved` blocks or
  aliases, data sources, deletes, and changes of a parent resource.
- The check has its own conformance tests, with a fixture for each case.

### 7. Security rules

- **S1. Authors.** Whoever opened the review request, authored a commit in it
  or pushed to it is an author, and cannot approve it on either side. Any push
  resets the approvals on both sides.
- **Webhooks.** The server verifies each signature, refuses a replay, and never
  decides on the payload alone. It reads the current state of the review
  request through the API before each gate decision.
- **Identity links** use the immutable user ID of the Git host, never the
  login.
- **No user token of the Git host is stored on the backend.** An OAuth user
  token is bound to the console session only. Background work runs as the
  OpenDustpan app, and the record names the person for whom it runs.
- **Agents never approve**, on either side.
- **Forks.** A review request from a fork gets a plan only after a trusted
  person comments, and that plan runs with no credentials.

### 8. Failures

- When the Git host says "not mergeable" or does not answer, the command
  fails with the answer of the Git host (as Atlantis does).
- Webhooks only, no polling.
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
  conflicts (decision 6). On a merge queue or merge train commit, it is
  success only when the root tree of that commit is the tree of the approved
  revision.
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
| `approvals` status required | Yes | Yes | Not applicable | No. `plan` status only |
| Who merges | People on the Git host | OpenDustpan, or people, as the scope sets | No review request | People |
| Apply | After the merge | After the merge | At the apply request | As the tool runs today |

### Mode 2: "only the OpenDustpan app merges", with no bypass

The merge goes through the normal merge API with the head of the approved
revision. The app is never a bypass actor. Where the Git host can restrict who
merges, the scope can name the app as the only merger: GitHub "restrict who
can push" on the branch (organization repositories), GitLab "allowed to
merge" set to the bot only, Gitea and Forgejo the merge allowlist. The server
reads the rules and refuses this option when the app is on a bypass list or
when the Git host cannot restrict the merger. The console says why.

### Mode 3: change orders from commits

A push to a branch that no review request uses starts a change order. The
server reads the commits, the diff and the files through the API of the Git
host to show the code diff. After the approval and the apply request, the
server merges the branch through the API (a review request as a carrier where
the branch rules need one). Non-IaC files in the same repository keep their
own flow on the Git host. A required status applies to each review request of
the branch, because no Git host scopes a required status by path. So the
server posts `approvals` = success "no OpenDustpan unit changed" on review
requests with no unit path. While the server is down, those merges wait.

### Comments and the timeline

- Code comments: the Git host is the only store. The console reads them
  (event-driven) and writes them as the person with the session-bound token.
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

### The user token, bound to the session

- The person links a Git host account (by the immutable user ID) and, in each
  console session, authorizes the OpenDustpan app on the Git host. The token
  is kept for the life of that session only, with no refresh token beyond it,
  and is deleted at sign-out or session expiry.
- The token is used only inside the HTTP request of that person's action:
  write a code comment, mirror an approval (mode 2), approve on the Git host
  (mode 1), merge as the person.
- Two layers of authorization: OpenDustpan checks its own action first, and
  the Git host checks the rights of the person.
- With no token in the session, each control that needs one is off with the
  reason ("Sign in to GitHub from here to comment as yourself"), never hidden.
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
| Check at the boundary | These have no effect: a webhook with a bad signature, and a replayed delivery ID. An event that says "approved" while the API answers "commented" has no effect. A review on an older commit has no effect. A check of the same name from another app has no effect. A console action with an old digest or head has no effect. |
| Contain | The immutable user ID, never a login. A renamed account that takes the login of a linked person: no effect. The session token never reaches a log, an audit entry, a store or a runner. A comment from the Git host reaches the UI escaped. |
| Limit | The smallest token permission. The token only in the person's request and only for the session. A fork plan runs with no credentials, only after a trusted person comments. The request budget and the circuit breaker. |
| Authorize at use | OpenDustpan checks its action, then the Git host checks the person. At the approval and again at the apply. An author never counts. A resolve by an author does not unblock. |
| Fail closed | A timeout, `null` or `checking`, an expired token, an open breaker, a Git host that says "not mergeable": no approval, no apply, and an audit entry. |

## What this changes in accepted decisions

| ADR or RFC | Effect |
| --- | --- |
| ADR 103 decision 3 ("approvals add to the forge") | Replaced by the four modes |
| ADR 190 decision 4 (authors) | S1 |
| ADR 190 decision 10 (apply is a request of a person) | Modes 1 and 2: the merge is the request |
| ADR 190 decision 12 | A close of the review request closes the change order |
| ADR 191 decisions 4 to 6 (the digest binds the exact plan. A new plan voids each signature) | The three binding levels. L2 keeps the digest of today |
| ADR 192, ADR 193 | Plan comments and verdicts stay in OpenDustpan. Code comments live on the Git host |
| ADR 102 decision 8 and RFC 27 question 11 (no plan value on the Git host) | The plan comment holds the diff unless the team turns it off. Leaks are handled by decision 4 |
| RFC 27 question 6 (a hold from the first pre-merge apply) | Plan conflicts replace unit locks for review requests. See open question 4 |
| ADR 271 decision 4 ("each consumer that must not miss an event keeps a poll") | Webhooks only for review requests |
| ADR 225 decision 7 ("an approver is a person in a session") | Mode 1 counts Git host reviews of linked person accounts |

## Open questions

Each question has the context, the options, the consequences, a
recommendation and a plain summary.

1. **Where the session-bound token lives.**
   - Context: decision 7 forbids a stored token. A server with more than one
     instance must find the token of a session on each instance.
   - Options: (a) in the session record, sealed, deleted with the session. (b) in the memory of one instance, with sticky sessions. (c) in the
     browser, sent with each request.
   - Consequences: (a) works with each instance, and the token is in the
     database for the life of the session. (b) a restart loses it, and the
     person signs in to the Git host again. (c) the browser holds a token that
     can write to the Git host.
   - Recommendation: (a), if the owner reads "not stored" as "no store beyond
     the session".
   - Plain summary: the token lives as long as your session and dies with it.

2. **A change of mode on a scope with open change orders.**
   - Options: (a) the mode is copied onto each change order at its creation,
     and a change applies only to new change orders. (b) the change closes
     each open change order.
   - Consequences: (a) two modes can be live in one scope for a while. (b) work
     in review is lost.
   - Recommendation: (a).
   - Plain summary: a new mode applies to new changes.

3. **What counts as one operation at L1.**
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

4. **Apply before the merge.**
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

5. **The status on GitLab Free and Premium.**
   - Context: the docs do not confirm that a status from the API counts for
     "Pipelines must succeed".
   - Options: (a) test it first. If it fails, a documented CI job asks the
     server for the state of `approvals`. (b) the gate on GitLab is advisory
     there, and a merge with no approval never applies.
   - Recommendation: (a), then (b) with a visible warning. No test runs before
     the owner decides on accounts and network.
   - Plain summary: we test GitLab before we rely on it.

6. **Who is a trusted person for a fork.**
   - Options: (a) a linked principal with `unit:run-plan` on each unit. (b) an
     account with write access on the Git host. (c) both.
   - Recommendation: (c).
   - Plain summary: a person that both OpenDustpan and the Git host trust.

7. **The scope of the plan conflict check.**
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

8. **A Git host that is down.**
   - Context: decision 8 fails the command with the answer of the Git host.
   - Options: (a) wait. (b) a person with a new action and a fresh
     authentication moves the change to an OpenDustpan-only review, with a
     reason and a break-glass review (ADR 142).
   - Recommendation: (b).
   - Plain summary: in an outage, a person can move the review into
     OpenDustpan, on the record.

9. **A change across many repositories.**
    - Options: (a) one change order for each review request now, and a later
      RFC for a group of linked review requests. (b) one change order for many
      review requests now.
    - Recommendation: (a).
    - Plain summary: one review request, one change order for now.

## Checklist

- [x] Tolerates asynchronous replication between installations: the change
  order, its approvals and the snapshot of the Git host are namespace data
  under the fence of their namespace. A read of the Git host writes only to
  that namespace.

## Outcome

None yet.
