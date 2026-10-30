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

The first tagged release comes after milestone 7, not here
([ADR 279](adr/0279-no-release-before-milestone-7.md)).

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
| server  | [RFC 4](rfc/0004-what-sits-under-a-namespace.md) What sits under a namespace | collections                              | discussing; collections and CLI placement accepted (ADR 51, ADR 61) |
| server  | [RFC 19](rfc/0019-users-teams-roles.md) Users, teams and roles               | all                                      | discussing; groups, grants and export accepted (ADR 52) |
| server  | [RFC 20](rfc/0020-access-tokens.md) Access tokens and machine identity       | all                                      | discussing; token life cycle, workload identity, service principals and lifetimes accepted (ADR 53, ADR 54, ADR 67, ADR 69) |
| server  | [RFC 21](rfc/0021-directory-sync-and-sign-in.md) Directory sync and sign-in  | all                                      | discussing; accepted in part (ADR 46, ADR 55, ADR 56, ADR 57) |
| server  | [RFC 22](rfc/0022-audit-log.md) Audit log                                    | all                                      | discussing; port, entries, export, sinks and opt-in tamper evidence accepted (ADR 60); retention accepted (ADR 68) |
| console | [RFC 19](rfc/0019-users-teams-roles.md) Users, teams and roles               | members, groups, grants, access-review export | discussing; groups, grants and export accepted (ADR 52) |
| console | [RFC 52](rfc/0052-console-core.md) Console core views                        | sections with the reason they are locked | draft |

## Milestone 5. Runs

**Goal.** OpenDustpan runs plans and applies itself, on runners, from a pull
request or from the API, with policy checks before an apply.

| Where   | Item                                                                        | Part                            | State |
|---------|-----------------------------------------------------------------------------|---------------------------------|-------|
| server  | [RFC 6](rfc/0006-runners-and-remote-runs.md) Runners and remote runs        | all                             | accepted in part (ADR 100-103, 130-139) |
| server  | [RFC 5](rfc/0005-profiles.md) Profiles                                      | all                             | discussing; core accepted |
| server  | [RFC 12](rfc/0012-api-edits-and-write-back.md) API edits and write-back     | all                             | accepted in part |
| server  | [RFC 13](rfc/0013-policy-and-approvals.md) Policy and approvals             | all                             | accepted |
| server  | [RFC 28](rfc/0028-stored-plans.md) Stored plans                             | all                             | accepted |
| server  | [RFC 27](rfc/0027-pull-request-automation.md) Pull request automation       | all                             | draft |
| server  | [RFC 30](rfc/0030-opentofu-remote-execution.md) CLI-driven remote execution | all                             | accepted in part |
| server  | [RFC 23](rfc/0023-events-and-notifications.md) Events and notifications     | all                             | accepted |
| server  | [RFC 15](rfc/0015-output-sharing.md) Output sharing                         | all                             | accepted |
| server  | [RFC 24](rfc/0024-run-triggers-and-orchestration.md) Run triggers           | all                             | accepted |
| console | [RFC 52](rfc/0052-console-core.md) Console core views                       | run page with timeline and diff | draft |

## Milestone 6. Self-service

**Goal.** A platform team publishes blueprints, and other teams create and
promote units from them, inside guard rails.

| Where   | Item                                                                            | Part                    | State |
|---------|---------------------------------------------------------------------------------|-------------------------|-------|
| server  | [RFC 14](rfc/0014-blueprints.md) Blueprints                                     | all                     | accepted; domain core built |
| server  | [RFC 43](rfc/0043-ephemeral-units.md) Ephemeral units                           | all                     | draft |
| server  | [RFC 55](rfc/0055-environment-promotion.md) Environment promotion               | all                     | draft |
| server  | [RFC 10](rfc/0010-artifact-registry-and-curated-upstreams.md) Artifact registry | all                     | accepted in part; providers done |
| server  | [RFC 56](rfc/0056-upgrade-pull-requests.md) Upgrade pull requests               | all                     | draft |
| server  | [RFC 16](rfc/0016-egress-proxy.md) Egress proxy                                 | all                     | draft |
| console | [RFC 14](rfc/0014-blueprints.md) Blueprints                                     | catalog and create form | accepted; domain core built |

## Milestone 7. High availability on one database

