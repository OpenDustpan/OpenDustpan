# 271. The Forge port, Git host connections and the installation-level GitHub App

Status: accepted · 2026-10-20. Decisions 1 to 11 and 13 are owner decisions. The project manager decided details of decisions 8, 10, 12 and 13 on the same day, under the owner's delegation. Amends ADR 213 decision 7. Accepts RFC 89, except its questions 4 and 11

Changed by:

- ADR 286 changes: the words catalog and promotion become self-service and
  failover.
- ADR 299 changes decision 4: webhooks with three recovery paths.
- ADR 299 extends decision 7: more comment commands and a reaction.
- ADR 299 extends decision 2: new operations of the Forge port.
- ADR 300 changes decision 7: a third comment command, `dustpan plan`.
- ADR 303 extends decision 11: OpenDustpan is not a Git host. The Forge port
  connects to Git hosts for repositories, pull requests and statuses.
- ADR 336 extends: answers question 4, the author and the signature of a GitHub write-back commit, and the retry rule for a moved head.

## Context

Many accepted decisions use a Git host and name "the `Forge` port of RFC 12,
which a later ADR decides" (ADR 101 decision 3). No port existed, so each
consumer would get its own narrow port, and each adapter would implement all
of them. Nothing would test that the adapters agree.

[RFC 89](../rfc/0089-forge-port.md) proposed the port, the connection API and
the rules for credentials. The project owner decided each question of the RFC
on 2026-10-20, except two. Question 4 (how the GitHub adapter signs a commit)
waits for a signing spike. Question 11 (the merge method) belongs to RFC 12.
This ADR does not decide them.

ADR 213 decision 7 gave each namespace its own GitHub App. A GitHub App has
exactly one webhook URL and one webhook secret for all its installations. So
the URL and the secret cannot belong to one namespace when the installations
of the app serve many namespaces.

The code term is "forge". User-facing text never says "forge" (decision 11).

## Decision

1. **The port lives in `pkg/forge`, and that package owns the connections.**
   `pkg/forge` is a tool-neutral domain package. It holds the `Forge` port, the
   store port of the connections, the sealed credential sets, the default flag,
   and the service that picks a connection and opens it. The package imports no
   `net/http`, no SQL and no adapter. The adapters live in `pkg/forge/github`
   and, later, `pkg/forge/gitlab` and `pkg/forge/bridge`. They run the
   conformance suite in `pkg/forge/forgetest`. Each consumer keeps its own
   narrow port (`catalog.RequestFiles`, `changeorder.CauseDiffs`,
   `source.Fetcher`). The wiring implements them over the service.

   Benchmark: the Google Go style guide puts an interface in the package that
   uses it.
2. **One interface with capabilities.** `Opener` makes a `Forge` for one
   connection and one credential set, and makes no call to the Git host.
   `Forge` has `Capabilities`, `Check`, and the groups `Files`, `Commits`,
   `Reviews`, `Tokens`, `Events` and `Rotation`. Every adapter implements every
   method. A method that the Git host or the auth kind cannot do returns
   `ErrUnsupported`, and `Capabilities` does not list it. The capabilities are
   `files`, `compare`, `commit`, `review`, `git-access`, `repo-token`, `events`
   and `self-rotate`. A connection with no push access lists no `commit` and no
   `review`. The capability `repo-token` implies `git-access`. `Needs` gives the
   capabilities of each consumer, so that a feature that a connection cannot
   serve fails when a person sets it up, and not in a job. The port stays
   forge-neutral: no type, method or error in it exists only because one Git
   host needs it. The sentinel errors are `ErrNotFound`, `ErrConflict`,
   `ErrRefused`, `ErrAuth`, `ErrUnavailable`, `ErrTooLarge`, `ErrUnsupported`,
   `ErrSignature` and `ErrInvalid`. A rate limit is an `*UnavailableError` with
   the wait time that the Git host gave.

   Benchmark: a driver of the Kubernetes Container Storage Interface
   implements one service and lists its capabilities.
