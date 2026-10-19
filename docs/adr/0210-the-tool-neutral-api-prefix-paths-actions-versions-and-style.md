# 210. The tool-neutral API: one prefix, resource paths, action names, versions and a style guide

Status: accepted · 2026-10-18. Accepts RFC 59. Supersedes in part the route paths and the `server:` action names of earlier ADRs (see "What this ADR replaces"). ADR 247 extends decision 3

## Context

Decided by the project manager under the owner's decision rule, 2026-10-18.
The rule: the project manager decides a question when Google, AWS, PCI DSS,
SOC 2 or ISO 27001 would answer it the same way, when no persona is upset or
an escape hatch exists, and when the answer is not stupid on a step back.
Decision 6 is an owner decision of 2026-10-18.

[RFC 59](../rfc/0059-installation-api.md) asks for the shape of the
tool-neutral API. ADR 17 gives OpenDustpan's own objects a REST API with its
own route prefix. On 2026-10-12 the project owner answered RFC 59: the
prefix is "`api/dustpan/v1` or something to that effect", names follow
"industry norms", the shapes must be "as ergonomic and standard compared
with industry as possible", and versions follow "industry standards".

The facts on `main`:

- The tool-neutral routes sit under many prefixes: `/api/server/`,
  `/api/inventory/`, `/api/catalog/`, `/api/change-orders/`, `/api/jobs/`,
  `/api/runners/`, `/api/agent/`, `/api/context/`, `/api/unmanaged/`,
  `/api/namespaces/`, `/api/calendars/`, `/api/me/` and others. Some put the
  namespace after the area (`/api/runners/{namespace}/pools`), some put it
  under `namespaces` (`/api/namespaces/{ns}/budgets`).
- Unit routes name a unit by its tool and its name or ID, for example
  `/api/inventory/units/{tool}/{unit_id}/dependents`. A unit ID is unique in
  the installation.
- The tool-compatible APIs own other paths: the Pulumi Cloud-compatible API
  (`/api/stacks/...`, `/api/user`, `/api/capabilities` and others), the
  TFE-compatible API (`/api/v2/...`) and the OpenTofu HTTP backend
  (`/tofu/state/{namespace}/{workspace}`). The real CLIs send these paths,
  and we cannot change them.
- Some OpenDustpan routes sit inside a tool prefix but no CLI sends them,
  for example the restore routes of ADR 28 decisions 3 and 5
  (`POST /api/v2/state-versions/{state_version_id}/actions/restore` and
  `POST /api/stacks/{namespace}/{project}/{stack}/restore/{version}`).
- Some endpoints are stored by an external system: the runner protocol
  `/runner/v1` (ADR 131, ADR 132), which a runner binary calls, and the
  sign-in callback `/api/server/sign-in/{provider}/callback`, which each
  identity provider stores as a redirect URI.
- 23 actions have the area `server:`, for example `server:backup` and
  `server:observe`. The other areas name an object kind: `namespace:`,
  `unit:`, `job:`, `changeorder:`, `caller:` and others.
- ADR 182 gives the error shape of the tool-neutral APIs: `{"code",
  "message", "errorCode", "errorArgs"}`.
- No tag exists. ADR 73 lets every API except the tool-compatible APIs
  change and break before 1.0.

## Decision

1. **One prefix for the tool-neutral REST API.** Each route of the
   tool-neutral REST API moves under `/api/dustpan/v1/`. This includes the
   routes that the console uses. The move lands before the first tag. The
   server keeps no alias and no redirect for an old path of the REST API,
   because no tag shipped one (ADR 73).

   Benchmark: Google AIP-185 puts the major version of an API in its path.