**Goal.** An operator can run two or more `opendustpan serve` instances of
one installation behind one load balancer, on one PostgreSQL database and one
S3-compatible bucket. A team can then lose an instance, or deploy a new
release instance by instance, while the Pulumi CLI and OpenTofu keep working.
Background work, backups and restores continue on another instance with no
operator action. High availability of the database and of the object store
stays with the operator, and the docs say what each one must provide
([ADR 280](adr/0280-milestone-7-high-availability-on-one-database.md)).

Out of scope: replication between installations, promotion and forwarding
(ADR 248); active-active across sites; SQLite with more than one instance; the
shared cache (ADR 72 decision 9); a flag for the lease period.

**Minimum supported topology.** Two or more instances; PostgreSQL 16 or
later; an S3-compatible object store, with no requirement for conditional
writes; a backup target on an S3-compatible store. The Helm chart refuses the
`fs`, `dir:` and `file:` settings when `replicas` is more than 1. A later
decision defines how the server works with stores that do not enforce
conditional writes.

**Exit test.** On Linux, on PostgreSQL, with the blobs and the backup target
on an S3-compatible server, the e2e suite starts three `opendustpan serve`
binaries of one installation behind a round-robin TLS proxy in the suite. The
proxy has active health checks on `/health/ready` and sends each request to
the next healthy instance. For each Pulumi CLI and OpenTofu version in the
matrix, the suite does these steps:

1. **Alternation.** It runs the full milestone 1 Pulumi scenario and the full
   milestone 2 OpenTofu scenario through the proxy. The proxy record shows
   that each instance served requests of each tool.
2. **Kill during work.** It starts `pulumi up` and `tofu apply`, each with a
   resource that waits 30 s. It sends SIGKILL to one instance while both run.
   Both commands end with exit 0. The next `pulumi preview` and `tofu plan`
   show no changes, and the secret decrypts.
3. **Kill during a backup.** It starts a backup of at least 500 MB of blobs,
   finds the instance that holds the claim (`GET /api/server/operations` or
   the backup status), and sends it SIGKILL. The backup ends `succeeded`
   within the lease period + 60 s. `opendustpan backup check` reports it
   complete. The operation shows one expiry.
4. **Restart during a restore.** It starts a restore, and restarts each
   instance that does not hold the restore, one after another. The restore
   ends `succeeded`. Then it sends SIGKILL to the instance that holds the
   restore, and starts a new restore. That restore ends `succeeded` on
   another instance with no operator action. Both CLIs read the restored
   state.
5. **Rolling deploy.** It replaces each instance with a binary of a newer
   build, one at a time, while a loop of `tofu apply` and `pulumi up` runs. No
   command fails. While an old binary is in the pool, the schema minimum
   version prevents its writes, with a clear error. For this step only, the
   test fakes a contract migration with a test-only schema. When a `v*` tag
   exists, `make mixed-version-test` passes with the last 2 tags.
6. **Schedules.** It sets each job schedule to a 10 s interval through the
   test settings, runs for 60 s, and checks that each tick ran on exactly one
   instance (`durable_operations` start keys).
7. **Database outage.** It stops PostgreSQL for 60 s while `tofu apply` and
   `pulumi up` run. Every instance answers 503 on `/health/ready` and 503
   (not 500) on API writes, with `Retry-After`. After the database returns,
   the instances turn ready with no restart and both commands complete.
8. **No worker.** With only `api` instances, a backup request and an audit
   chain approval answer 503 with the text that names `--components worker`.

The full Pulumi and OpenTofu scenarios still pass on one instance on SQLite
and on PostgreSQL. The Helm chart CI profile `ci/ha.yaml` installs 3 replicas
with a PodDisruptionBudget and passes `make chart-lint`.

The docs give the recovery figures that this test measures, each with the
date of the measurement. They give no service level agreement and no
compliance claim.

| Where   | Item                                                                                                     | Part                                              | State             |
|---------|----------------------------------------------------------------------------------------------------------|---------------------------------------------------|-------------------|
| server  | [RFC 36](rfc/0036-high-availability.md) High availability                                                | the rest of the scale-out on one database         | accepted (ADR 72) |
| server  | [RFC 105](rfc/0105-purge-and-backup-at-the-same-time.md) Blob deletes while a backup runs                | all: option 3                                     | accepted (ADR 280) |
| server  | High availability on one database (no RFC)                                                               | scope, topology, 503 on store loss, fixed lease   | accepted (ADR 280) |

