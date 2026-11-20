---
version: 9
---

Issue: #1299, #1300, #1301.

## Why

**Persona: operator.** The operator installs the installation-level GitHub App and points its one webhook at the server. Today the server has the registration, the hook ID and the check, but no route takes a delivery, and the GitHub adapter answers ErrUnsupported for ParseEvent and RepoToken. The operator needs a stable hook URL, an answer that never tells an attacker why a delivery was dropped, a rotation of the event secret that does not lose deliveries, and a record of every refusal.

The user path this change must make work:

1. **Server start.** The operator registers the GitHub App with PUT /api/dustpan/v1/git-host-apps/github (exists on main) and reads the `hookID` in the answer.
2. **API.** The operator rotates the event secret with the same PUT. The answer of GET /api/dustpan/v1/git-host-apps/github shows `oldSecretEndsAt`. The operator ends the rotation with the NEW route POST /api/dustpan/v1/git-host-apps/github/confirm-rotation (the change adds it).
3. **Docs page.** The operator sets the webhook address of the GitHub App to https://<stable-host>/webhooks/<hook ID> and sends the test delivery (a ping) from GitHub.
4. **E2e.** A signed ping and a signed pull request delivery reach the in-process server of the e2e suite through the fake GitHub host. The step checks the 204 answer, the hint record and the audit entries.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Bad signature, unknown hook ID, body above 2 MiB, or event kind that the port does not name | 204 with an empty body, the same bytes as for an accepted delivery. The server counts the reject by reason class and writes one limited audit entry. | `GET /api/dustpan/v1/git-host-apps/github` (compare `hookID`, `eventSecret.setID` and `oldSecretEndsAt` with the GitHub App settings) |
| Event of an installation that the map does not assign | 204. The reject reason is `not-assigned`. | `PUT /api/dustpan/v1/git-host-installations/{installation_id}` |
| Event for a namespace whose primary installation is another member | 204. The member drops the event and the federation poll catches the change. | `GET /api/dustpan/v1/components` |
| Operator confirms a rotation when no window is open | 409 that names the missing window. | `GET /api/dustpan/v1/git-host-apps/github` |

Evidence for the claims above:

- The registration, the installation map and the connections exist on main. Proof: `pkg/forge/connection.go:204-218` (type Registration), `pkg/forge/registration.go:93` (func Service.PutRegistration), `pkg/store/sqlite/migrations/0168_baseline.sql:1019` (table git_host_app_registrations).
- The registration route set has no confirm route. Proof: `internal/api/server/githost.go:106-108` lists GET, PUT and DELETE on `gitHostAppPath` only.
- The adapter does not parse events and does not mint a repo token. Proof: `pkg/forge/github/github.go:309-316` (RepoToken and ParseEvent return ErrUnsupported), `pkg/forge/github/github.go:289-301` (Capabilities lists no CapEvents and no CapRepoToken).
- The scoped installation token that RepoToken needs exists as a private helper. Proof: `pkg/forge/github/files.go:527` (func gitHub.mintToken) asks for one repository and one permission, and refuses a wider grant.
- No route takes a webhook. Proof: `pkg/dustpan/dustpan.go:3892-3904` mounts only the discovery document, the issuer routes and the registry on the root mux, and `pkg/dustpan/route_actions_test.go:43-65` (publicRoutes) lists no webhook route.
- Files, Commits and Reviews are built on main. Proof: `pkg/forge/github/files.go:61` (Head), `pkg/forge/github/files.go:127` (ReadFile), `pkg/forge/github/compare.go:64` (Compare), `pkg/forge/github/commit.go:65` (Commit), `pkg/forge/github/commit.go:849` (FindCommit), `pkg/forge/github/review.go:62` (OpenReview), `pkg/forge/github/review.go:260` (ReadReview), `pkg/forge/github/review.go:308` (CloseReview). This change does not touch them. Issue #1300 is done on main.

## What Changes

- Parse GitHub events in the adapter: `ParseEvent` for ping and pull request events, with the X-Hub-Signature-256 check against up to two event secrets. The adapter lists CapEvents.
- `RepoToken` in the adapter: one repository, contents read, an expiry of at most 1 hour, Scoped set. The adapter lists CapRepoToken and CapGitAccess.
- A NEW store part for event receipts (the hint record and the 24-hour duplicate rule) with its table in both baselines, its conformance cases and its backup coverage.
- The rotation of the event secret in the registration: three NEW nullable columns for the old secret, the rotation inside PutRegistration, the NEW confirm route and the NEW field `oldSecretEndsAt`.
- The NEW route POST /webhooks/{hook ID} in the NEW package internal/api/githubwebhook, the NEW metric `dustpan_git_host_event_rejected_total`, limited audit entries of rejects, and the audit entry of each event that the old secret signed.
- The docs: the section "Webhooks and a federation" of the connect guide, and the reference of the registration routes.