2. **Paths that stay outside `/api/dustpan/v1`.** A protocol endpoint that
   an external system stores (a CLI, a runner, an IdP, a cloud trust
   policy) is not part of `/api/dustpan/v1` and has no API version. Such a
   path never moves with a major version of the REST API. Its own protocol
   versions it, when it has versions.

   | Path | Who stores it | Rule |
   | --- | --- | --- |
   | `/api/...` of the Pulumi Cloud-compatible API | the Pulumi CLI | no change |
   | `/api/v2/...` of the TFE-compatible API | OpenTofu | no change |
   | `/tofu/state/{namespace}/{workspace}` (the OpenTofu HTTP backend) | the `backend "http"` block | no change |
   | `/.well-known/terraform.json` | OpenTofu service discovery | no change |
   | the probe paths (ADR 34) | the orchestrator | no change |
   | `/runner/v1/...` (ADR 131, ADR 132) | the runner binary | no change. The runner protocol has its own version. |
   | `/oidc/{namespace ID}/.well-known/openid-configuration` and `/oidc/{namespace ID}/jwks` | a cloud trust policy | as ADR 204 gives them |
   | `/sign-in/{id}/callback` | the identity provider, as a redirect URI | moves once from `/api/server/sign-in/{id}/callback`, with no alias (below) |
   | `/mcp` | the configuration of an MCP client | moves once from `/api/agent/mcp`. MCP has its own protocol version. |
   | `/metrics` | the scrape configuration of a Prometheus server | moves once from `/api/server/metrics`. The action of ADR 32 stays. |
   | `/chat/{tool}/actions` | the app configuration of a chat tool | moves once from `/api/chat/{tool}/actions` (ADR 146) |

   **The sign-in callback.** The callback moves one time, now, from
   `/api/server/sign-in/{id}/callback` to `/sign-in/{id}/callback`. It
   never moves with a major version. The server keeps no alias for the
   old path, because no tag shipped it (ADR 73). The start of a sign-in
   is a route of the REST API (`/api/dustpan/v1/sign-in/{id}`), because
   only the console calls it.

   Benchmark: OAuth 2.0 redirect URIs match exactly (RFC 6749 section
   3.1.2), so the identity providers keep them stable. Kubernetes keeps
   its discovery and probe paths outside its versioned API groups.
3. **Resource-oriented paths.** A path names a collection or a resource in
   it. Collection names are plural nouns, in the kebab-case that the earlier
   ADRs use (`change-orders`, `freeze-schedules`). No path holds the word
   "server".

   | Object | Path |
   | --- | --- |
   | An object of the installation | `/api/dustpan/v1/<collection>[/{id}]`, for example `/api/dustpan/v1/backups/{id}`, `/api/dustpan/v1/groups/{id}`, `/api/dustpan/v1/calendars/{id}` |
   | An object of a namespace | `/api/dustpan/v1/namespaces/{namespace}/<collection>[/{id}]`, for example `.../namespaces/{namespace}/change-orders/{id}`, `.../namespaces/{namespace}/runner-pools/{id}` |
   | A unit | `/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}` |
   | An object of a unit | `/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/<collection>[/{id}]` |
   | An object of the caller | `/api/dustpan/v1/me/<collection>`, for example `/api/dustpan/v1/me/preferences` |

   A unit path holds the unit ID, never `{tool}/{name}`. The unit ID is
   unique in the installation and never changes. A rename of a stack or a
   workspace does not change it. The tool of a unit is a field of the unit
   and a filter of a list (`?tool=pulumi`), not a path segment.

   Benchmark: Google AIP-121 (resource-oriented design) and AIP-122
   (resource names with the parent first, and an ID that does not change
   when a name changes). AIP-122 writes collection names in camelCase. This
   ADR keeps kebab-case, because each earlier ADR and the console use it,
   and a path segment is not a JSON field.
4. **Reads across namespaces use the wildcard `-`.** A list across
   namespaces puts `-` in the place of the namespace:
   `GET /api/dustpan/v1/namespaces/-/units` lists every unit that the
   caller can read, in each namespace. The answer holds only the items
   that the caller can read, and each item names its namespace. A get by
   unit ID also works through `namespaces/-`, for example
   `GET /api/dustpan/v1/namespaces/-/units/{unit_id}`, because the unit ID
   is unique in the installation. A write never takes `-`: a write names
   its namespace, because the fence of the namespace guards it.

   Benchmark: Google AIP-159 (reading across collections with the
   wildcard `-`).
5. **Custom methods use a sub-path segment.** An operation that is not a
   create, a read, an update or a delete is a `POST` on a sub-path of its
   resource, for example `.../backups/{id}/check`,
   `.../deleted-units/{unit_id}/restore` and `.../change-orders/{id}/verdict`.
   The verb is a kebab-case segment of its own. The Go `ServeMux` matches
   whole path segments only, so it cannot match the `{id}:verb` form of
   Google AIP-136.

   Benchmark: Google AIP-136 (custom methods). The GitHub REST API and the
   Stripe API also write a custom method as a sub-path, for example
   `POST /v1/invoices/{id}/pay`.