Work items, in order:

- W1. A restore is a durable operation that a `worker` process runs, with
  fenced steps that can run again after a takeover.
- W2. Version rows of each instance and the minimum version check on the
  write path (ADR 72 decision 11).
- W3. 503 with `Retry-After` when the store is not available, in the error
  shape of each API, and an e2e check that each CLI version retries it.
- W4. The worker check on every route that starts a durable operation.
- W5. Blob deletes wait for a running backup (RFC 105 option 3).
- W6. Limits that each instance counts move into the shared store.
- W7. The Helm chart for several replicas, with the refusal of `fs`, `dir:`
  and `file:` when `replicas` is more than 1, and the `ci/ha.yaml` profile.
- W8. A graceful stop: readiness answers 503 during a drain, and the worker
  releases its claims.
- W9. Readiness for a PostgreSQL failover, and a guide section for the
  settings of a database with high availability.
- W10. The e2e suite for high availability (`make e2e-ha`), with exit-test
  steps 1 to 8.
- W11. Docs: the supported layout, the minimum topology and the measured
  figures, and an explanation page of what OpenDustpan does and what the
  operator does.
- W12. Measured sizing with two and three instances (`make load` and
  `make soak`), with the takeover time and the error count during a kill.

W1, then W4, then W10 is the critical path. W11 closes the milestone.

**First tagged release: `v0.1.0`** ([ADR 21](adr/0021-the-first-tagged-release.md),
timing from [ADR 279](adr/0279-no-release-before-milestone-7.md)). No release
comes before the exit test of each of milestones 1 to 7 passes. Its gate:

- [x] Milestone 1 met.
- [x] Milestone 2 met.
- [x] Milestone 3 met.
- [ ] Milestone 4 met.
- [ ] Milestone 5 met.
- [ ] Milestone 6 met.
- [ ] Milestone 7 met.
- [x] A `LICENSE` in this repository ([ADR 22](adr/0022-apache-2-license.md)).
- [x] A `LICENSE` in the Console repository.
- [x] A `--version` flag that the build stamps.
- [x] An upgrade test from the previous tag (for `v0.1.0`, an empty database).
- [x] The binary refuses to start on a database schema that is newer than it
  knows ([ADR 33](adr/0033-upgrade-checks.md), amendment to ADR 21).
- [ ] Install docs, with TLS ([RFC 35](rfc/0035-packaging.md) accepted first).
- [x] The e2e suite passes for both tools on SQLite and on PostgreSQL.

## Accepted designs that wait for adapters

The owner accepted these designs on 17 and 18 October 2026. Each has its
ports and conformance suites in `pkg/`, and no SQL adapter, HTTP handler or
binary yet. Each row says what the design unblocks. The open work of each
design is in its ADRs.

| Design                                                         | ADRs       | Unblocks                                                                   |
|----------------------------------------------------------------|------------|----------------------------------------------------------------------------|
| The shared job model (`pkg/job`)                               | 100 to 103 | the Catalog, Dusty, the runner protocol and the write-back executor        |
| Runners, the runner protocol, the launcher and sources         | 130 to 138 | remote runs on runners (RFC 6), cloud sweeps (ADR 164)                      |
| Dusty, the built-in agent, the MCP endpoint and the stdio proxy | 120 to 127 | agent runs and agent changes through jobs                                   |
| Unit history, redaction, history policy and safe delete        | 150 to 153 | blame, point-in-time views, redaction, retention of job artifacts          |
| Context, the context read API, the graph and Sweep             | 160 to 165 | the context API, the graph and focus view, the state check, Sweep, findings |

## Later

Wanted, but not in an ordered milestone yet. Each row says what it waits on.