Out of scope:

- Files, Commits and Reviews methods (built on main, see above) and the runner side of the repo token. No caller of RepoToken exists on main (`rg RepoToken pkg internal` finds the port, the adapter stub and the conformance suite only). The refusal of a token with Scoped not set belongs to the change that adds the first caller. This change gives the adapter side: an app connection never answers an unscoped token.
- Hint consumers: the review watch, the manual redelivery decision from the state of the effect, and the comment commands. The receipt is the hint record. A consumer reads it in its own change.
- The `push` event. The port names EventPing and EventReview only (`pkg/forge/forge.go:1321-1326`). A push needs a new EventKind and a decision on who consumes it.
- Forwarding of events between federation members, and the provisioning of the stable hostname.
- A real-binary e2e harness for GitHub deliveries (none exists, see design decision 9).

Parked: owner question (outside the build tasks, the change starts without it):

- The address-digest change during a rotation. See design "Parked: owner question".

## Usability

- The 204 for every request hides the reason from a caller. The operator reads the reason class in the metric and in the audit entry, and the API shows the effective end of the rotation window, so the person never has to guess whether the old secret still works.
- A rotation needs two steps that the operator can see: PUT a new event secret (the old one keeps working for at most 7 days), then confirm after GitHub holds the new one. The docs page shows both commands in order.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The port declares ParseEvent and its sentinels: ErrSignature for no match or no secret, ErrInvalid for a body that does not parse, ErrUnsupported for a kind outside the port. (`pkg/forge/forge.go:427-439 (type Events)`)
- The port declares RepoToken with Scoped set for an app connection and the rule that a caller refuses a token with Scoped not set. (`pkg/forge/forge.go:414-424 (type Tokens)`)
- The adapter returns ErrUnsupported from both methods and lists neither capability. (`pkg/forge/github/github.go:309-316`, `pkg/forge/github/github.go:289-301 (func gitHub.Capabilities)`)
- The conformance suite already names the token and event cases with no body (nil), and its method table calls ParseEvent and RepoToken through the capability gate. (`pkg/forge/forgetest/forgetest.go:370-395`, `pkg/forge/forgetest/connection_cases.go:109-117`)
- The registration holds one sealed event secret, bound to the address digest, and PutRegistration replaces it in one call. The store keeps the hook ID and gives the next version. (`pkg/forge/connection.go:204-218 (type Registration)`, `pkg/forge/registration.go:93-139 (func Service.PutRegistration)`, `pkg/forge/connection.go:350-354`)
- The hook ID is 128 random bits, unique, and not changed by a replacement. (`pkg/forge/connection.go:110-113 (func NewHookID)`, `pkg/store/sqlite/migrations/0168_baseline.sql:1022 (hook_id ... UNIQUE)`, `pkg/store/postgres/migrations/0168_baseline.sql:1758-1759`)
- The registration answer carries `hookID` and the set ID of the event secret, and no secret. (`internal/api/server/githost_app.go:32-41 (type gitHostAppDocument)`)
- The registration routes are GET, PUT and DELETE, with the actions server:read-git-host and server:manage-git-host. (`internal/api/server/githost.go:36`, `internal/api/server/githost.go:106-108`)
- The installation map and its lookup exist, and Open refuses an unassigned installation. (`pkg/forge/registration.go:194-205 (func Service.GetAssignment)`, `pkg/forge/service.go:459-480 (func Service.requireAssigned)`)
- A refused Open counts a metric with a fixed label set and writes a denied audit entry in the namespace. (`internal/metrics/server.go:687-740 (func ServerMetrics.ObserveGitHostRefused)`, `pkg/forge/service.go:556-576 (func Service.refuse)`)
- The shared denial limiter and the recorder exist. (`internal/denylimit/denylimit.go:159 (func Limiter.Allow)`, `internal/api/refusal/refusal.go:403-406` (AppendServer on the installation scope))
- The one public-route list and the root prefix table are in the route test of pkg/dustpan. (`pkg/dustpan/route_actions_test.go:43 (publicRoutes)`, `pkg/dustpan/route_actions_test.go:73 (rootPrefixes)`)
- The root mux mounts routes outside the versioned API, for example the issuer routes. (`pkg/dustpan/dustpan.go:3900-3904`, `internal/api/issuer/routes.go:42-60`)
- The baseline has one migration file per dialect, and the git host tables are in it. (`pkg/store/sqlite/migrations/0168_baseline.sql:1019-1051`, `pkg/store/postgres/migrations/0168_baseline.sql:328-360`)
- The registration queries and the backup column lists name the event secret columns. (`pkg/store/sqlite/queries.sql:4334-4361`, `pkg/store/postgres/queries.sql:4826-4853`, `pkg/store/sqlite/backup_githost.go:89-92`, `pkg/store/postgres/backup_githost.go:21-30`, `pkg/backup/portable_githost.go:12-31`)
- The backup coverage lists the tables that a restore leaves empty. (`pkg/backup/coverage.go:61-89 (EmptiedTables)`)
- The e2e harness drives an in-process server with the fake GitHub host, and the fake signs and sends deliveries. (`e2e/githost.go:88 (func NewEnvWithGitHost)`, `e2e/githubfake/webhook.go:96-135 (NewDelivery, Redeliver, Send)`)
- The connect guide says that the release takes no webhook. (`docs/guides/connect-github.md:257-266`)