6. **Every mutating route takes an `Idempotency-Key` (owner decision
   2026-10-18).** Each route of the tool-neutral API that changes data
   (`POST`, `PUT`, `PATCH`, `DELETE`) accepts the header
   `Idempotency-Key`. The project owner wants easy integration with
   workflow engines such as Temporal, which retry a call that timed out.
   - **Scope.** The server keys a stored answer by the caller (the
     principal), the route (the method and the path) and the key. The same key from another caller or on another
     route is a new key.
   - **The key.** A string of 1 to 255 printable ASCII characters. A
     client uses a random value, for example a UUID. A key that is not
     valid answers 400 and changes nothing.
   - **Retention.** The server keeps the replay record (the key, a keyed
     hash of the request body and the first answer) for 24 hours from the
     first request.
   - **No secret in a replay record.** A replay record never holds a
     request body, a secret, a credential or a decrypted value. It holds
     the key, a keyed hash of the request body (HMAC-SHA-256 with a server
     key, never a plain hash, because a body can hold a low-entropy
     secret), the status and the answer. When the answer of a route holds
     a secret or a credential, the record holds the ID of the object that
     the first request made (for example a token ID or an open-session ID)
     in place of the answer. A replay then answers 409 with the error code
     `request.idempotency-key-replayed-secret` and that ID, and never
     answers the secret again. The declaration of the route says that its
     answer holds a secret, and a test checks each such route.
   - **A replay.** A request with a known key and the same body answers
     the stored first answer, with its status, and does the operation no
     second time. The answer has the header `Idempotent-Replayed: true`.
     Before it answers, a replay checks the authorization of the route
     again. If the caller does not have the action now, the replay
     answers 403 and does not answer the stored answer.
   - **A different body.** The same key with a different body answers 422
     with the error code `request.idempotency-key-reused` and changes
     nothing.
   - **A request in progress.** A request with a key whose first request
     has not ended answers 409 with the error code
     `request.idempotency-key-in-progress`. The client retries later.
   - **Errors.** The server stores an answer only when the operation ran:
     a 2xx answer, or a 4xx answer that the operation itself gave after it
     started. It never stores 400 (a key that is not valid), 401, 403,
     404, 409, 412, 422 (a reused key), 423, 429 or 5xx, so a retry with
     the same key after the client fixes the cause does the operation.
   - **Bulk calls.** A bulk call (ADR 213 decision 5) uses the key for a
     safe retry of the whole call. The server does not store an item that
     failed with 5xx or 429. A replay does such items again and answers
     the stored result of each other item.
   - **Which routes.** Decision 6 applies to the routes under
     `/api/dustpan/v1`. The key is optional. A route that changes data
     with no key behaves as today. A route can require the key. The route
     says so. A route that does not take the key declares it, and a
     request to it with the key answers 400. The server never ignores the
     key without an error.
   - **Transactions.** The server writes a pending record of the key in
     its own transaction, before the operation starts. It writes the final
     answer in the same transaction as the write of the operation, so a
     crash never stores an answer without the operation. A pending record
     that has no final answer after 5 minutes (for example after a crash)
     expires, and a retry with the same key then does the operation. The transaction of the final answer checks that the pending record of this request is still in place. If the record expired and a retry took the key, the transaction rolls back, so the operation never runs twice.
   - **Backup and restore.** The idempotency table is not in the backup
     table list. A restore empties it.

   Benchmark: Stripe idempotency keys (24 hours, a replay answers the
   first answer, a key with other parameters is an error), the AWS
   `ClientToken` of Amazon EC2, the Google `requestId` of AIP-155, and the
   IETF draft "The Idempotency-Key HTTP Header Field".
