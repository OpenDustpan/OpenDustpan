---
rfc: 89
title: The Forge port, what each consumer needs from a forge, and the forge connection API
status: proposed answers; waiting for owner acceptance
created: 2026-10-19
---

# RFC 89. The Forge port, what each consumer needs from a forge, and the forge connection API

Status: proposed answers; waiting for owner acceptance. An agent wrote this RFC for issue #886 under the
direction of the project manager. It proposes the shape of the `Forge` port
before the GitHub adapter. The pull request of this RFC also adds the port
as Go interfaces in `pkg/forge` and an empty conformance suite in
`pkg/forge/forgetest`. It adds no adapter, no SQL and no route. The
proposals have the label "(proposed)". The owner answers the questions at
the end. The Go interface holds only the methods that an accepted decision
needs today. The methods that only RFC 12, RFC 24 and RFC 27 need are in
this RFC as proposals, and they enter the Go interface with their
consumers.

## Problem

Many accepted decisions use a forge, and each one names "the `Forge` port
of RFC 12, which a later ADR decides" (ADR 101 decision 3):

- The write-back executor commits through the API of the forge, opens a
  review request, and looks for its own commit after a takeover (ADR 101
  decision 3). The review watcher records merges and closes, from a
  webhook or from a poll (ADR 100 decision 15). `job.verify` asks the forge
  whether a commit contains another commit.
- The Catalog reads request files from the forge at a commit
  (`catalog.RequestFiles`, ADR 110 and ADR 116).
- The change order review reads the cause diff from the forge
  (`changeorder.CauseDiffs`, ADR 196 decision 3).
- The source pipeline gets a git pack with the credentials of the forge
  connection (ADR 135). A runner step in the git credential mode `forge`
  gets a short-lived read-only token of the forge connection (ADR 130
  decision 11).
- RFC 27 asks for review request feedback (a summary comment and a
  status) and for comment commands. RFC 24 asks for triggers on a push, a
  tag and a review request.

No port exists yet. Each consumer has its own narrow port, or none. If each
adapter implements each narrow port, a new forge costs one adapter for each
consumer, and nothing tests that the adapters agree.

### Owner decisions that this RFC keeps