3. **No Git host credential reaches a runner for the source of its job.** The
   source comes only as the pack that the server builds (ADR 135). The
   capability `repo-token` stays only for the git credential mode `forge` of a
   runner step (ADR 130 decision 11), for other live reads of a repository. The runner path
   refuses a token with `Scoped` not set, so a long-lived token never leaves
   the server.
4. **An event is a hint.** `ParseEvent` is in the port, because each Git host
   signs its events in its own way. The HTTP route is in the internal API
   adapter. The route is `POST /webhooks/{hook ID}`. It is outside
   `/api/dustpan/v1` (ADR 210 decision 2), and its path has no word "forge".
   - The server gives each app registration a random hook ID. The hook ID is
     unique in the installation.
   - The route reads at most 2 MiB, finds the registration by the hook ID and
     checks the event against each active event secret of that registration. It
     answers 204 to each request, also for an unknown hook ID, a bad signature
     and a body that is too large. The metric `dustpan_git_host_event_rejected_total`
     counts the rejected events by reason.
   - The server drops a delivery ID that it saw for the same connection in the
     last 24 hours.
   - The server reads the object through the API before it changes a record.
     Each consumer that must not miss an event keeps a poll.
   - The intake runs in the `api` component. The effects run as durable
     operations in the `worker` component (ADR 249 decision 2).

   Benchmark: GitHub does not send a failed delivery again by itself. Stripe
   tells the receiver of an event to get the object through the API.
5. **A bridge dials out.** A forge bridge is a small process that a team runs
   next to a version control system that the server cannot reach. It dials out
   to the server and long-polls for work, as a runner does (ADR 131). It
   authenticates as a runner does (ADR 133), and its identity is scoped to one
   namespace. The bridge adapter and its `Kind` value enter Go with the bridge.
6. **Naming a connection needs its own action.** The action
   `namespace:use-git-host-connection` on the object
   `namespace:<ns>/git-host-connection:<id>` is necessary, in addition to the
   action on the unit, when a code location or a Catalog request home names a
   connection. The built-in role `maintainer` has
   `namespace:read-git-host-connections`, and it has
   `namespace:use-git-host-connection` when that action enters the code. The
   actions `namespace:read-git-host-connections` and
   `namespace:manage-git-host-connections` follow the pattern of
   `namespace:read-runners` and `namespace:manage-runners`. The role `admin`
   has all three.

   Benchmark: AWS asks for `iam:PassRole` to give a role to a service, in
   addition to the action on the service.
7. **Comment commands.** `dustpan apply` and `unlock` are the comment commands
   of a review request. Only an account that a person linked to a principal
   can run one (ADR 146), and the command runs as that principal with its
   actions. `dustpan apply` applies the exact plan of the summary comment for
   the current head when all of these are true: the feature is on for the code
   location (it is off by default), the review request can merge under the
   rules of OpenDustpan and of the Git host, and the principal can apply the
   unit. The server reads the review request and the comment through the API
   before it acts, and it refuses when the head moved or the summary comment
   changed. A comment never approves, never makes an approval intent or an
   approve link, and never passes a gate. This ADR records the rule. The
   commands enter the code with RFC 27.
8. **The person or IaC gives the event secret.** The event secret belongs to
   the app registration, not to a credential set or a connection. A person can
   set it in a write-only field of the console form, or IaC can set it. The
   server never answers it. A read answers only that a secret is set, with its
   set ID and its creation time. The server refuses an event secret of fewer
   than 32 bytes with 400, and never stores an empty secret. A registration
   keeps two active event secrets during a rotation. An event is valid when it
   matches either one. The first release keeps one event secret for each
   registration and replaces it in one call. The rotation with two active
   secrets is later work, and it enters the code with the webhook route. A
   provider of OpenDustpan for OpenTofu or Pulumi marks the secret write-only and
   sensitive.