Differences from the earlier audit of this change, found by reading main:

- The old task for the Files, Commits and Reviews methods is dropped: the methods exist.
- The old cites of one query line and one baseline line (the audit gave the lines 4163 and 969) were stale. The right lines are above.
- The store name MarkDelivery collides with the type Delivery and the function DeliveryOf of the commit write-back in `pkg/forge/forge.go:547-557`. The NEW names use "event receipt".
- The public list is in `pkg/dustpan/route_actions_test.go`, not in `internal/api/route`.
- The body limit of the intake is 2 MiB, as the decision says.
- The conformance suite of the registration store is `pkg/forge/connectiontest`, not a file in storetest.
- The e2e suite has no real-binary variant for the Git host. The step runs in process.

## ADR check

Each quote is copied from the file in `docs/adr/`. The "Changed by" chain and the "Current rule" come from `docs/adr/INDEX.md`. A decision is owner-tagged when its text says "(owner" or when the ADR status says that the project owner decided it.

**ADR 374: Namespace discovery from inside, and the window of the old event secret** (owner-tagged: "Decided by the project owner", decision 2 is "(owner, 2026-11-14)").

- Decisions that apply: 2. Extends ADR 271 decision 8 and ADR 52 decision 5.
- Changed by: none. Current rule: this ADR.
- Quote: "During a rotation, the old event secret is valid for the check of an event only. It ends after 7 days, or earlier when the operator ends the rotation (the confirmation of the rotation). Each event that the server accepts with the old secret writes an audit entry. The API shows the effective end of the window. The server refuses an event signed with the old secret after the end, or after the confirmation. A secret older than the old secret is never valid: a registration keeps at most two event secrets."
- Effect: design decisions 4 and 5 and the spec requirement "Old event secret window".
- Conflict: none. The decision speaks of a rotation. It does not say what a change of the API address does to an open window. That is the parked question.

**ADR 271: The Forge port, Git host connections and the installation-level GitHub App** (owner-tagged: "Decisions 1 to 11 and 13 are owner decisions").

- Decisions that apply: 3, 4, 8, 9.
- Changed by: ADR 286 changes; ADR 299 changes decision 4; ADR 299 extends decision 2; ADR 299 extends decision 7; ADR 300 changes decision 7; ADR 303 extends decision 11; ADR 336 extends; ADR 374 extends decision 8. Current rule: this ADR, with ADR 286, 299, 300, 303, 336, 374.
- Quote (decision 3): "The runner path refuses a token with `Scoped` not set, so a long-lived token never leaves the server."
- Quote (decision 4): "The route is `POST /webhooks/{hook ID}`. It is outside `/api/dustpan/v1` (ADR 210 decision 2), and its path has no word "forge"."
- Quote (decision 4): "The route reads at most 2 MiB, finds the registration by the hook ID and checks the event against each active event secret of that registration. It answers 204 to each request, also for an unknown hook ID, a bad signature and a body that is too large. The metric `dustpan_git_host_event_rejected_total` counts the rejected events by reason."
- Quote (decision 4): "The server drops a delivery ID that it saw for the same connection in the last 24 hours."
- Quote (decision 8): "A registration keeps two active event secrets during a rotation. An event is valid when it matches either one."
- Quote (decision 9): "A member that receives an event for a namespace whose primary installation is another member answers 204 and drops the event."
- Effect: the route, the 2 MiB bound, the 204 answer and the metric follow decision 4 word for word. The duplicate key reads "same connection" as the registration kind and the installation that the map assigns to one namespace (design decision 6). Decision 8 is changed by ADR 374 for the window.
- Conflict: none. ADR 299 changes decision 4 to "webhooks with three recovery paths and no polling". The three paths are the webhook, the catch-up read at start and manual redelivery (ADR 299 decision 29). This change takes only the first.