- **The first forge is GitHub, then GitLab** (owner, 2026-10-18, issue
  #886, answers 12.1 and 27.1). Forgejo and other small forges get a spike
  that tests the port for missing essentials. They are not built yet.
- **A small self-hostable forge bridge is an input to the port design**
  (issue #886, answers 12.9). A bridge carries the traffic to a version
  control system that the server cannot reach, for example one in an
  air-gapped network, or to a version control system with no forge API.
- **Forge credentials are sealed credentials of the namespace** (ADR 213
  decision 7): sealed with a data key that the key-encryption key wraps,
  never answered, rotated with two active sets, with a WARN log line and
  the metric `forge_credential_fallback_total` for a fallback.
- **A namespace can have many forge connections and one default** (ADR
  213 decision 8). The removal of the default fails until another
  connection is the default.

### Accepted decisions that limit the port

1. Writes go only through the API of the forge, never with a git client in
   the server (RFC 12, ADR 101 decision 3).
2. A change never lands twice: a takeover looks for the commit of the job
   by its trailer `Dustpan-Job: <job ID>` and for the branch
   `dustpan/<job ID>` (ADR 101 decision 3).
3. A conflict or a refusal by branch protection fails the job at once. A
   timeout or a rate limit retries with backoff (ADR 101 decision 3).
4. OpenDustpan approvals add to the approvals of the forge. They never
   replace them (ADR 103 decision 3).
5. Only a person in a session approves (ADR 225 decision 7). A comment on
   a forge is not a session.
6. Shared code stays tool-neutral (ADR 12, ADR 13). The port also stays
   forge-neutral: no type, method or error in it exists only because one
   forge needs it.
7. Code holds no internal project reference (ADR 66). The Go comments of
   `pkg/forge` describe the behavior, not this RFC.
8. A comment command never approves, never makes an approval intent or an
   approve link, and never confirms a gate. The summary comment can link to
   the console page of the job, where the plan is on screen. Each
   command runs as the linked principal with its actions (owner decision of
   19 October 2026: an approval needs the plan on screen; ADR 146 decision
   8). Q8 proposes a command list.

## The consumers, and what each needs from a forge

| Consumer (`forge.Consumer`) | When | What it needs from the forge | Port methods | Capabilities (`forge.Needs`) |
| --- | --- | --- | --- | --- |
| Write-back, direct push (`write-back`, ADR 101) | The deliver phase, the takeover, `job.abandon`, `job.verify` | The head of the ref of the code location. Each edited file at the base commit. One commit of many files, with a check that no edited file changed after the base. A search for the commit with the trailer of the job. An ancestor check for `job.verify`. | `Head`, `ReadFile`, `Commit`, `FindCommit`, `Compare` | `files`, `compare`, `commit` |
| Write-back, review request (`write-back-review`, ADR 101, ADR 100 decision 15) | The same, and the review watcher | The same, and a review request that a second open call does not duplicate. The state of the review request, with the merge time and the merge commit. A close with no merge. | The same, and `OpenReview`, `ReadReview`, `CloseReview` | `files`, `compare`, `commit`, `review` |
| Catalog request files (`catalog`, ADR 116) | A render of a request in git | One file at one commit. | `ReadFile` | `files` |
| Change order cause diff (`cause-diff`, ADR 196) | Each read of the review | The file diff from base to head, with a byte limit, and an ancestor check. | `Compare` | `compare` |
| Source fetch (`fetch`, ADR 135) | The `source.fetch` operation, in the server | A credential for the git protocol. The token can be the long-lived token of the connection: it stays in the server. | `RepoToken` | `git-access` |
| Runner git (`runner-git`, ADR 130 decision 11) | A runner step in the git credential mode `forge` | A token that reads one repository only and expires soon. The runner path refuses a token with `Scoped` not set. | `RepoToken` | `repo-token` |
| Triggers (RFC 24) (proposed, no Go constant yet) | A push, a tag, a review request that opens or changes its head | Signed events. The changed paths from the old head to the new head, to find the affected units. | `ParseEvent`, `Compare`, `Head` | `events`, `files`, `compare` |
| Review request feedback (RFC 27) (proposed, no Go constant yet) | After a plan, and after an apply | A status on the head commit for each unit. One summary comment that the server edits in place. A merge at an expected head, for "apply before merge" (RFC 12). | `SetStatus`, `PutComment`, `MergeReview` (proposed) | `status`, `comment`, `merge` (proposed) |
| Comment commands (RFC 27) (proposed, no Go constant yet) | A new comment on a review request | A signed event. A read of the comment through the API, with the stable ID of its author, before the server acts. A reply. | `ParseEvent`, `ReadComment`, `PutComment`, `ReadReview` | `events`, `comment`, `review` (proposed) |

`forge.Needs` holds the same map in code, as capabilities (below), for the
rows that have a constant. `TestNeeds` in `pkg/forge` lists the same rows,
one for one. The server checks the map when a feature names a connection, so
that a feature that a connection cannot serve fails at configuration time,
not in a job. The first six rows are in the Go interface now. The last
three rows wait for the RFCs that need them.

## The proposed port

### Where it lives (proposed)

A new tool-neutral domain package, `pkg/forge`. It owns the forge
connections of a namespace: their store port, the sealed credential sets,
the default flag and the service that picks a connection, opens it with
the newest credential set that works, and falls back to an older set
(ADR 213 decision 7). That service is the consumer of the `Forge` port.
The internal wiring implements the narrow ports that exist
(`catalog.RequestFiles`, `changeorder.CauseDiffs`, `source.Fetcher`) and
the write-back executor over the service. So a consumer never imports an
adapter, and an adapter implements one port for all consumers. Q1 asks
the owner.

The package is in the `domain` rule of depguard. It imports no
`net/http`, no SQL and no adapter. The adapters live in
`pkg/forge/github`, `pkg/forge/gitlab` and `pkg/forge/bridge`, and run
`pkg/forge/forgetest`.

### One interface, with capabilities (proposed)

```go
// Opener makes a Forge for one connection and one credential set.
type Opener interface {
	Kind() Kind
	// ErrInvalid for a connection of another kind, an auth kind that the
	// adapter does not support, a missing parameter or a secret that it
	// cannot parse. It makes no call to the forge.
	Open(ctx context.Context, c Connection, cred Credential) (Forge, error)
}

type Forge interface {
	Capabilities() Capabilities              // no call to the forge
	Check(ctx context.Context) (Account, error)
	Files    // Head, ReadFile, Compare
	Commits  // Commit, FindCommit
	Reviews  // OpenReview, ReadReview, CloseReview
	Tokens   // RepoToken
	Events   // ParseEvent
}
```

`Connection` and `Credential` are structs of the package. `Credential` holds
the `Secret` (the private key of an app or a token), the set ID and the
creation time. It holds no event secret. The event secret belongs to the
app registration (below, "Webhook intake"), not to a connection (PM
decision). No registration type exists in Go yet, so `ParseEvent` takes the
active event secrets as an argument: the intake reads them for the hook ID
and gives them all to the call. An event is valid when it matches any one of
them, which is how two active secrets work during a rotation. Who makes the
event secret is Q9.

Every adapter implements every method. A method that the forge, or the
auth kind of the connection, cannot do returns `ErrUnsupported`, and
`Capabilities` does not list its capability. A small forge or a bridge
then gives what it can, and the server says what is missing. This is the
pattern of the Kubernetes Container Storage Interface, where a driver
implements one service and lists its capabilities.

The capabilities:

| Capability | Methods |
| --- | --- |
| `files` | `Head`, `ReadFile` |
| `compare` | `Compare` |
| `commit` | `Commit`, `FindCommit` |
| `review` | `OpenReview`, `ReadReview`, `CloseReview` |
| `git-access` | `RepoToken`: a token for the git protocol, scoped or not |
| `repo-token` | `RepoToken` with `Token.Scoped` set: one repository, read only, at most 1 hour |
| `events` | `ParseEvent` |

With `Push` not set, the list has no `commit` and no `review`. The
capability `repo-token` implies `git-access`: a connection that lists
`repo-token` also lists `git-access`. The capabilities
`merge`, `status` and `comment` are proposed with their methods (below).

### Methods and their errors

Each method takes the repository by its full path on the forge, for
example `acme/infra` or `group/subgroup/infra`. Each method can also return
`ErrAuth` (the forge refused the credential) and `ErrUnavailable` (no
answer, a server error or a rate limit). The adapter wraps a sentinel with
`%w` and never returns an error of its HTTP client alone.

| Method | Does | Errors |
| --- | --- | --- |
| `Check()` | Calls the forge and returns the account of the credential. A connection test and a rotation call it. | `ErrAuth`, `ErrUnavailable` |
| `Head(repo, ref)` | The commit at the head of a branch or a tag, with its time. | `ErrNotFound` |
| `ReadFile(repo, commit, path, limit)` | The content and the blob ID of one file at one full commit ID. | `ErrNotFound`, `ErrTooLarge` |
| `Compare(repo, base, head, limit)` | Each changed file (added, modified, deleted, renamed), the patch up to `limit` bytes with `Truncated`, and whether `base` is an ancestor of `head`. | `ErrNotFound` |
| `Commit(repo, req)` | One commit of 1 to 64 file changes (at most 4 MiB) on `req.Branch`, all or none. With `NewBranch`, it creates the branch at `req.Base`. | `ErrNotFound` for a missing repository, or a missing branch with `NewBranch` not set. `ErrConflict` when a changed file differs at the head of the branch from `req.Base`, or a new branch exists with another head. `ErrRefused` for branch protection or a missing permission. `ErrInvalid` and `ErrTooLarge` from `ValidateCommit`, with no call. |
| `FindCommit(repo, ref, trailer, since)` | The newest commit on `ref`, at or after `since`, whose message has the trailer line. | `ErrNotFound` |
| `OpenReview(repo, req)` | A review request from `req.Branch` into `req.Target`. An open review request of the same branch and the same target is returned, not duplicated. | `ErrNotFound` for a missing branch, `ErrRefused` |
| `ReadReview(repo, id)` | The state (`open`, `merged`, `closed`), the head, and the merge time and merge commit after a merge. | `ErrNotFound` |
| `CloseReview(repo, id)` | Closes with no merge. A closed one returns nil. | `ErrNotFound` for a missing one, `ErrConflict` for a merged one |
| `RepoToken(repo)` | A token for the git protocol. An app connection answers a token that reads only `repo` and expires in at most 1 hour, with `Scoped` set. A `token` connection answers its own token with `Scoped` not set. | `ErrUnsupported` when the auth kind has no token for the git protocol. `ErrNotFound` for a missing repository. The runner path refuses a token with `Scoped` not set, so the long-lived token never leaves the server. |
| `ParseEvent(r, secrets)` | Checks the signature or token of an event against each active event secret of the registration, and returns a forge-neutral event: `ping` or `review` (`push` and `comment` wait for their consumers), with the delivery ID and, for an app, the installation ID. No call to the forge. | `ErrSignature`, `ErrInvalid`, `ErrUnsupported` for another kind |

The sentinels are `ErrNotFound`, `ErrConflict`, `ErrRefused`, `ErrAuth`,
`ErrUnavailable`, `ErrTooLarge`, `ErrUnsupported`, `ErrSignature` and
`ErrInvalid`. An adapter returns a rate limit through
`forge.Unavailable(after, reason)`. That is an `*UnavailableError` that wraps
`ErrUnavailable` and holds the wait time that the forge gave (for example
the header `Retry-After`). `forge.RetryAfter(err)` reads the wait time. The
retry of a job uses it in place of its own backoff when it is longer. The
wiring maps `ErrUnavailable` to
`catalog.ErrForgeUnavailable` and `changeorder.ErrForgeUnavailable`, and
`ErrConflict` and `ErrRefused` to a failed write-back job (ADR 101
decision 3). The forge APIs answer "not found" for a repository that the
credential cannot see, so `ErrNotFound` covers both, as the forge does. Each method returns
`ErrNotFound` for a missing repository.

**The conflict rule of `Commit`.** ADR 101 decision 3 fails a write-back
only when an edited key changed after the base. The port checks at the
file level: a commit fails with `ErrConflict` only when a file of the
change differs at the head of the branch from the base. A change of
another file on the branch is not a conflict. The write-back executor then
reads the file again and checks the keys, as ADR 101 decision 3 says.

**The mechanics of `Commit` on GitHub (proposed).** The adapter uses
`createCommitOnBranch` with `expectedHeadOid`, which fails when the head
moved:

1. The branch exists (`NewBranch` not set). The adapter reads the head of
   the branch. When the head is `req.Base`, it commits with
   `expectedHeadOid` set to `req.Base`. When the head is another commit, it
   calls `Compare(req.Base, head)`. It returns `ErrConflict` when
   `BaseIsAncestor` is false, or when a path of `req.Changes` is in the
   changed files. Otherwise it commits with `expectedHeadOid` set to that
   head.
2. The commit call fails because the head moved between the read and the
   call. The adapter goes back to step 1, at most 3 times in all, and then
   returns `ErrConflict`.
3. `NewBranch` is set. The mutation cannot create a branch, so the adapter
   creates the ref at `req.Base` first (`POST /repos/{owner}/{repo}/git/refs`).
   When the forge answers that the ref exists, the adapter reads the head. A
   head other than `req.Base` is `ErrConflict`. A head equal to `req.Base`
   is not a conflict yet: this is the two-call race. Two calls for one new
   branch both create or find the ref at `req.Base`, and both commit with
   `expectedHeadOid` set to `req.Base`. The forge lets one commit pass, and
   the other fails with a moved head, which is `ErrConflict`. The suite has
   a case for it.
4. A retry of the same job after a crash can find a branch whose head is its
   own commit. The port says `ErrConflict`. The write-back executor calls
   `FindCommit` with the trailer of the job before a second commit, so it
   finds its own commit and does not commit again (ADR 101 decision 3).

GitLab does not need steps 1 to 3: its commits API takes `last_commit_id`
for each file and `start_sha` for a new branch (below).

### Proposed methods, not in the Go interface (proposed)

These methods have their consumers in open RFCs: RFC 12 (apply before
merge), RFC 24 (triggers) and RFC 27 (review request feedback and comment
commands). They enter the Go interface, with their capabilities and their
suite cases, in the pull request that adds the first consumer.

| Method | Does | Errors |
| --- | --- | --- |
| `MergeReview(repo, id, head, method)` | Merges only when the head is `head`, with the method of Q11. | `ErrConflict` for another head or a merge conflict, `ErrRefused` for a rule of the forge |
| `SetStatus(repo, commit, s)` | Sets the status with the name `s.Name`. A second call with the same name replaces it. | `ErrNotFound` |
| `PutComment(repo, reviewID, key, body)` | Writes the comment that `key` names. The first call creates it. A later call edits it in place. | `ErrNotFound` |
| `ReadComment(repo, reviewID, id)` | The comment as the forge holds it now, with the stable ID of its author. | `ErrNotFound` for a missing or deleted comment |

The capabilities would be `merge`, `status` and `comment` (`PutComment` and
`ReadComment`).

**The key of `PutComment`.** The forge has no field for a key. The adapter
puts a hidden marker that is the same on every forge (an HTML comment such
as `<!-- opendustpan:key=... -->`) in the body. To edit in place, it lists
the comments of the review request, finds the comment with the marker, and
checks that the author of that comment is the account of the connection. A
comment with the marker from another account is not edited, so that a person
cannot make the server edit a comment of theirs.

The suite cases for these methods wait for the methods: merge at the head,
merge with a stale head is a conflict, merge that a rule of the forge stops
is refused, set status twice replaces it, put comment twice edits one
comment, put comment on a missing review request is not found, put comment
with two keys makes two comments, read comment answers the stable ID of the
author, read an edited comment answers the new body, read a deleted comment
is not found.

### The conformance suite

`pkg/forge/forgetest.Run` takes a factory of a `Harness`: the `Forge`
under test, a fresh repository, and a `Fixture` that changes the
repository outside the port (a push by another author, branch protection,
a merge by a person, a signed event, a redelivery, a revoked credential).
Each adapter runs it against a recorded or a fake API of its forge, as
issue #886 asks for GitHub.

The suite lists 63 table-driven cases (`forgetest.CaseNames`). Each case
has no body yet and skips with "not implemented". A case gets its body
before an adapter implements the methods that it covers (CLAUDE.md: a new
port method gets conformance tests before any adapter implements it). A
case of a capability that the forge does not list skips for that forge.
The `Harness` holds the `Opener`, the `Connection` and the `Credential`, so
that a case can open the forge again with a bad secret or with a second
credential set. The `Fixture` makes the events with a secret that the case
gives (the kind may be one that the port does not name), repeats a
delivery with `Redeliver` so that a retried delivery keeps its delivery ID,
and can make the forge fail with a server error or a rate limit. The
case "an operation outside the capabilities is unsupported" checks that such
a method returns `ErrUnsupported`. The case "no error and no event holds a
part of the secret" puts a canary in the secret and in the event secret, and
checks each error text and each field of each event. The case "parse event of an app has the installation ID" skips when `Connection.Auth` is not `app`. The case "a connection with no push access lists no commit and no review" checks the rule of `Push`, and the case "a forge with repo-token also lists git-access" checks the rule above.

## How the auth kinds map to the port

A connection has a kind (`github` or `gitlab`; a bridge kind waits for Q5), an auth kind
(`app` or `token`), the API address of the forge, parameters that are not
secret, and a push flag. The credential sets are sealed (ADR 213 decision
7). `Opener.Open` gets one unsealed set, and the service tries the next
older set after `ErrAuth`.

**GitHub App (`app`).** The parameters are the app ID and the
installation ID. The sealed secret is the private key of the app. The
adapter signs a JWT with the key (RS256, at most 10 minutes), and gets an
installation access token, which expires after 1 hour, with
`POST /app/installations/{installation_id}/access_tokens`. That call takes
`repositories` and `permissions`, so `RepoToken` asks for one repository
and `contents: read` only. A GitHub App is the only auth kind here that
gives every capability with a token that is limited for each call.
Sources:
<https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-json-web-token-jwt-for-a-github-app>,
<https://docs.github.com/en/rest/apps/apps#create-an-installation-access-token-for-an-app>.

**GitHub token (`token`).** The sealed secret is a fine-grained personal
access token. Its owner limits it to repositories and permissions when
they make it. The adapter cannot make a narrower token from it. So
`RepoToken` answers the token itself with `Scoped` not set, and
`Capabilities` lists `git-access` and not `repo-token`. The source fetch
of the server works: the token stays in the server. The runner path refuses
the token, so a pool in the git credential mode `forge` cannot use the
connection (Q3).

**GitHub, the commit.** Two APIs make a commit of many files with no
clone (Q4):

- The Git database API: create blobs, a tree and a commit, then update the
  ref with `force: false`. The commit can have the person as its author.
  GitHub does not sign it.
- The GraphQL mutation `createCommitOnBranch`, which takes
  `expectedHeadOid`. GitHub signs a commit that it makes for a GitHub App,
  and the commit shows as verified. The author is the account of the
  connection (spike). The person goes in a `Co-authored-by` trailer.

Sources: <https://docs.github.com/en/rest/git>,
<https://docs.github.com/en/graphql/reference/mutations#createcommitonbranch>.

**GitHub, the rest.** The proposed `MergeReview` would use
`PUT /repos/{owner}/{repo}/pulls/{pull_number}/merge` with `sha`, which
answers 409 when the head moved. The proposed `SetStatus` would use the
commit status API with `context`, which works for both auth kinds. The
Checks API needs a GitHub App, so the port does not use it in the first
version. Events carry `X-Hub-Signature-256` (HMAC-SHA256 of the body) and
`X-GitHub-Delivery`. GitHub does not send a failed delivery again by itself.
So the review watcher keeps its poll (ADR 100 decision 15), and triggers
need one too (Q2). A GitHub App has one webhook URL for all its
installations, and each event carries the installation ID; see the intake
below. Source:
<https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries>.

**GitHub Enterprise Server** is the same adapter with another API address.

## How GitLab, Forgejo and a bridge fit

**GitLab (`token`).** The sealed secret is a group or project access
token with the scope `api`. The commits API takes many `actions` with
`last_commit_id` for each file, and `start_sha` for a new branch, so it
gives the conflict rule of `Commit` in one call. The proposed methods
would use the merge API with `sha`, the commit status API with `name`, and
the notes of the merge request. Events carry `X-Gitlab-Token`, which is the
secret in plain text, not a signature of the body (spike: newer GitLab
releases may sign webhooks). GitLab has no call that makes a token for
one repository and read access from a group access token (spike). So
`RepoToken` answers the token of the connection with `Scoped` not set, and
`repo-token` needs a second, read-only project access token, or stays off
(Q3). Sources:
<https://docs.gitlab.com/api/commits/>,
<https://docs.gitlab.com/api/merge_requests/>,
<https://docs.gitlab.com/user/project/integrations/webhooks/>.

**Forgejo (spike only).** The facts below come from the API documentation.
The spike checks each of them. `POST /repos/{owner}/{repo}/contents`
changes many files in one commit, with the blob `sha` of each file it
replaces and `new_branch`. Pull requests merge with
`head_commit_id`. Events carry `X-Forgejo-Signature` (HMAC-SHA256). An
access token has scopes for each kind of object, not for each
repository, so `repo-token` stays off and `git-access` is on. Source:
<https://forgejo.org/docs/latest/user/api-usage/>.

**A forge bridge (proposed, not in the Go port yet).** A bridge is a small
process that a team runs next to its version control system. It would
implement the operations of the port on that side, and speak one protocol of
OpenDustpan to the server. The server would have one adapter for it,
`pkg/forge/bridge`. The bridge dials out to the server, and asks for work
with long polling, as a runner does (ADR 131). So a network that allows no
traffic into it can still use write-back, and the server needs no route to
the version control system. `forge.Kind` has no bridge value until Q5 has an
answer; the bridge question is open, and the Go code does not hold a name for
it. A bridge in front of a plain git server can use a git client on its own
side: rule 1 above is about the server, and the bridge is the API of that
system. A plain git server has no review request, so a bridge for it lists no
`review`, and write-back can only push directly.

### The essentials: what each consumer needs, and what each forge gives

"Yes" means the API gives the capability with the auth kind named.
"Spike" means the spike must check it. "Bridge" means the bridge gives it
when its version control system has the feature.

| Capability | Needed by | GitHub App | GitHub token | GitLab token | Forgejo token | Bridge |
| --- | --- | --- | --- | --- | --- | --- |
| `files` | write-back, Catalog, triggers | yes | yes | yes | yes (spike) | yes |
| `compare` (diff and ancestry) | write-back verify, cause diff, triggers | yes | yes | yes | spike | bridge |
| `commit` (many files, base check, trailer search) | write-back | yes | yes | yes | yes (spike) | yes |
| `review` | write-back by review request, comment commands | yes | yes | yes | yes (spike) | bridge |
| `merge` at an expected head (proposed) | apply before merge | yes | yes | yes | yes (spike) | bridge |
| `status` (proposed) | feedback | yes | yes | yes | yes (spike) | bridge |
| `comment` (edit in place, read with author ID) (proposed) | feedback, comment commands | yes | yes | yes | spike | bridge |
| `git-access` (a token for the git protocol) | source fetch in the server | yes | yes | yes | yes (spike) | no |
| `repo-token` (one repository, read, at most 1 hour) | runner git, git mode `forge` | yes | no | no (spike) | no (spike) | no |
| `events` (signed, with a delivery ID) | review watcher, triggers, comment commands | yes | yes, a repository webhook | token only (spike) | yes (spike) | yes, through the bridge protocol |
| Automatic redelivery of a failed event | triggers | no | no | spike | spike | bridge |

The spike of Forgejo fills the column with facts, and adds a row for each
essential that the port does not have.

## Webhook intake (proposed: parse in the port, the route outside it)

The port holds `ParseEvent`, because each forge signs its events in its
own way. The HTTP route is in the internal API adapter, not in the port.

- The route is `POST /forge-events/{hook ID}`. As ADR 210 decision 2 gives
  for a path that an external system stores, it is outside
  `/api/dustpan/v1` and has no API version. It is on the public list of
  the route test. This adds a row to the table of ADR 210 decision 2.
- The server gives each connection a random hook ID when a person creates
  it. The hook ID is unique in the installation. A read of the connection
  answers it. A GitHub App has one webhook URL for all its installations,
  so the hook ID belongs to an app registration: the connections of one
  namespace with the same app ID share one hook ID and one event secret.
  The event secret belongs to the app registration, not to a connection (PM
  decision). The registration holds the app ID, the hook ID and the sealed
  event secrets, two during a rotation. A `token` connection has a
  registration of its own, with one connection. The create call refuses with
  409 an app ID that a connection of another namespace uses.
  `ParseEvent` puts the installation ID of the event in `Event`, and the
  server maps it to the connection whose installation ID parameter matches.
  An event for an installation that no connection names is dropped with 204
  and counted in `forge_event_rejected_total` with the reason
  `unknown-installation`.
- The route reads at most 2 MiB of body (proposed), finds the registration
  by the hook ID, and calls `ParseEvent` with the active event secrets of
  that hook ID. The intake checks the event against those secrets before it
  trusts `InstallationID`. It answers 204 to each request,
  also for an unknown hook ID and a bad signature. The metric
  `forge_event_rejected_total` counts the rejects by reason and connection.
  A body above the limit is dropped with 204, counted as
  `forge_event_rejected_total{reason="too-large"}`, and the poll catches the
  change that it announced.
- The server drops a delivery ID that it saw for the same connection in
  the last 24 hours.
- **An event is a hint** (proposed). The server reads the object through
  the API before it changes a record: `ReadReview` before the review
  watcher records a merge, `ReadComment` (proposed) before a comment
  command, `Head` before a trigger starts a job. So a forged or replayed
  event can cause one more read, and nothing else. Stripe tells a receiver
  of its events the same: the order is not promised, and the receiver can
  get the object through the API. Source: <https://docs.stripe.com/webhooks>.
- **Where the work runs.** The intake runs in the `api` component (ADR 249
  decision 2): it parses the event, drops a repeated delivery and inserts a
  durable operation. The effects run as durable operations in the `worker`
  component: the reads through the API, the review watcher, the start of a
  trigger. An `api` process never claims an operation.
- The event secret is part of the app registration, not of the credential
  set. Who makes it, and whether the server answers it, is Q9. Where the events go
  after a promotion, or at a replica that does not forward, is Q10.

## The forge connection REST API

All routes are under `/api/dustpan/v1/namespaces/{namespace}/forge-connections`
(ADR 210 decision 3). Each mutating route takes `Idempotency-Key` (ADR 210
decision 6). Each write checks the namespace fence and writes an audit
entry with no part of a credential (ADR 213 decision 7).

| Method and path | Does | Action | Object |
| --- | --- | --- | --- |
| `GET .../forge-connections` | Lists the connections: ID, kind, auth kind, API address, parameters that are not secret, push flag, default flag, hook ID, and for each credential set its ID and creation time. Never a secret. | `namespace:read-forge-connections` | the namespace |
| `GET .../forge-connections/{id}` | Reads one connection, as in the list. | `namespace:read-forge-connections` | the namespace |
| `POST .../forge-connections` | Creates a connection with its first credential set. The first connection of a namespace becomes the default. The answer holds the hook ID and no secret (Q9). Fails with 409 for an app ID that a connection of another namespace uses. | `namespace:manage-forge-connections` | the namespace |
| `PATCH .../forge-connections/{id}` | Changes the API address, the parameters or the push flag. Never a credential. | `namespace:manage-forge-connections` | the namespace |
| `DELETE .../forge-connections/{id}` | Deletes the connection and all its credential sets. Fails with 409 for the default connection (ADR 213 decision 8), and for a connection that a code location, a Catalog request home or a job that is not terminal names. | `namespace:manage-forge-connections` | the namespace |
| `POST .../forge-connections/{id}/make-default` | Makes this connection the default. A custom method (ADR 210 decision 5). | `namespace:manage-forge-connections` | the namespace |
| `POST .../forge-connections/{id}/credentials` | Adds a credential set. The server calls `Check` with it first, and refuses a set that fails. Answers the set ID, never the secret. | `namespace:manage-forge-connections` | the namespace |
| `DELETE .../forge-connections/{id}/credentials/{set ID}` | Removes a credential set. Fails with 409 for the last set. | `namespace:manage-forge-connections` | the namespace |
| `POST .../forge-connections/{id}/check` | Calls `Check` with each active set. Answers the account, the capabilities, and the missing capabilities for each consumer (`forge.Needs`). | `namespace:manage-forge-connections` | the namespace |
| `POST` or `PATCH` of a code location or a Catalog request home that names a connection (proposed, Q6) | Names the connection. The call also needs its own action on the unit. | `namespace:use-forge-connection` | the connection (`namespace:<ns>/forge-connection:<id>`) |
| `POST /forge-events/{hook ID}` | Webhook intake, above. | none (public list) | none |

The built-in roles: `admin` gets both actions. `maintainer` gets
`namespace:read-forge-connections` and, by default,
`namespace:use-forge-connection` (proposed, Q6). `namespace:manage-forge-connections`
follows the pattern of `namespace:manage-runners` and
`namespace:read-runners`. Who can name a connection in a code location is
Q6.

## Open questions for the owner

Q7 is not here: ADR 213 decision 7 decided it (see "Not in this RFC").

**Q1. Where does the port live?**

- A. In a new domain package `pkg/forge`, which owns the connections and
  is the consumer of the port. The wiring implements the narrow ports of
  `catalog`, `changeorder`, `source` and `job` over it. (proposed)
- B. No shared port. Each consumer package keeps its own narrow port, and
  each adapter implements all of them.
- C. The port in `pkg/job`, because write-back is the first consumer.

Recommendation: A. One adapter for each forge, one suite, and each
consumer keeps its narrow port. Benchmark: the Google Go style guide puts
an interface in the package that uses it. The `pkg/forge` service uses
the port. The consumers use their own narrow ports, which stay. C puts
connections that the Catalog and the source pipeline also use in the job
package.

**Q2. Do triggers and comment commands use webhooks, and is an event a
hint only?**

ADR 100 decision 15 already decides this for the review watcher: a forge
webhook gives the merge or the close, and the `job.review-watch` operation
polls the forge as the fallback for a lost webhook. The question is about
triggers (RFC 24) and comment commands (RFC 27).

- A. `ParseEvent` in the port, the route in the API adapter, each event a
  hint that the server checks with a read through the API, and a poll for
  each consumer that must not miss an event, as for the review watcher.
  (proposed)
- B. Events are facts: the server acts on the body of a valid event with
  no read.
- C. No webhooks. The server polls only.

Recommendation: A. Benchmark: GitHub does not send a failed delivery again
by itself, and Stripe tells receivers to get the object through the API.
So a design with no poll misses events, and a design with no read trusts a
body that the forge cannot replay. The reject metric supports the detection
of anomalies (SOC 2 CC7.2). This is a comparison, not a compliance claim.

**Q3. What does a connection with no `repo-token` give to a runner?**

- A. Nothing. `RepoToken` answers the connection token with `Scoped` not
  set, the source fetch of the server uses it, and the runner path refuses
  it. A pool in the git credential mode `forge` cannot use the connection,
  and the console says why. (proposed)
- B. Give the long-lived token of the connection to the step. ADR 130
  decision 11 excludes this: the `forge` mode answers a short-lived
  read-only token.
- C. A connection can hold a second, read-only token for runners.

Recommendation: A now, C as a later option. Benchmark: GitHub Actions
gives each job a `GITHUB_TOKEN` that only reaches its own repository and
expires when the job ends. ISO 27001:2022 control 8.2 (privileged access
rights) and SOC 2 CC6.3 (least privilege) refuse B.

**Q4. How does the GitHub adapter make a commit: as the person, or signed
as the app?**

- A. The Git database API. The person is the git author, as RFC 12
  proposes. The commit is not signed. A branch with the rule "require
  signed commits" refuses it.
- B. `createCommitOnBranch`. GitHub signs the commit as the app, and the
  person is in a `Co-authored-by` trailer and in the audit entry.
  (proposed)
- C. A setting of each connection.

Recommendation: B, if a spike confirms that a fine-grained-token commit
through `createCommitOnBranch` is signed. The spike tests the app and the
token auth kinds. If the commit of a token is not signed, the setting C
decides for a `token` connection. Benchmark: GitHub signs the commits that
it makes for its web editor, and the commits of `createCommitOnBranch` for
a GitHub App (spike). A branch with the GitHub rule "require signed
commits" refuses an unsigned commit, so A fails on such a branch. The port
already lets the adapter choose (`CommitRequest.Author` says so).

**Q5. Which way does a forge bridge connect?**

- A. The bridge dials out to the server and long-polls for work, as a
  runner does (ADR 131). It authenticates as runners do (ADR 133). The
  identity of the bridge is scoped to one namespace, as the identity of a
  runner pool is. (proposed)
- B. The server calls the bridge over HTTPS. The bridge serves a REST API
  that mirrors the port.
- C. Both.

Recommendation: A. It works in a network that allows no traffic into it,
and it reuses the runner protocol pattern. Benchmark: GitHub Actions
self-hosted runners and HCP Terraform agents only make outbound
connections. B adds a route from the server into the network of the team.

**Q6. Who can name a forge connection in a code location or a Catalog
request home?**

- A. Each person who can change the unit (`unit:manage`), for each
  connection of the namespace.
- B. A new action `namespace:use-forge-connection` on the object
  `namespace:<ns>/forge-connection:<id>`, which is needed in addition to
  the action on the unit. The route table lists it. (proposed)
- C. Each connection holds a list of repositories, and a code location can
  only name one of them.

Recommendation: B, with C as a later option. A connection can read every
repository that its credential reaches, so to name it is to pass its
access to a job. Benchmark: AWS asks for `iam:PassRole` to give a role to
a service, in addition to the action on the service. The built-in
`maintainer` role gets `namespace:use-forge-connection` by default.

**Q8. Who can run a comment command?**

- A. Only a forge account that a person linked to a principal from a
  console session, as for chat (ADR 146). The server then checks the
  actions of that principal. (proposed)
- B. Each account with write access to the repository on the forge, as
  Atlantis does.
- C. B and A together.

Recommendation: A. In each option, a comment command never approves, never
makes an approval intent or an approve link, and never confirms a gate. The
summary comment can link to the console page of the job, where the plan is on
screen. Each command runs as the linked principal with its actions (rule 8 of the
accepted decisions above). The command list is limited to commands that
start a plan or show a status (proposed). This drops `apply` and `unlock`
from RFC 27. RFC 89 answers this question for RFC 27. Benchmark: SOC 2 CC6.1 asks for
authentication and authorization by the system that acts. Atlantis trusts
the permission of the forge, and adds a team allowlist as an option.

**Q9. Who makes the event secret, and how does it rotate?**

The event secret is in the app registration. ADR 213 decision 7 says that the
server never answers a credential.

- A. The server makes a random secret when a person creates the connection
  or adds a credential set, and answers it once, so that the person can copy
  it into the webhook of the forge. This is an exception to "never
  answers". A replay of the create with the same `Idempotency-Key` answers
  409 (ADR 210 decision 6).
- B. The person gives the secret with the credentials, as the person gives the
  webhook secret of a GitHub App. The server never answers it. (proposed)
- C. A mix: B for an app, A for a `token` connection.

Rotation is the same for each option: a new event secret is added to the
registration, both secrets are active during the rotation, the server accepts an
event that matches either secret, and the person changes the secret in the
webhook of the forge before the person removes the old secret.

Recommendation: B, because it needs no exception to ADR 213 decision 7. The
cost is that the person makes a long random value. The console can offer a
generator in the browser. Benchmark: GitHub takes the webhook secret of an
app from the person (spike: it shows the value again or not). The owner
decides if A is worth an exception.

**Q10. Where do the events go after a promotion, or at a replica that does
not forward?**

After a promotion (ADR 248), the webhook URL of the forge still names the old
primary installation. A replica without request forwarding answers 421 to a
write (ADR 248 decision 7).

- A. The replica forwards the event to the primary installation, when
  forwarding is on.
- B. The operator changes the webhook URL in the forge at the promotion.
- C. The replica drops the event with 204 and the poll catches the change.
  (proposed)

Recommendation: C for the first version, because an event is a hint and each
consumer that must not miss an event has a poll (Q2). A later, when
forwarding exists. B stays an operator step in the runbook of a promotion.
Benchmark: GitHub does not send a failed delivery again by itself, so the
design already needs the poll.

**Q11. Which merge method does `MergeReview` use?**

The method is only proposed (see "Proposed methods"). The question does not
block this RFC: RFC 12 owns it, and it changes `job.verify`.

- A. The method is a parameter of the call, and the code location sets it.
  The default is a merge commit. (proposed)
- B. The default of the forge repository.
- C. Always a merge commit.

Recommendation: A. `job.verify` asks whether the commit of the state version
contains the delivered commit (ADR 100 decision 15). A squash merge or a
rebase merge makes new commits, so the check may report `unverified` for a
change that did land. A merge commit keeps the delivered commit. Benchmark:
GitHub offers the three methods in the API (`merge_method`), and a repository
can allow only some of them, so the call can be refused (`ErrRefused`).

## Hard to change later

- **The path `POST /forge-events/{hook ID}`.** Each configured webhook of a
  forge stores this address, so a change needs a change in the forge for each
  webhook. It follows the pattern of ADR 210 decision 2 for a path that an
  external system stores.
- **One hook ID for each app registration.** The hook ID is in the stored
  address of each webhook, so it does not change after the first webhook
  stores it.

## Not in this RFC

- **An operator-wide GitHub App (was Q7).** ADR 213 decision 7 decides it:
  each namespace registers its own app, and the private key is a sealed
  credential of the namespace. An operator-wide app needs an RFC against ADR
  213 decision 7 and ADR 220, when an operator asks for it.

## Proposed answers (PM)

One line for each open question, with its benchmark. The owner decides.

- **Q1: A.** The Google Go style guide puts an interface in the package that
  uses it.
- **Q2: A.** GitHub does not send a failed delivery again, so the server
  fetches the object through the API (the pattern of ADR 100 decision 15).
- **Q3: A, C later.** ADR 130 decision 11. GitHub Actions gives each job a
  `GITHUB_TOKEN` that reaches only its own repository.
- **Q4: B, if a spike confirms signed commits from a fine-grained token.**
  GitHub signs the commits that it makes for an app. The person is in the
  trailer and in the audit entry.
- **Q5: A.** GitHub self-hosted runners and HCP Terraform agents only make
  outbound connections.
- **Q6: B, with `maintainer` by default.** AWS asks for `iam:PassRole` in
  addition to the action on the service.
- **Q8: A.** Linked accounts only. A comment never approves.
- **Q9: B.** GitHub takes the app webhook secret from the person. This needs
  no exception to ADR 213 decision 7.
- **Q10: C.** Events are hints with a poll (ADR 248).
- **Q11: A, deferred to RFC 12.** GitHub offers `merge_method`. A merge commit
  keeps the check of `job.verify` exact.

## Outcome

Empty until the owner accepts the RFC (an ADR records the port shape, as
issue #886 asks before the GitHub adapter merges) or drops it.