| Item                                                                                               | Waits on                                                        |
|----------------------------------------------------------------------------------------------------|-----------------------------------------------------------------|
| [RFC 7](rfc/0007-one-binary-many-roles.md) One binary, many roles                                  | a second role that needs its own process (runner, RFC 6)        |
| [RFC 9](rfc/0009-themes-and-white-labeling.md) Themes and white-labeling                           | Console core views (RFC 52)                                     |
| [RFC 11](rfc/0011-languages-and-locales.md) Languages and locales                                  | Console core views (RFC 52)                                     |
| [RFC 25](rfc/0025-drift-detection.md) Drift detection                                              | runs (milestone 5)                                              |
| [RFC 29](rfc/0029-existing-ci.md) Existing CI as the executor                                      | runs (milestone 5)                                              |
| [RFC 31](rfc/0031-kubernetes.md) Kubernetes                                                        | runners (RFC 6)                                                 |
| [RFC 33](rfc/0033-customer-managed-keys.md) Customer-managed keys                                  | secrets (RFC 3, part 2)                                         |
| [RFC 37](rfc/0037-federation.md) Namespace placement and routing                                   | installation settings (RFC 68); replication (ADR 4) for installations |
| [RFC 39](rfc/0039-reading-state.md) Reading state contents                                         | accepted (ADR 41); a milestone to place it in                   |
| [RFC 40](rfc/0040-inventory-queries.md) Inventory queries                                          | the inventory projection (ADR 13) at scale                      |
| [RFC 41](rfc/0041-agent-interface.md) Interface for AI agents                                      | read-only tokens (RFC 20)                                       |
| [RFC 47](rfc/0047-discovery-and-codification.md) Discovery and codification                        | runs (milestone 5)                                              |
| [RFC 48](rfc/0048-management-as-code.md) Managing OpenDustpan as code                              | a stable REST API for groups and roles (milestone 4)            |
| [RFC 53](rfc/0053-terraform-cli.md) Terraform CLI support                                          | OpenTofu (milestone 2) and a license check                      |
| [RFC 54](rfc/0054-sources-beyond-git.md) Sources other than git                                    | runs (milestone 5)                                              |
| [RFC 57](rfc/0057-documentation.md) Documentation as a product                                     | its research                                                    |
| [RFC 61](rfc/0061-docs-site-and-landing-page.md) Docs site and landing page                        | documentation as a product (RFC 57)                             |
| [RFC 66](rfc/0066-moving-units-between-namespaces-and-installations.md) Moving units               | unit lifecycle (RFC 17, ADR 47)                                 |
| [RFC 67](rfc/0067-spend-and-cost-tool-integration.md) Spend and cost tools                         | a decision on cost estimation (RFC 45)                          |
| [RFC 45](rfc/0045-cost-estimation.md) Cost estimation                                              | research on spend and cost tools (RFC 67)                       |
| [RFC 68](rfc/0068-installation-settings.md) Installation settings                                  | the shape of the installation API (RFC 59)                      |
| [RFC 69](rfc/0069-community-channels.md) Community channels                                        | the owner sets up the channels                                  |
| [RFC 72](rfc/0072-programmatic-runs-api-and-sdk.md) Programmatic runs through the API and an SDK   | runners (RFC 6) and the run model (milestone 5)                 |
| C# (.NET) and Java clients                                                                         | the REST API is stable (#1281). Go first, then Python and TypeScript (#966). |
| [RFC 73](rfc/0073-ai-agent-for-infrastructure.md) An AI agent for infrastructure across both tools | the interface for AI agents (RFC 41) and access tokens (RFC 20) |

## Candidates to drop or merge

Not decided. Each row says why it could go, and where it could go.

| Item                                                                   | Proposal                           | Why                                                                   |
|------------------------------------------------------------------------|------------------------------------|-----------------------------------------------------------------------|
| [RFC 26](rfc/0026-unit-status.md) Unit status                          | merge into RFC 52                  | a status bucket is a Console view over data the inventory already has |
| [RFC 44](rfc/0044-bulk-operations.md) Bulk operations                  | merge into RFC 40                  | bulk actions run on a saved view or a query result                    |
| [RFC 49](rfc/0049-run-tasks.md) Run tasks                              | merge into RFC 13                  | a run task is a policy check with a callback                          |
| [RFC 50](rfc/0050-policy-authoring.md) Policy authoring                | merge into RFC 13                  | same subject; split again only if RFC 13 grows too big                |
| [RFC 46](rfc/0046-insights.md) Insights                                | drop, or later                     | needs RFCs 26 and 40 first; low value before many users               |
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
