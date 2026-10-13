# Roadmap

The roadmap sorts every RFC into ordered milestones. The map ticket
[Order the milestones and place every RFC](https://github.com/OpenDustpan/OpenDustpan/issues/28)
set this order on 2026-10-10. A milestone is done when
its exit test passes. An RFC in a milestone must be accepted (as an ADR) before
the milestone's code starts. Console work sits in the same milestones as rows
marked `console`, because the Console ships from its own repository (ADR 8).

Each RFC appears once. When one RFC is split across milestones, each row names
its part.

How to read a row:

- **Where**: `server` (this repository) or `console` (`OpenDustpan/console`).
- **Item**: an RFC with its number, or a short name for work with no RFC.
- **State**: the state of the RFC or decision. `accepted (ADR n)` means it
  is decided. `discussing` and `draft` are RFC states (`docs/rfc/README.md`).

## Milestone 1. The Pulumi CLI runs a full deployment on one node

**Goal.** A team can use OpenDustpan as the backend of the Pulumi CLI for
everyday work, on one node, with SQLite or PostgreSQL.

**Exit test.** The e2e suite passes `up`, `preview`, `refresh`, `destroy`,
`cancel`, `stack export` and `stack import`, and config secrets, on the three
Pulumi CLI versions in the suite, on SQLite and on PostgreSQL. The stack
steps that the scenario already runs (`stack rename`, `stack rm`, `stack tag
set`) pass too. Delta checkpoints and journaling are not part of it.

**Status.** Met on 2026-10-11. The e2e suite passes all 59 Pulumi steps on
the three versions and both stores (issue #58).

| Where   | Item                                                                             | Part                                                                                   | State             |
|---------|----------------------------------------------------------------------------------|----------------------------------------------------------------------------------------|-------------------|
| server  | [RFC 2](rfc/0002-organizations-are-namespaces.md) Organizations are namespaces   | all                                                                                    | accepted (ADR 18) |
| server  | [RFC 3](rfc/0003-domain-modules-for-updates-secrets-and-state.md) Domain modules | 1: the Pulumi update cycle                                                             | accepted (ADR 20) |
| server  | [RFC 3](rfc/0003-domain-modules-for-updates-secrets-and-state.md) Domain modules | 2: secrets                                                                             | accepted (ADR 23) |
| server  | Stable identity of a stack (no RFC)                                              | all                                                                                    | accepted (ADR 19) |
| server  | Where checkpoints live (no RFC)                                                  | all                                                                                    | accepted (ADR 24) |
| server  | [RFC 17](rfc/0017-unit-lifecycle.md) Unit lifecycle                              | Pulumi stack rename, hard delete, tags as the CLI sends them (rules in ADR 19, ADR 23) | draft             |
| server  | [RFC 62](rfc/0062-pulumi-journaling.md) Pulumi journaling                        | follow-up after the exit test: journal version 1                                       | accepted (ADR 43) |
| server  | [RFC 64](rfc/0064-pulumi-journaling-architecture.md) Journaling architecture     | follow-up after the exit test: all                                                     | accepted (ADR 43) |
| console | Keep the Console working through the API changes above                           | n/a                                                                                    | n/a               |

## Milestone 2. OpenTofu runs on one node

**Goal.** A team can use OpenDustpan as the remote backend of OpenTofu, next to
the Pulumi CLI, on one node.

**Exit test.** On Linux, for each OpenTofu version in the e2e matrix, on
SQLite and on PostgreSQL, over TLS, the e2e suite drives `init`, `plan`,
`apply`, `output` and `state pull`. It drives `state push` (the server rejects
a stale serial and accepts `-force`). It drives a lock held by another
principal and `force-unlock`, and recovery after a crashed `apply`. It drives
`workspace new`, `workspace list` and `workspace select`, and `destroy`. The
full Pulumi scenario still passes on the same server.

**Status.** Met on 2026-10-11. The e2e suite passes every exit-test step on
each OpenTofu version in the matrix. It runs on SQLite and on PostgreSQL,
over TLS through the real binary. The full Pulumi scenario passes on the same
server (issue #99).

| Where   | Item                                                                                         | Part                                                                     | State             |
|---------|----------------------------------------------------------------------------------------------|--------------------------------------------------------------------------|-------------------|
| server  | [RFC 1](rfc/0001-opentofu-via-the-tfe-compatible-api.md) OpenTofu via the TFE-compatible API | all                                                                      | accepted (ADR 25) |
| server  | [RFC 3](rfc/0003-domain-modules-for-updates-secrets-and-state.md) Domain modules             | 3: OpenTofu state                                                        | accepted (ADR 26) |
| server  | [RFC 35](rfc/0035-packaging.md) Packaging and installation                                   | TLS, binary, container image                                             | accepted (ADR 27) |
| server  | [RFC 17](rfc/0017-unit-lifecycle.md) Unit lifecycle                                          | the rest: soft delete, move, workspace lifecycle, tags as labels (RFC 4) | draft; soft delete accepted (ADR 47) |
| server  | [RFC 63](rfc/0063-workspace-lock-holder.md) A workspace lock belongs to a principal          | follow-up after the exit test: all                                       | accepted (ADR 44) |
| server  | [RFC 65](rfc/0065-opentofu-http-backend.md) OpenTofu through the http backend too            | follow-up after the exit test: all                                       | accepted (ADR 49) |
| console | [RFC 52](rfc/0052-console-core.md) Console core views                                        | unit list across both tools, "connect your CLI" snippet                  | draft             |

**First tagged release: `v0.1.0`** ([ADR 21](adr/0021-the-first-tagged-release.md)).
Its gate:

- [x] A `LICENSE` in this repository ([ADR 22](adr/0022-apache-2-license.md)).
- [ ] A `LICENSE` in the Console repository.
- [x] A `--version` flag that the build stamps.
- [x] An upgrade test from the previous tag (for `v0.1.0`, an empty database).
- [x] The binary refuses to start on a database schema that is newer than it
  knows ([ADR 33](adr/0033-upgrade-checks.md), amendment to ADR 21).
- [ ] Install docs, with TLS ([RFC 35](rfc/0035-packaging.md) accepted first).
- [x] The e2e suite passes for both tools on SQLite and on PostgreSQL.

## Milestone 3. Move in and run it for real

**Goal.** A team can move its existing state into OpenDustpan without losing
history or secrets, and can operate the server: back it up, restore it,
upgrade it and watch it.

**Exit test.** On Linux, on SQLite and on PostgreSQL, the e2e suite drives
these steps for both tools, on each version in its matrix. It imports state
from a Pulumi service backend, a Pulumi file backend, an OpenTofu backend and
a TFE-compatible API (ADR 29). The imported secrets decrypt, `up` and `plan`
show no changes, and the source stays unchanged. It lists OpenTofu state
versions, restores an old one, and restores an old Pulumi checkpoint. A restore
while another principal holds the lock gives 409. `force-unlock` and
`pulumi cancel` free a stuck lock or update (ADR 28). It takes a backup while
the scenarios write. It restores the backup into an empty installation with the
same key, and both CLIs read the same state. A wrong key stops the restore
before it writes (ADR 30). The OpenTofu scenario and the backup scenario pass
with the blobs on an S3-compatible server (ADR 31). The metrics route counts
the requests of both tools. `/health/ready` turns to 503 when the database
stops (ADR 32, ADR 34). The upgrade test starts the current binary on a database of the
previous tag. Data, secrets and the held lock survive. The previous binary
refuses to start on the upgraded database (ADR 33). The full Pulumi and
OpenTofu scenarios still pass on the same server.

**Status.** Met on 2026-10-12 on main 60f8163, with the upgrade exception
below. The e2e suite passes the exit-test steps for both tools, on each
version in the matrix, on SQLite and on PostgreSQL, through the real binary over TLS. The backup and
restore steps also pass with the blobs on an S3-compatible server, and
the upgrade test passes on both stores for the empty-database case: data,
secrets and the held lock survive a restart of the current binary (issue
#184). No `v*` tag exists yet, so the two steps that need a previous tag
did not run: the start of the current binary on a database of the previous
tag, and the refusal of the previous binary on the upgraded database. They
run from the first tag on.

| Where   | Item                                                                 | Part                               | State                                             |
|---------|----------------------------------------------------------------------|------------------------------------|---------------------------------------------------|
| server  | [RFC 42](rfc/0042-migration-import.md) Migration from other backends | first sources                      | accepted (ADR 29)                                 |
| server  | [RFC 18](rfc/0018-state-operations.md) State operations              | history, restore, force-unlock     | accepted (ADR 28)                                 |
| server  | [RFC 8](rfc/0008-backups-and-restore.md) Backups and restore         | all                                | accepted (ADR 30, portable format ADR 42)         |
| server  | [RFC 32](rfc/0032-object-storage.md) Object storage adapters         | S3-compatible                      | accepted (ADR 31)                                 |
| server  | [RFC 34](rfc/0034-server-observability.md) Observing the server      | all                                | accepted (ADR 32)                                 |
| server  | [RFC 38](rfc/0038-upgrade-checks.md) Upgrade checks                  | all                                | accepted (ADR 33)                                 |
| server  | [RFC 58](rfc/0058-large-request-limits.md) Large request limits      | follow-up after the exit test: all | accepted (ADR 45)                                 |
| server  | [RFC 59](rfc/0059-installation-api.md) Installation API              | follow-up after the exit test: all | draft                                             |
| server  | [RFC 60](rfc/0060-maintenance-mode-visibility-wait.md) Instance registry | follow-up after the exit test: all | accepted (ADR 48)                             |
| console | [RFC 18](rfc/0018-state-operations.md) State operations              | version list, restore, lock holder | accepted (ADR 28)                                 |
| console | [RFC 42](rfc/0042-migration-import.md) Migration                     | import progress                    | deferred: ADR 29 has no import API in milestone 3 |

## Milestone 4. People, teams and access

**Goal.** More than one person and more than one CI pipeline can share an
install, each with only the access they need, and every change is on record.

| Where   | Item                                                                         | Part                                     | State |
|---------|------------------------------------------------------------------------------|------------------------------------------|-------|
| server  | [RFC 4](rfc/0004-what-sits-under-a-namespace.md) What sits under a namespace | groups                                   | draft |
| server  | [RFC 19](rfc/0019-users-teams-roles.md) Users, teams and roles               | all                                      | draft |
| server  | [RFC 20](rfc/0020-access-tokens.md) Access tokens and machine identity       | all                                      | draft |
| server  | [RFC 21](rfc/0021-directory-sync-and-sign-in.md) Directory sync and sign-in  | all                                      | discussing; built-in MFA accepted (ADR 46) |
| server  | [RFC 22](rfc/0022-audit-log.md) Audit log                                    | all                                      | discussing; port, entries, export, sinks and opt-in tamper evidence accepted (ADR 60); retention open |
| console | [RFC 19](rfc/0019-users-teams-roles.md) Users, teams and roles               | members, teams, role grants              | draft |
| console | [RFC 52](rfc/0052-console-core.md) Console core views                        | sections with the reason they are locked | draft |

## Milestone 5. Runs

**Goal.** OpenDustpan runs plans and applies itself, on runners, from a pull
request or from the API, with policy checks before an apply.

| Where   | Item                                                                        | Part                            | State |
|---------|-----------------------------------------------------------------------------|---------------------------------|-------|
| server  | [RFC 6](rfc/0006-runners-and-remote-runs.md) Runners and remote runs        | all                             | draft |
| server  | [RFC 5](rfc/0005-profiles.md) Profiles                                      | all                             | draft |
| server  | [RFC 12](rfc/0012-api-edits-and-write-back.md) API edits and write-back     | all                             | draft |
| server  | [RFC 13](rfc/0013-policy-and-approvals.md) Policy and approvals             | all                             | draft |
| server  | [RFC 28](rfc/0028-stored-plans.md) Stored plans                             | all                             | draft |
| server  | [RFC 27](rfc/0027-pull-request-automation.md) Pull request automation       | all                             | draft |
| server  | [RFC 30](rfc/0030-opentofu-remote-execution.md) CLI-driven remote execution | all                             | draft |
| server  | [RFC 23](rfc/0023-events-and-notifications.md) Events and notifications     | all                             | draft |
| server  | [RFC 15](rfc/0015-output-sharing.md) Output sharing                         | all                             | draft |
| server  | [RFC 24](rfc/0024-run-triggers-and-orchestration.md) Run triggers           | all                             | draft |
| console | [RFC 52](rfc/0052-console-core.md) Console core views                       | run page with timeline and diff | draft |

## Milestone 6. Self-service

**Goal.** A platform team publishes blueprints, and other teams create and
promote units from them, inside guard rails.

| Where   | Item                                                                            | Part                    | State |
|---------|---------------------------------------------------------------------------------|-------------------------|-------|
| server  | [RFC 14](rfc/0014-blueprints.md) Blueprints                                     | all                     | draft |
| server  | [RFC 43](rfc/0043-ephemeral-units.md) Ephemeral units                           | all                     | draft |
| server  | [RFC 55](rfc/0055-environment-promotion.md) Environment promotion               | all                     | draft |
| server  | [RFC 10](rfc/0010-artifact-registry-and-curated-upstreams.md) Artifact registry | all                     | draft |
| server  | [RFC 56](rfc/0056-upgrade-pull-requests.md) Upgrade pull requests               | all                     | draft |
| server  | [RFC 16](rfc/0016-egress-proxy.md) Egress proxy                                 | all                     | draft |
| console | [RFC 14](rfc/0014-blueprints.md) Blueprints                                     | catalog and create form | draft |

## Later

Wanted, but not in an ordered milestone yet. Each row says what it waits on.

| Item                                                                        | Waits on                                                 |
|-----------------------------------------------------------------------------|----------------------------------------------------------|
| [RFC 7](rfc/0007-one-binary-many-roles.md) One binary, many roles           | a second role that needs its own process (runner, RFC 6) |
| [RFC 9](rfc/0009-themes-and-white-labeling.md) Themes and white-labeling    | Console core views (RFC 52)                              |
| [RFC 11](rfc/0011-languages-and-locales.md) Languages and locales           | Console core views (RFC 52)                              |
| [RFC 25](rfc/0025-drift-detection.md) Drift detection                       | runs (milestone 5)                                       |
| [RFC 29](rfc/0029-existing-ci.md) Existing CI as the executor               | runs (milestone 5)                                       |
| [RFC 31](rfc/0031-kubernetes.md) Kubernetes                                 | runners (RFC 6)                                          |
| [RFC 33](rfc/0033-customer-managed-keys.md) Customer-managed keys           | secrets (RFC 3, part 2)                                  |
| [RFC 36](rfc/0036-high-availability.md) High availability                   | home regions and fencing in use (ADR 4)                  |
| [RFC 39](rfc/0039-reading-state.md) Reading state contents                  | accepted (ADR 41); a milestone to place it in            |
| [RFC 40](rfc/0040-inventory-queries.md) Inventory queries                   | the inventory projection (ADR 13) at scale               |
| [RFC 41](rfc/0041-agent-interface.md) Interface for AI agents               | read-only tokens (RFC 20)                                |
| [RFC 47](rfc/0047-discovery-and-codification.md) Discovery and codification | runs (milestone 5)                                       |
| [RFC 48](rfc/0048-management-as-code.md) Managing OpenDustpan as code       | a stable REST API for groups and roles (milestone 4)     |
| [RFC 53](rfc/0053-terraform-cli.md) Terraform CLI support                   | OpenTofu (milestone 2) and a license check               |
| [RFC 54](rfc/0054-sources-beyond-git.md) Sources other than git             | runs (milestone 5)                                       |
| [RFC 57](rfc/0057-documentation.md) Documentation as a product              | its research                                             |
| [RFC 61](rfc/0061-docs-site-and-landing-page.md) Docs site and landing page | documentation as a product (RFC 57)                      |
| [RFC 66](rfc/0066-moving-units-between-namespaces-and-installations.md) Moving units | unit lifecycle (RFC 17, ADR 47)                  |
| [RFC 67](rfc/0067-spend-and-cost-tool-integration.md) Spend and cost tools  | a decision on cost estimation (RFC 45)                   |
| [RFC 68](rfc/0068-installation-settings.md) Installation settings          | the shape of the installation API (RFC 59)               |

## Candidates to drop or merge

Not decided. Each row says why it could go, and where it could go.

| Item                                                                   | Proposal                           | Why                                                                   |
|------------------------------------------------------------------------|------------------------------------|-----------------------------------------------------------------------|
| [RFC 26](rfc/0026-unit-status.md) Unit status                          | merge into RFC 52                  | a status bucket is a Console view over data the inventory already has |
| [RFC 44](rfc/0044-bulk-operations.md) Bulk operations                  | merge into RFC 40                  | bulk actions run on a saved view or a query result                    |
| [RFC 49](rfc/0049-run-tasks.md) Run tasks                              | merge into RFC 13                  | a run task is a policy check with a callback                          |
| [RFC 50](rfc/0050-policy-authoring.md) Policy authoring                | merge into RFC 13                  | same subject; split again only if RFC 13 grows too big                |
| [RFC 45](rfc/0045-cost-estimation.md) Cost estimation                  | drop, or an RFC 52 extension point | other tools do it well; OpenDustpan can show their output             |
| [RFC 46](rfc/0046-insights.md) Insights                                | drop, or later                     | needs RFCs 26 and 40 first; low value before many users               |
| [RFC 37](rfc/0037-federation.md) Federation                            | drop                               | one install with namespaces and regions (ADR 4) may cover it          |
| [RFC 51](rfc/0051-change-management-integrations.md) Change management | drop, or later                     | a webhook (RFC 23) may be enough                                      |

## How this file changes

- Change this file only by a pull request.
- The pull request that accepts or drops an RFC also changes its row here.
- A new RFC gets a row in the pull request that adds it: in a milestone, or
  in "Later".
- Moving an RFC to another milestone, or changing the order of milestones,
  needs the owner's approval in the pull request.
- When a milestone's exit test passes, the pull request that makes it pass
  marks the milestone done.