9. **A replica drops an event that it cannot serve, and the poll catches the
   change.** The hook URL uses a stable hostname that each federation member
   accepts, for example with DNS failover or a global load balancer. It never
   uses the FQDN of one member, so a promotion (ADR 248) does not change the
   URL that the Git host stores. A member that receives an event for a
   namespace whose primary installation is another member answers 204 and drops
   the event. This holds until the forwarding effort forwards webhook events.
   Forwarding is part of the definition of done of that effort. An operator can
   configure the members of a federation to share the installation-level hooks:
   the same app registration, hook ID and event secret on each member.
10. **One installation-level GitHub App is the default. A per-namespace GitHub
    App is an option.** This amends ADR 213 decision 7, which gave each
    namespace its own app.
    - The operator registers the installation-level app one time. Its private
      key and its event secret are sealed credentials of the installation, not
      of a namespace. The operator rotates them on the registration.
    - The operator assigns each GitHub App installation, by its installation
      ID, to one namespace. The installation-to-namespace map is
      installation-wide data. A namespace backup does not hold it, and only the
      operator changes it.
    - A connection of the installation-level app names an installation ID that
      the operator assigned to its namespace. It holds no credential set and no
      event secret.
    - A per-namespace app has its private key and event secret as sealed
      credentials of the namespace (ADR 213 decision 7). They travel with the
      namespace backup. The create call refuses with 409 the app ID of a
      per-namespace app that a connection of another namespace uses, and the
      app ID of the installation-level app. One app ID has one owner.
    - A move of a namespace between installations that are not one federation
      remaps the installation-level app: the operator of the target assigns the
      installation to the namespace there. A per-namespace app moves with the
      namespace.
    - The routes of the registration and of the map belong to the installation
      API, with installation-level actions.

    Benchmark: GitHub allows exactly one webhook URL and one webhook secret for
    each GitHub App.
11. **User-facing text says "GitHub" or "Git host", never "forge".** The paths
    are `/api/dustpan/v1/namespaces/{namespace}/git-host-connections` and its
    sub-paths. The object kind is `git-host-connection`. Error text names the
    service when the adapter knows its kind, for example "GitHub refused the
    credential". A sentinel of the port may say "Git host". The console label
    and the title of a documentation page name the service when the kind is
    known. The metrics are `dustpan_git_host_event_rejected_total`,
    `dustpan_git_host_connection_refused_total` and `dustpan_git_host_credential_fallback_total`.
    The last one replaces the name `forge_credential_fallback_total` of ADR 213
    decision 7. The wire value of the kind (`github`, `gitlab`, `forgejo`) stays.
