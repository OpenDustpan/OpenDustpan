# 210. The tool-neutral API: one prefix, resource paths, action names, versions and a style guide

Status: accepted · 2026-10-18 · accepts RFC 59. Supersedes in part the route paths and the `server:` action names of earlier ADRs (see "What this ADR replaces")

## Context

Decided by the project manager under the owner's decision rule, 2026-10-18.
The rule: the project manager decides a question when Google, AWS, PCI DSS,
SOC 2 or ISO 27001 would answer it the same way, when no persona is upset or
an escape hatch exists, and when the answer is not stupid on a step back.

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
- The tool-compatible APIs own other paths under `/api/`: the Pulumi
  Cloud-compatible API (`/api/stacks/...`, `/api/user`, `/api/capabilities`
  and others), the TFE-compatible API (`/api/v2/...`) and the OpenTofu HTTP
  backend. The real CLIs send these paths, and we cannot change them.
- 23 actions have the area `server:`, for example `server:backup` and
  `server:observe`. The other areas name an object kind: `namespace:`,
  `unit:`, `job:`, `changeorder:`, `caller:` and others.
- ADR 182 gives the error shape of the tool-neutral APIs: `{"code",
  "message", "errorCode", "errorArgs"}`.
- No tag exists. ADR 73 lets every API except the tool-compatible APIs
  change and break before 1.0.

## Decision

1. **One prefix for every tool-neutral route.** Each tool-neutral route
   moves under `/api/dustpan/v1/`. This includes the console routes, the
   runner protocol, the MCP endpoint of the agent and the metrics route. The
   move lands before the first tag. The server keeps no alias and no
   redirect for an old path, because no tag shipped one (ADR 73).

   These paths do not change: the routes of the Pulumi Cloud-compatible API,
   of the TFE-compatible API and of the OpenTofu HTTP backend, the discovery
   document `/.well-known/terraform.json`, and the probe paths (ADR 34).

   Benchmark: Google AIP-185 puts the major version of an API in its path.
2. **Resource-oriented paths.** A path names a collection or a resource in
   it. Collection names are plural nouns, in the kebab-case that the earlier
   ADRs use (`change-orders`, `freeze-schedules`). No path holds the word
   "server".

   | Object | Path |
   | --- | --- |
   | An object of the installation | `/api/dustpan/v1/<collection>[/{id}]`, for example `/api/dustpan/v1/backups/{id}`, `/api/dustpan/v1/metrics`, `/api/dustpan/v1/groups/{id}`, `/api/dustpan/v1/calendars/{id}` |
   | An object of a namespace | `/api/dustpan/v1/namespaces/{namespace}/<collection>[/{id}]`, for example `.../namespaces/{namespace}/change-orders/{id}`, `.../namespaces/{namespace}/runner-pools/{id}` |
   | A unit | `/api/dustpan/v1/namespaces/{namespace}/units/{tool}/{name}` |
   | An object of a unit | `/api/dustpan/v1/namespaces/{namespace}/units/{tool}/{name}/<collection>[/{id}]` |
   | An object of the caller | `/api/dustpan/v1/me/<collection>`, for example `/api/dustpan/v1/me/preferences` |

   A route of an earlier ADR moves by this table. An area word that only
   grouped routes, such as `server`, `inventory` or `unmanaged`, leaves the
   path when the collection name is clear without it. A route that put the
   namespace after the area word puts it under `namespaces/{namespace}/`.
   For example, `/api/server/backups` becomes `/api/dustpan/v1/backups`,
   `/api/server/caller/preferences` becomes `/api/dustpan/v1/me/preferences`,
   and `/api/runners/{namespace}/pools` becomes
   `/api/dustpan/v1/namespaces/{namespace}/runner-pools`. The pull request
   that moves the routes gives the full list of old and new paths.

   Benchmark: Google AIP-121 (resource-oriented design) and AIP-122
   (resource names with the parent first). AIP-122 writes collection names
   in camelCase. This ADR keeps kebab-case, because each earlier ADR and the
   console use it, and a path segment is not a JSON field.