7. **Action names: `<area>:<verb>`.** An action has two parts and one colon.
   The area is the object kind of the action, one word in lowercase letters
   (`namespace`, `unit`, `job`, `changeorder`). The verb starts with a verb
   and is in kebab-case (`read`, `run-plan`, `manage-tokens`). The grammar
   is the regular expression `^[a-z]+:[a-z]+(-[a-z]+)*$`. A reference page in
   `docs/reference/` gives the grammar and lists each action with its object.

   The area `server:` becomes `installation:`, in the same change as the
   paths of decision 3. The verb does not change: `server:backup` becomes
   `installation:backup`, and `server:manage-settings` becomes
   `installation:manage-settings`. This ADR does not rename the
   authorization and audit object of the installation, `@server`.

   Benchmark: AWS IAM writes an action as `service:Action`, for example
   `s3:GetObject`. This ADR keeps the kebab-case verbs of the earlier ADRs.
8. **Versions after 1.0.** The path holds the major version (`v1`). Inside
   a major version, a change is additive only: a new route, a new optional
   request field, a new answer field, a new optional query parameter or a
   new error code (ADR 182). A change that can break a client written for
   an earlier release of the same major version needs a new major version
   (`v2`). The earlier major version stays served for a deprecation window
   of at least three minor releases at the 8-week cadence of ADR 75, so at
   least 24 weeks. Before 1.0, ADR 73 applies: `v1` can change and break,
   and the release notes list each break.

   Benchmark: Google AIP-180 (backwards compatibility) and AIP-185 (major
   versions in the path).
9. **Deprecation headers.** After 1.0, each answer of a deprecated route
   or of a route of a deprecated major version holds two headers:
   - `Deprecation` (RFC 9745), with the time at which the route became
     deprecated.
   - `Sunset` (RFC 8594), with the earliest time at which the route stops,
     at least the window of decision 8 after the deprecation.

   A `Link` header with the relation `deprecation` names the page of the
   release notes. The release notes of the release that deprecates the
   route have an entry that names the route, the replacement and the
   sunset date.

   Benchmark: RFC 9745 and RFC 8594. The GitHub REST API and the Stripe
   API mark a deprecated API version with headers and a changelog entry.
10. **One JSON style guide.** Each tool-neutral route follows these rules.
    A reference page in `docs/reference/` gives them with examples.
    - **Field names** are camelCase.
    - **Times** are RFC 3339 strings in UTC, for example
      `"2026-10-18T09:30:00Z"`. The store keeps Unix milliseconds. Only the
      wire changes.
    - **Lists** page with an opaque cursor. A request takes `pageSize` and
      `pageToken`. An answer holds the items and `nextPageToken`, which is
      empty on the last page. Each list route declares its default and its
      largest page size. A list parameter of an earlier ADR, such as
      `after`, follows this rule.
    - **Partial updates** use `PATCH` with JSON Merge Patch (RFC 7396) and
      the media type `application/merge-patch+json`, as ADR 180 does for
      preferences.
    - **Concurrency.** An answer that returns one resource holds its
      `ETag`. `PATCH`, `PUT` and `DELETE` take `If-Match`. A value that
      does not match answers 412 and changes nothing. A route can require
      `If-Match`. The route says so.
    - **Errors** keep the shape of ADR 182.

    Benchmark: Google AIP-140 (field names), AIP-158 (pagination), AIP-134
    (update) and AIP-154 (ETags).

## The move table

A route of an earlier ADR moves by decision 3. An area word that only
grouped routes, such as `server`, `inventory`, `context` or `unmanaged`,
leaves the path when the collection name is clear without it. A route that
put the namespace after the area word puts it under
`namespaces/{namespace}/`. A route that took the namespace as a query
parameter (`?namespace=` or `?organization=`) takes it in the path. A route
across namespaces takes `namespaces/-` (decision 4). The pull request that
moves the routes gives the full list of old and new paths. These rows are
the rule for each kind:

