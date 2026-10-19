---
rfc: 87
title: The Forge port, what each consumer needs from a forge, and the forge connection API
status: discussing
created: 2026-10-19
---

# RFC 87. The Forge port, what each consumer needs from a forge, and the forge connection API

Status: discussing. An agent wrote this RFC for issue #886 under the
direction of the project manager. It proposes the shape of the `Forge` port
before the GitHub adapter. The pull request of this RFC also adds the port
as Go interfaces in `pkg/forge` and an empty conformance suite in
`pkg/forge/forgetest`. It adds no adapter, no SQL and no route. The
proposals have the label "(proposed)". The owner answers the questions at
the end.

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

## The consumers, and what each needs from a forge

| Consumer | When | What it needs from the forge | Port methods |
| --- | --- | --- | --- |
| Write-back job (ADR 101) | The deliver phase, the takeover, the review watcher, `job.abandon`, `job.verify` | The head of the ref of the code location. Each edited file at the base commit. One commit of many files, with a check that no edited file changed after the base. A search for the commit with the trailer of the job. A review request that a second open call does not duplicate. The state of the review request, with the merge time and the merge commit. A close with no merge. An ancestor check for `job.verify`. | `Head`, `ReadFile`, `Commit`, `FindCommit`, `OpenReview`, `ReadReview`, `CloseReview`, `Compare` |
| Catalog request files (ADR 116) | A render of a request in git | One file at one commit. | `ReadFile` |
| Change order cause diff (ADR 196) | Each read of the review | The file diff from base to head, with a byte limit, and an ancestor check. | `Compare` |
| Source pipeline (ADR 135, ADR 130) | The `source.fetch` operation, and a runner step in the git credential mode `forge` | A token for the git protocol that reads one repository only and expires soon. | `RepoToken` |
| Review request feedback (RFC 27) | After a plan, and after an apply | A status on the head commit for each unit. One summary comment that the server edits in place. A merge at an expected head, for "apply before merge" (RFC 12). | `SetStatus`, `PutComment`, `MergeReview` |
| Triggers (RFC 24) | A push, a tag, a review request that opens or changes its head | Signed events. The changed paths from the old head to the new head, to find the affected units. | `ParseEvent`, `Compare`, `Head` |
| Comment commands (RFC 27) | A new comment on a review request | A signed event. A read of the comment through the API, with the stable ID of its author, before the server acts. A reply. | `ParseEvent`, `ReadComment`, `PutComment`, `ReadReview` |