3. **Action names: `<area>:<verb>`.** An action has two parts and one colon.
   The area is the object kind of the action, one word in lowercase letters
   (`namespace`, `unit`, `job`, `changeorder`). The verb starts with a verb
   and is in kebab-case (`read`, `run-plan`, `manage-tokens`). The grammar
   is the regular expression `^[a-z]+:[a-z]+(-[a-z]+)*$`. A reference page in
   `docs/reference/` gives the grammar and lists each action with its object.

   The area `server:` becomes `installation:`, in the same change as the
   paths of decision 2. The verb does not change: `server:backup` becomes
   `installation:backup`, and `server:manage-settings` becomes
   `installation:manage-settings`. This ADR does not rename the
   authorization and audit object of the installation, `@server`.

   Benchmark: AWS IAM writes an action as `service:Action`, for example
   `s3:GetObject`. This ADR keeps the kebab-case verbs of the earlier ADRs.
4. **Versions after 1.0.** The path holds the major version (`v1`). Inside
   a major version, a change is additive only: a new route, a new optional
   request field, a new answer field, a new optional query parameter or a
   new error code (ADR 182). A change that can break a client written for
   an earlier release of the same major version needs a new major version
   (`v2`). The earlier major version stays served for a deprecation window
   of at least three minor releases (ADR 75), so at least 24 weeks. Before
   1.0, ADR 73 applies: `v1` can change and break, and the release notes
   list each break.

   Benchmark: Google AIP-180 (backwards compatibility) and AIP-185 (major
   versions in the path).
5. **One JSON style guide.** Each tool-neutral route follows these rules. A
   reference page in `docs/reference/` gives them with examples.
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
   - **Concurrency.** An answer that returns one resource holds its `ETag`.
     `PATCH`, `PUT` and `DELETE` take `If-Match`. A value that does not
     match answers 412 and changes nothing. A route can require
     `If-Match`. The route says so.
   - **Errors** keep the shape of ADR 182.

   Benchmark: Google AIP-140 (field names), AIP-158 (pagination), AIP-134
   (update) and AIP-154 (ETags).

## What this ADR replaces

The route paths and the `server:` action names in the ADRs below read as
decisions 1 to 3 give them. The status line of each of these ADRs says so.
Each other part of these ADRs stays as it is.

ADRs 30, 32, 38, 60, 64, 65, 67, 70, 102, 110, 113, 117, 120, 122, 123,
124, 125, 129, 138, 140, 141, 142, 143, 144, 145, 146, 147, 148, 149, 150,
151, 152, 153, 154, 160, 161, 162, 163, 164, 165, 166, 180, 181, 182 and
197.

## Not decided here

- The shape of a list across namespaces, for example all units that the
  caller can read. Google AIP-159 reads across parents with the wildcard
  `-` (`/namespaces/-/units`). That is the lead proposal.
- The shape of a custom method, an operation that is not a create, a read,
  an update or a delete. Google AIP-136 writes it as `{id}:verb`. The Go
  `ServeMux` cannot match a part of a path segment, so the earlier ADRs use
  a sub-path, such as `.../verdict`. The routes keep the sub-path until a
  decision.
- The headers that mark a deprecated route after 1.0, and the usage ledger
  of RFC 38. The project decides both at the first deprecation.

## Consequences

- One pull request moves every tool-neutral route and renames the
  `server:` actions. The route test of ADR 17 and the action constants of
  the `identity` package change with it. The console change merges before
  the next server tag (ADR 73).
- The Pulumi CLI and OpenTofu see no change. `make e2e` shows it.
- The `GLOSSARY.md` entries that name a path, such as **Installation** and
  **Preferences**, name the new path.
- Two reference pages come with the move: the action grammar of decision 3
  and the style guide of decision 5.