| Old path | New path |
| --- | --- |
| `/api/server/backups[/{id}[/check]]` | `/api/dustpan/v1/backups[/{id}[/check]]` |
| `/api/server/restores[/{id}]` | `/api/dustpan/v1/restores[/{id}]` |
| `/api/server/caller/...` | `/api/dustpan/v1/me/...` |
| `/api/server/namespaces/{namespace}/...` | `/api/dustpan/v1/namespaces/{namespace}/...` |
| `/api/runners/{namespace}/pools[/{id}]` | `/api/dustpan/v1/namespaces/{namespace}/runner-pools[/{id}]` |
| `/api/server/session-policy`, `/api/server/token-policy`, `/api/server/namespaces/{namespace}/ip-allowlist` | views of the settings resource (ADR 212 decision 7) |
| `/api/server/sign-in/{provider}` | `/api/dustpan/v1/sign-in/{id}` |
| `/api/server/sign-in/{provider}/callback` | `/sign-in/{id}/callback`, with no alias (decision 2) |
| `/api/inventory/units?namespace=...` | `/api/dustpan/v1/namespaces/{namespace}/units`, or `/api/dustpan/v1/namespaces/-/units` across namespaces |
| `/api/inventory/units/{tool}/{unit_id}/dependents` | `/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/dependents`, also through `namespaces/-` |
| `/api/inventory/units/{tool}/{unit_id}/graph/{upstream,downstream,focus}` | `/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/graph/{upstream,downstream,focus}`, also through `namespaces/-` |
| `/api/inventory/deleted-units?organization=...` | `/api/dustpan/v1/namespaces/{namespace}/deleted-units`, or `/api/dustpan/v1/namespaces/-/deleted-units` across namespaces |
| `/api/inventory/deleted-units/{tool}/{unit_id}[/restore]` | `/api/dustpan/v1/namespaces/{namespace}/deleted-units/{unit_id}[/restore]` |
| `/api/server/deleted-units/sweep` | `/api/dustpan/v1/deleted-units/sweep` (an operation of the installation, because a write never takes `-`) |
| `/api/inventory/namespaces/{namespace}/collections[/{collection}]` | `/api/dustpan/v1/namespaces/{namespace}/collections[/{collection}]` |
| `/api/context/units/{tool}/{unit_id}[/resource?key=...]` | `/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/context[/resource?key=...]`, also through `namespaces/-` |
| `/api/unmanaged/{namespace}/...` | `/api/dustpan/v1/namespaces/{namespace}/...` |
| `POST /api/v2/state-versions/{state_version_id}/actions/restore` (ADR 28 decision 3) | `POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/state-versions/{state_version_id}/restore` |
| `POST /api/stacks/{namespace}/{project}/{stack}/restore/{version}` (ADR 28 decision 5) | `POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/state-versions/{version}/restore` |

**Extension routes inside a tool prefix.** An OpenDustpan route that sits
inside the prefix of a tool-compatible API, and that no supported CLI
sends, moves under `.../namespaces/{namespace}/units/{unit_id}/...`. The
restore routes of ADR 28 are the first of them. A field that OpenDustpan
adds to an answer of a route that the CLI sends, such as the attributes
`holder` and `restored-from` of ADR 28 decision 2, stays on that route.
`make e2e` checks that each supported CLI accepts it.

## What this ADR replaces

The route paths and the `server:` action names in the ADRs below read as
decisions 1 to 7 give them. The status line of each of these ADRs says so.
Each other part of these ADRs stays as it is.

ADRs 28, 30, 32, 38, 60, 64, 65, 67, 70, 102, 110, 113, 117, 120, 122, 123,
124, 125, 129, 138, 140, 141, 142, 143, 144, 145, 146, 147, 148, 149, 150,
151, 152, 153, 154, 160, 161, 162, 163, 164, 165, 166, 180, 181, 182 and
197.

Decision 6 also amends ADR 200 decision 9 and ADR 203 decision 12. A replay record holds a keyed hash (HMAC-SHA-256) of the body, and a route that does not take the key answers 400.

## Not decided here

- The usage ledger of deprecated features of RFC 38. The project decides it
  at the first deprecation of a feature.

## Consequences

- One pull request moves every tool-neutral route and renames the
  `server:` actions. The route test of ADR 17 and the action constants of
  the `identity` package change with it. The console change merges before
  the next server tag (ADR 73).
- The route test puts the paths of decision 2 in a separate list, so a
  route outside `/api/dustpan/v1` cannot be added by mistake. A test
  checks that the old callback path answers 404.
- The Pulumi CLI and OpenTofu see no change. `make e2e` shows it.
- The idempotency store of decision 6 gets a port in the consuming package
  and a conformance suite, and each store adapter runs it. A sweep deletes
  keys older than 24 hours and pending records older than 5 minutes. A
  test checks each route whose answer holds a secret.
- The `GLOSSARY.md` entries that name a path, such as **Installation** and
  **Preferences**, name the new path.
- Three reference pages come with the move: the action grammar of decision
  7, the idempotency rules of decision 6 and the style guide of decision 10.