**ADR 299: Review requests and change orders: one integration with the Git host** (owner-tagged: decisions 29 and 31 carry "(owner)").

- Decisions that apply: 29, 31.
- Changed by: ADR 328 extends decision 1; ADR 359 extends decisions 1, 15 and 25; ADR 368 changes decision 19. Current rule: this ADR, with ADR 328, 359, 368.
- Quote (decision 31): "Webhooks and identity (owner). The server verifies each signature, refuses a replay, and never decides on the payload alone."
- Quote (decision 29): "No polling (owner). Webhooks, a "Plan again" button and the command `dustpan plan`, one catch-up read at start, and manual redeliveries that the server decides from the state of the effect."
- Effect: the receipt rule refuses a replay, including a replay with a new delivery ID (design decision 6). The decision of a manual redelivery from the effect state belongs to the consumers and is out of scope.
- Conflict: none.

**ADR 250: Defense in depth for security code** (owner-tagged: "The project owner decided it").

- Decisions that apply: 1, 2, 3, 4, 6.
- Changed by: ADR 382 extends decision 3. Current rule: this ADR, with ADR 382.
- Quote (decision 3): "Fail closed and record: an unknown value, a parse error, missing configuration or a dependency error means deny. Every denial and every use of a privilege goes to the audit log."
- Quote (decision 4): "Every layer has its own test. The test sends hostile input directly to that layer, as if every other layer had failed."
- Quote (decision 6): "Use the standard library or well-known primitives (the constant-time compare of the standard library package for subtle crypto, `crypto/rand`)."
- Effect: one hostile table for each layer (design "Layers"), HMAC compare through the standard library, and an audit entry for each reject through the limiter.
- Conflict: none.

**ADR 382: A repeatable domain refusal goes through the shared denial limiter** (owner-tagged: "Records a decision of the project owner of 15 November 2026").

- Decisions that apply: the ADR as a whole. Extends ADR 250 decision 3.
- Changed by: none. Current rule: this ADR.
- Quote: "For each key, the first denial of a 1-minute window writes an entry. The limiter counts each later denial in the window and writes no entry for it. The count goes out with the next entry of the key."
- Effect: rejects of an unauthenticated route can be sent in any number by a stranger. The intake writes the entry of a reject through the limiter, with a fixed key set (design decision 7).
- Conflict: none.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide** (owner-tagged: decision 2 is named by ADR 271).

- Decisions that apply: 2 (the prefix). The change adds one NEW route under the registration path (confirm-rotation, with the existing action) and one route outside the prefix as ADR 271 decision 4 says.
- Changed by: see INDEX.md. Current rule: this ADR, with its later changes.
- Conflict: none.

**ADR 336 and ADR 303** (owner-tagged). Their subjects are the author of a write-back commit and the scope limit "OpenDustpan is not a Git host". This change touches neither: the Commit method is not changed, and the change only reads events from the Git host.

## Open questions

none

One owner question is parked outside the build tasks (the address digest during a rotation). The change specifies and builds without it: design "Parked: owner question" gives the interim behavior that the structure forces.

Questions considered and answered:

- How long does the old event secret stay valid? ADR 374 decision 2: at most 7 days, or until the confirmation.
- Where does the parsing live? ADR 271 decision 4: ParseEvent in the port, the route in the internal adapter.
- Which action guards the confirm route? The same action as the PUT that starts a rotation, server:manage-git-host. The confirm only ends trust in a secret, so it needs no wider right than the PUT.
- What does the intake record for a reject? One limited audit entry for each reason class in each minute, and a counter (ADR 250 decision 3, ADR 382).