`forge.Needs` holds the same map in code, as capabilities (below). The
server checks it when a feature names a connection, so that a feature that
a connection cannot serve fails at configuration time, not in a job.

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
	Reviews  // OpenReview, ReadReview, CloseReview, MergeReview
	Feedback // SetStatus, PutComment, ReadComment
	Tokens   // RepoToken
	Events   // ParseEvent
}
```

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
| `merge` | `MergeReview` |
| `status` | `SetStatus` |
| `comment` | `PutComment`, `ReadComment` |
| `repo-token` | `RepoToken` |
| `events` | `ParseEvent` |

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
| `Commit(repo, req)` | One commit of 1 to 64 file changes (at most 4 MiB) on `req.Branch`, all or none. With `NewBranch`, it creates the branch at `req.Base`. | `ErrConflict` when a changed file differs at the head of the branch from `req.Base`, or a new branch exists with another head. `ErrRefused` for branch protection or a missing permission. `ErrInvalid` and `ErrTooLarge` from `ValidateCommit`, with no call. |
| `FindCommit(repo, ref, trailer, since)` | The newest commit on `ref`, at or after `since`, whose message has the trailer line. | `ErrNotFound` |
| `OpenReview(repo, req)` | A review request from `req.Branch` into `req.Target`. An open review request of the same branch is returned, not duplicated. | `ErrNotFound` for a missing branch, `ErrRefused` |
| `ReadReview(repo, id)` | The state (`open`, `merged`, `closed`), the head, and the merge time and merge commit after a merge. | `ErrNotFound` |
| `CloseReview(repo, id)` | Closes with no merge. A closed one returns nil. | `ErrConflict` for a merged one |
| `MergeReview(repo, id, head)` | Merges only when the head is `head`. | `ErrConflict` for another head or a merge conflict, `ErrRefused` for a rule of the forge |
| `SetStatus(repo, commit, s)` | Sets the status with the name `s.Name`. A second call with the same name replaces it. | `ErrNotFound` |
| `PutComment(repo, reviewID, key, body)` | Writes the comment that `key` names. The first call creates it. A later call edits it in place. | `ErrNotFound` |
| `ReadComment(repo, reviewID, id)` | The comment as the forge holds it now, with the stable ID of its author. | `ErrNotFound` for a missing or deleted comment |
| `RepoToken(repo)` | A token that reads only `repo` and expires in at most 1 hour. | `ErrUnsupported` when the auth kind cannot limit a token. The server never gives a broader token in its place. |
| `ParseEvent(r)` | Checks the signature or token of an event with the event secret, and returns a forge-neutral event: `ping`, `push`, `review` or `comment`, with the delivery ID. No call to the forge. | `ErrSignature`, `ErrInvalid`, `ErrUnsupported` for another kind |

The sentinels are `ErrNotFound`, `ErrConflict`, `ErrRefused`, `ErrAuth`,
`ErrUnavailable`, `ErrTooLarge`, `ErrUnsupported`, `ErrSignature` and
`ErrInvalid`. The wiring maps `ErrUnavailable` to
`catalog.ErrForgeUnavailable` and `changeorder.ErrForgeUnavailable`, and
`ErrConflict` and `ErrRefused` to a failed write-back job (ADR 101
decision 3). The forge APIs answer "not found" for a repository that the
credential cannot see, so `ErrNotFound` covers both, as the forge does.

**The conflict rule of `Commit`.** ADR 101 decision 3 fails a write-back
only when an edited key changed after the base. The port checks at the
file level: a commit fails with `ErrConflict` only when a file of the
change differs at the head of the branch from the base. A change of
another file on the branch is not a conflict. The write-back executor then
reads the file again and checks the keys, as ADR 101 decision 3 says.

### The conformance suite

`pkg/forge/forgetest.Run` takes a factory of a `Harness`: the `Forge`
under test, a fresh repository, and a `Fixture` that changes the
repository outside the port (a push by another author, branch protection,
a merge by a person, a comment, a signed event, a revoked credential).
Each adapter runs it against a recorded or a fake API of its forge, as
issue #886 asks for GitHub.

The suite lists 45 table-driven cases (`forgetest.CaseNames`). Each case
has no body yet and skips with "not implemented". A case gets its body
before an adapter implements the methods that it covers (CLAUDE.md: a new
port method gets conformance tests before any adapter implements it). A
case of a capability that the forge does not list skips for that forge.
The case "an operation outside the capabilities is unsupported" checks
that such a method returns `ErrUnsupported`. The case "no error and no
event holds a part of the secret" puts a canary secret in the credential
and checks each error text.

## How the auth kinds map to the port

A connection has a kind (`github`, `gitlab` or `bridge`), an auth kind
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
`Capabilities` does not list `repo-token` for this auth kind, and a pool
in the git credential mode `forge` cannot use the connection (Q3). The
source fetch of the server still works: the token stays in the server.

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

**GitHub, the rest.** `MergeReview` uses
`PUT /repos/{owner}/{repo}/pulls/{pull_number}/merge` with `sha`, which
answers 409 when the head moved. `SetStatus` uses the commit status API
with `context`, which works for both auth kinds. The Checks API needs a
GitHub App, so the port does not use it in the first version. Events carry
`X-Hub-Signature-256` (HMAC-SHA256 of the body) and `X-GitHub-Delivery`.
GitHub does not send a failed delivery again by itself. So the review
watcher keeps its poll (ADR 100 decision 15), and triggers need one too
(Q2). Source:
<https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries>.

**GitHub Enterprise Server** is the same adapter with another API address.

## How GitLab, Forgejo and a bridge fit

**GitLab (`token`).** The sealed secret is a group or project access
token with the scope `api`. The commits API takes many `actions` with
`last_commit_id` for each file, and `start_sha` for a new branch, so it
gives the conflict rule of `Commit` in one call. The merge API takes
`sha`. Statuses use the commit status API with `name`. Comments are
notes of the merge request. Events carry `X-Gitlab-Token`, which is the
secret in plain text, not a signature of the body (spike: newer GitLab
releases may sign webhooks). GitLab has no call that makes a token for
one repository and read access from a group access token (spike). So
`repo-token` needs a second, read-only project access token, or stays
off (Q3). Sources:
<https://docs.gitlab.com/api/commits/>,
<https://docs.gitlab.com/api/merge_requests/>,
<https://docs.gitlab.com/user/project/integrations/webhooks/>.

**Forgejo (spike only).** The facts below come from the API documentation.
The spike checks each of them. `POST /repos/{owner}/{repo}/contents`
changes many files in one commit, with the blob `sha` of each file it
replaces and `new_branch`. Pull requests merge with
`head_commit_id`. Events carry `X-Forgejo-Signature` (HMAC-SHA256). An
access token has scopes for each kind of object, not for each
repository, so `repo-token` stays off. Source:
<https://forgejo.org/docs/latest/user/api-usage/>.

**A forge bridge (proposed).** A bridge is a small process that a team
runs next to its version control system. It implements the operations of
the port on that side, and speaks one protocol of OpenDustpan to the
server. The server has one adapter for it, `pkg/forge/bridge`. The bridge
dials out to the server, and asks for work with long polling, as a runner
does (ADR 131). So a network that allows no traffic into it can still
use write-back, and the server needs no route to the version control
system. Q5 asks the owner. A bridge in front of a plain git server can use
a git client on its own side: rule 1 above is about the server, and the
bridge is the API of that system. A plain git server has no review
request, so a bridge for it lists no `review`, and write-back can only
push directly.

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
| `merge` at an expected head | apply before merge | yes | yes | yes | yes (spike) | bridge |
| `status` | feedback | yes | yes | yes | yes (spike) | bridge |
| `comment` (edit in place, read with author ID) | feedback, comment commands | yes | yes | yes | spike | bridge |
| `repo-token` (one repository, read, at most 1 hour) | source fetch on a runner, git mode `forge` | yes | no | no (spike) | no (spike) | no |
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
  answers it.
- The route reads at most 2 MiB of body (proposed), finds the connection
  by the hook ID, and calls `ParseEvent`. It answers 204 to each request,
  also for an unknown hook ID and a bad signature. The metric
  `forge_event_rejected_total` counts the rejects by reason and connection.
- The server drops a delivery ID that it saw for the same connection in
  the last 24 hours.
- **An event is a hint** (proposed). The server reads the object through
  the API before it changes a record: `ReadReview` before the review
  watcher records a merge, `ReadComment` before a comment command, `Head`
  before a trigger starts a job. So a forged or replayed event can cause
  one more read, and nothing else. Stripe tells a receiver of its events
  the same: the order is not promised, and the receiver can get the
  object through the API. Source: <https://docs.stripe.com/webhooks>.
- The event secret of a `token` connection is a random value that the
  server makes when a person creates the connection. The server answers
  it once, in the create answer, so that the person can copy it into the
  webhook of the forge. A replay of the create with the same
  `Idempotency-Key` then answers 409 with
  `request.idempotency-key-replayed-secret` (ADR 210 decision 6). The
  event secret of a GitHub App is the webhook secret of the app, which
  the person gives with the credentials.

## The forge connection REST API

All routes are under `/api/dustpan/v1/namespaces/{namespace}/forge-connections`
(ADR 210 decision 3). Each mutating route takes `Idempotency-Key` (ADR 210
decision 6). Each write checks the namespace fence and writes an audit
entry with no part of a credential (ADR 213 decision 7).

| Method and path | Does | Action | Object |
| --- | --- | --- | --- |
| `GET .../forge-connections` | Lists the connections: ID, kind, auth kind, API address, parameters that are not secret, push flag, default flag, hook ID, and for each credential set its ID and creation time. Never a secret. | `namespace:read-forge-connections` | the namespace |
| `GET .../forge-connections/{id}` | Reads one connection, as in the list. | `namespace:read-forge-connections` | the namespace |
| `POST .../forge-connections` | Creates a connection with its first credential set. The first connection of a namespace becomes the default. Answers the event secret once for `token`. | `namespace:manage-forge-connections` | the namespace |
| `PATCH .../forge-connections/{id}` | Changes the API address, the parameters or the push flag. Never a credential. | `namespace:manage-forge-connections` | the namespace |
| `DELETE .../forge-connections/{id}` | Deletes the connection and all its credential sets. Fails with 409 for the default connection (ADR 213 decision 8), and for a connection that a code location, a Catalog request home or a job that is not terminal names. | `namespace:manage-forge-connections` | the namespace |
| `POST .../forge-connections/{id}/make-default` | Makes this connection the default. A custom method (ADR 210 decision 5). | `namespace:manage-forge-connections` | the namespace |
| `POST .../forge-connections/{id}/credentials` | Adds a credential set. The server calls `Check` with it first, and refuses a set that fails. Answers the set ID, never the secret. | `namespace:manage-forge-connections` | the namespace |
| `DELETE .../forge-connections/{id}/credentials/{set ID}` | Removes a credential set. Fails with 409 for the last set. | `namespace:manage-forge-connections` | the namespace |
| `POST .../forge-connections/{id}/check` | Calls `Check` with each active set. Answers the account, the capabilities, and the missing capabilities for each consumer (`forge.Needs`). | `namespace:manage-forge-connections` | the namespace |
| `POST /forge-events/{hook ID}` | Webhook intake, above. | none (public list) | none |

The built-in roles: `admin` gets both actions. `maintainer` gets
`namespace:read-forge-connections` (proposed). `namespace:manage-forge-connections`
follows the pattern of `namespace:manage-runners` and
`namespace:read-runners`. Who can name a connection in a code location is
Q6.

## Open questions for the owner

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

**Q2. Is webhook intake in the port, and is an event a hint only?**

- A. `ParseEvent` in the port, the route in the API adapter, each event a
  hint that the server checks with a read through the API, and a poll for
  each consumer that must not miss an event. (proposed)
- B. Events are facts: the server acts on the body of a valid event with
  no read.
- C. No webhooks. The server polls only.

Recommendation: A. Benchmark: GitHub does not send a failed delivery again
by itself, and Stripe tells receivers to get the object through the API.
So a design with no poll misses events, and a design with no read trusts a
body that the forge cannot replay. SOC 2 CC7.2 (detect anomalies) is met
by the reject metric.

**Q3. What does a connection with no `repo-token` give to a runner?**

- A. Nothing. A pool in the git credential mode `forge` cannot use the
  connection, and the console says why. The source fetch of the server
  still uses the credential, which stays in the server. (proposed)
- B. Give the long-lived token of the connection to the step.
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

Recommendation: B. Benchmark: GitHub signs the commits that it makes for
its web editor, and the commits of `createCommitOnBranch` for a GitHub App
(spike). A branch with the GitHub rule "require signed commits" refuses an
unsigned commit, so A fails on such a branch. The port
already lets the adapter choose (`CommitRequest.Author` says so).

**Q5. Which way does a forge bridge connect?**

- A. The bridge dials out to the server and long-polls for work, as a
  runner does (ADR 131). It authenticates as runners do (ADR 133).
  (proposed)
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
- B. A new action `namespace:use-forge-connection`, which is needed in
  addition to the action on the unit. (proposed)
- C. Each connection holds a list of repositories, and a code location can
  only name one of them.

Recommendation: B, with C as a later option. A connection can read every
repository that its credential reaches, so to name it is to pass its
access to a job. Benchmark: AWS asks for `iam:PassRole` to give a role to
a service, in addition to the action on the service.

**Q7. Who registers a GitHub App: each namespace, or the operator of the
installation?**

- A. Each namespace registers its own app. The private key is a sealed
  credential of the namespace, as ADR 213 decision 7 says. (proposed for
  the first version)
- B. The operator registers one app for the installation. A namespace
  connection holds only an installation ID. The private key is a secret
  of the installation.
- C. Both. B comes later, for OpenDustpan Cloud.

Recommendation: A now, C later. With B, the server must prove that the
person who connects an installation administers that GitHub account. A
setup URL with an installation ID can be forged. GitHub tells an app to
check the installation with a user access token
(`GET /user/installations`). B also shares one key across namespaces,
which ADR 220 must accept first. Benchmark: HCP Terraform offers a shared
GitHub App and per-organization VCS providers. Source:
<https://docs.github.com/en/apps/creating-github-apps/about-creating-github-apps/best-practices-for-creating-a-github-app>.

**Q8. Who can run a comment command?**

- A. Only a forge account that a person linked to a principal from a
  console session, as for chat (ADR 146). The server then checks the
  actions of that principal. A comment never approves (ADR 225 decision
  7). (proposed)
- B. Each account with write access to the repository on the forge, as
  Atlantis does.
- C. B and A together.

Recommendation: A, and the command list of RFC 27 is limited to what a
chat action can do. Benchmark: SOC 2 CC6.1 asks for authentication and
authorization by the system that acts. Atlantis trusts the permission of
the forge, and adds a team allowlist as an option.

## Outcome

Empty until the owner accepts the RFC (an ADR records the port shape, as
issue #886 asks before the GitHub adapter merges) or drops it.