12. **A credential is bound to its address.** A person with
    `namespace:manage-git-host-connections` must not be able to send a
    credential to an address of their choice. Each rule below stops the attack
    alone, and none relies on another.
    1. A connection of the installation-level app takes its API address and its
       app ID from the registration of the operator. A request of a namespace
       that holds either field fails with 400. The create call sets
       `installation_id`, which the operator must have assigned to the
       namespace. No later call changes it: a `PATCH` that holds it fails with
       400, and a new connection points at another installation.
    2. For a `token` connection and a per-namespace app, a change of the API
       address or of a parameter of the auth kind needs a new credential set in
       the same call. For a new app ID it also needs a new event secret. The
       server calls `Check` with the new set at the new address first, and
       refuses the whole call when `Check` fails. Then it stores the new set
       and the new values, and removes the old sets, in one transaction.
    3. The server seals each credential set with a digest of the API address
       and the auth parameters for which the set was checked. The digest is the
       associated data of the AEAD that seals the set. `Open` computes the digest
       from the values that the registration holds now, and the set opens only
       with that digest. A writer of the database who changes the address, and
       the digest next to it, still cannot open the set for the new address.
       `Open` also refuses a stored digest that differs from the values now, and
       fails closed.
    4. For a connection of the installation-level app, `Open` reads the map at
       each call. When the map does not assign the installation ID to the
       namespace, or the service cannot read the map, `Open` fails with an error
       that wraps `ErrAuth`. The fallback to an older set does not apply. The
       service writes an audit entry and counts
       `dustpan_git_host_connection_refused_total{reason="installation-not-assigned"}`.
       A reassignment by the operator stops the old namespace at once.
    5. The API address passes the outbound address check at set time (create,
       `PATCH` and `check`) and again at each call, with the address that the
       name resolves to. The check refuses a loopback, link-local, private,
       unspecified and multicast address, the unique-local range of IPv6,
       100.64.0.0/10, and the metadata addresses of the cloud providers. An
       installation setting of the operator can allow a range, for GitHub
       Enterprise Server on a private network. A range opens private-network
       addresses only: RFC 1918 and the unique-local range of IPv6. It never
       opens a loopback, link-local, metadata, unspecified or multicast address,
       also inside a range, and it never skips another check. The server refuses
       a range with fewer than 8 bits for IPv4 or fewer than 32 bits for IPv6,
       and so refuses `0.0.0.0/0` and `::/0`. The scheme is `https`. The
       adapter follows no redirect to another host, and each redirect that it
       follows passes the same check.
    6. `check` asks GitHub for an installation token with the smallest
       permission, read of the metadata, and revokes the token after the call.
       The server limits `check` to one call in 10 seconds for each connection
       and 30 calls in a minute for each installation. A call over the limit
       answers 429 with a `Retry-After` header, which is not more than 60
       seconds, and makes no call to GitHub. The state of the limit is in the
       memory of one server.
    7. A request to create or to check a connection whose installation the map
       does not assign to the namespace answers 409 with the code
       `git-host.installation-not-assigned`.
    A namespace can remove its only connection. A default
    connection is needed only while a namespace has a connection, so the rule of
    ADR 213 decision 8 ("the removal of the default connection fails until another
    connection is the default") applies while another connection exists. This
    ADR reads that decision this way.
13. **Credential expiry and rotation.** The GitHub App is the recommended
    connection, because its tokens are short-lived. The server records the
    expiry of a token credential when a person adds a set, at each `Check`, and
    at a periodic check. The API and the console show it. A metric and a WARN log
    line tell the operator 30 days and 7 days before the expiry. The server
    flags a token with no expiry. GitHub has no API that rotates a fine-grained
    token, so for GitHub the server shows the expiry and warns only. A GitLab
    token rotates through its `self/rotate` endpoint, as a durable operation
    with these rules:
    1. A connection has at most one rotation at a time. "Rotate now" while one
       runs answers 409, and a scheduled rotation that finds one running makes
       no call.
    2. Before the call, the operation writes a "rotation started" mark with the
       ID of the old set, in its own transaction.
    3. The step that receives the new token seals and stores it, and clears the
       mark, in one transaction.
    4. The operation never retries with the old token, and `RotateSelf` never
       uses the older-set fallback. GitLab revokes the old token at once, and a
       second rotation with a revoked token makes GitLab revoke the whole token
       family.
    5. On resume, a mark with no new set marks the connection "needs a new
       credential" and fires an alert. It makes no second call.

## Consequences

- The first implementation adds the connection store, the registration of the
  installation-level app, the installation map, the routes of RFC 89 and the
  GitHub adapter for `Check`. Webhook intake, write-back, tokens for runners,
  GitLab, rotation and the file, commit and review methods follow in later
  changes.
- A GitHub setup guide and a federation setup guide document the scope of the
  GitHub App and the stable hook hostname, as decisions 9 and 10 give them.
- ADR 213 decision 7 stays valid for sealing, rotation with two active sets,
  and the audit entry. Its sentence that each namespace has its own app is
  replaced by decision 10, and its metric name by decision 11.
- Question 4 stays open: it asks how the GitHub adapter signs a commit. Question
  11 stays with RFC 12: it asks which merge method `MergeReview` uses. Neither
  question blocks the port.
- Each rule of decision 12 and each step of ADR 250 needs its own test with
  hostile input sent straight to that step.
- A later change renames the value `forge` of the git credential mode of a
  runner pool before the first release.
