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
| server  | [RFC 35](rfc/0035-packaging.md) Packaging and installation                                   | TLS, binary, container image                                             | accepted (ADR 27, ADR 283) |
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

**Scope.** The owner decided on 2026-10-30 that milestone 4 has no scope
cut. Each row of the table ships as planned.

**Exit test.** On Linux, on SQLite and on PostgreSQL, through the real binary
over TLS, the e2e suite does these steps for each Pulumi CLI and OpenTofu
version in its matrix. The suite runs the test identity providers, a test
SMTP server, a test syslog server and a test OTLP collector itself. Each
step ends with a check of the audit entries that the step must write
(step 12).

1. **Sign-in methods.** For each method of RFC 21 and ADR 55, a person signs
   in, gets a session, creates an access token, and runs `pulumi up` and
   `tofu apply` with that token. The methods are OIDC, LDAP, SAML, CAS, an
   OAuth 2.0 provider with no OIDC (a user-info endpoint and a claim
   mapping), trusted-header sign-in, and a passkey of a local user with no
   password. A trusted header from an address that is not a named proxy
   gives no sign-in. Trusted-header sign-in is off until an administrator
   turns it on.
2. **MFA.** With "require MFA" on (ADR 46), a local user and an LDAP user
   cannot finish a sign-in without a TOTP code or a WebAuthn key. An SSO
   sign-in does not ask for a second factor. A token creation and a token
   reveal without a fresh MFA step give the error of ADR 56.
3. **SCIM.** A SCIM 2.0 client creates a user and a group, and adds the user
   to the group. A grant of the group on a collection lets the user run
   `pulumi up` and `tofu apply` there. The SCIM removal of the user disables
   the sessions and the access tokens of the user and keeps them. The next
   CLI command fails with 401. With the revocation setting on, a removal
   revokes them (ADR 55 decision 9).
4. **Groups, roles and grants.** A grant of `writer` on one collection lets
   the CLIs write the units of that collection and gives 403 on a unit of
   another collection. A grant of `viewer` lets `pulumi stack export` and
   `tofu state pull` read, and refuses `pulumi up` and `tofu apply`. The
   revocation of a grant takes effect on the next request. The access-review
   export lists each grant with its holder, role, object, creator and time
   (ADR 52).
5. **The planner role.** A token of a principal with the `planner` role
   (ADR 212) reads state with both CLIs. `pulumi up`, `pulumi destroy`,
   `tofu apply` and `tofu destroy` give 403. The read of a secret value gives
   403. The start of a plan-only job with `unit:run-plan` is step 3 of
   milestone 5.
6. **Token life cycle and expiry email.** An access token records its
   last-used time. A token past its expiry gives 401 to both CLIs. A token
   with no use for the inactivity period is disabled. Before the expiry, the
   server sends the warning email to the test SMTP server, the API shows the
   warning field, and the CLI shows the warning of ADR 53 decision 11. The
   SMTP settings are those of ADR 212. A service principal gets a token with the lifetime
   limits of ADR 67 and ADR 69.
7. **Workload identity.** A CI job of the suite gets a JWT from a test OIDC
   issuer, exchanges it for an access token (ADR 54), and runs `pulumi up`
   and `tofu apply`. A JWT with a wrong audience, a wrong issuer or a claim
   that no trust rule matches gives 401. A trust rule of one namespace gives
   no access to another namespace.
8. **IP allowlists.** An administrator turns the feature on. The
   installation, a namespace and an access token each opt in to a list
   (ADR 55 decision 8). A console sign-in and a CLI request with the token
   from an address that is not on the list give 403. The same requests from
   an address on the list pass. A scope that did not opt in has no list.
9. **mTLS.** A workload with a client certificate from the trust bundle,
   with a SPIFFE ID in the URI SAN that a trust rule names, gets an access
   token, and the CLIs run with it. A certificate from another CA, an
   expired certificate and an ID that no rule names get no token. Design
   needed: which tool CLIs can send a client certificate, and how mTLS works
   behind a TLS proxy of the operator (open questions of ADR 54). The step
   proves the answer that the design gives.
10. **Collections and placement.** A placement rule on the Pulumi project and
    the stack name puts a new stack into its collection. The TFE project of
    a new workspace puts it into the collection of that project (ADR 61).
    `pulumi stack init` and `tofu init` for a unit that no rule places fail
    with the error that names the missing rule, and the unit does not exist
    (ADR 51 decision 5).
11. **Break-glass mode 2.** An installation set at first boot to mode 2
    (ADR 57) accepts a recovery request that the offline operator key signs,
    and gives one admin session. A request with a wrong signature, or a
    second use of the same request, gives no session. The use opens a
    break-glass review (ADR 142).
12. **Audit log, export and sinks.** Each of steps 1 to 11 writes the audit
    entries of ADR 60. The audit export gives them for a time range. The
    `otlp` sink sends them to the test OTLP collector, and the `syslog` and
    `webhook` sinks send them too. With tamper evidence on, the chain check
    passes, and a changed row fails it. Retention removes entries past the
    period of ADR 68 and writes its own entry.
13. **Console.** The browser tests of the console repository run against the
    server of the suite. They add a member, make a group, give and revoke a
    grant, and download the access-review export. They show the banner of
    a token that expires soon. A section that the principal cannot open
    shows the reason that the server gives.

The full Pulumi and OpenTofu scenarios of milestones 1 to 3 still pass with
an access token of a non-admin principal.

| Where   | Item                                                                         | Part                                     | State |
|---------|------------------------------------------------------------------------------|------------------------------------------|-------|
| server  | [RFC 4](rfc/0004-what-sits-under-a-namespace.md) What sits under a namespace | collections, placement, the refusal of a unit with no collection | discussing; collections and CLI placement accepted (ADR 51, ADR 61) |
| server  | [RFC 19](rfc/0019-users-teams-roles.md) Users, teams and roles               | all, with the planner role               | discussing; groups, grants and export accepted (ADR 52), the planner role (ADR 212) |
| server  | [RFC 20](rfc/0020-access-tokens.md) Access tokens and machine identity       | all: expiry email, workload identity, mTLS | discussing; token life cycle, workload identity, service principals and lifetimes accepted (ADR 53, ADR 54, ADR 67, ADR 69) |
| server  | [RFC 21](rfc/0021-directory-sync-and-sign-in.md) Directory sync and sign-in  | all: OIDC, LDAP, SAML, CAS, OAuth 2.0, trusted header, passkeys, MFA, SCIM, IP allowlists, break-glass mode 2 | discussing; accepted in part (ADR 46, ADR 55, ADR 56, ADR 57); design needed for the open questions of ADR 55 |
| server  | [RFC 22](rfc/0022-audit-log.md) Audit log                                    | all: export, the OTLP sink               | discussing; port, entries, export, sinks and opt-in tamper evidence accepted (ADR 60); retention accepted (ADR 68) |
| console | [RFC 19](rfc/0019-users-teams-roles.md) Users, teams and roles               | members, groups, grants, access-review export | discussing; groups, grants and export accepted (ADR 52) |
| console | [RFC 52](rfc/0052-console-core.md) Console core views                        | sections with the reason they are locked | draft |

## Milestone 5. Runs

**Goal.** OpenDustpan runs plans and applies itself, on runners, from a pull
request or from the API, with policy checks before an apply.

**Scope.** The owner decided on 2026-10-30 that milestone 5 has no scope
cut. Each row of the table ships as planned. The owner also decided:

- **Runners run both tools.** One runner pool runs Pulumi jobs and OpenTofu
  jobs.
- **The policy checks are the gates and approvals of ADRs 222 to 230.** No
  other policy engine comes in this milestone.
- **The first pull-request slice** is a plan, one commit status and one
  summary comment for each pull request.
- **An apply runs after the merge, from the default branch, by default.** A
  pre-merge apply, from the branch of the pull request, is an option of a
  unit. It is not the default. The docs say that the project does not
  recommend it.
- **Review modes 2 and 4 ship in this milestone** (owner, 2026-11-01, ADR
  328). Mode 2 is "OpenDustpan merges", and mode 4 is "no change orders"
  (ADR 299). Modes 1 and 3 ship in milestone 6.

**Exit test.** On Linux, on SQLite and on PostgreSQL, through the real binary
over TLS, the e2e suite starts a runner pool with real runners of the
project, and the fake GitHub API of the suite as the forge. The runners
use the sandboxed process of the machine coordinator (ADR 137). For each
Pulumi CLI and OpenTofu version in the matrix, the suite does these steps:

1. **Runners.** A runner joins the pool with a join token (ADR 133), claims
   jobs of both tools, and reports each one. A job that asks for a label
   that no runner has stays `queued`. A runner that the suite kills during a
   job loses its claim when the lease expires, and another runner takes the
   job (ADR 131). The log streams to the API while the job runs.
2. **A job from the API.** `POST` of a job with the operation `plan`, then
   one with `apply`, for a stack and a workspace. The job runs `pulumi
   preview` and `pulumi up`, and `tofu plan` and `tofu apply`, on the runner.
   The update and the state version arrive through the tool APIs, and the job
   links to them.
3. **The planner role.** A principal with only the `planner` role starts a
   plan-only job (`unit:run-plan`, ADR 212). An apply job of the same
   principal gives 403.
4. **Profiles.** The job opens its profile (ADRs 200 to 209). The tool reads
   its config and its secrets as ADR 209 says for each tool. The job log
   holds no secret value of the profile. A profile change makes a new
   revision, and the next job uses it.
5. **Stored plans.** The apply phase applies the stored plan of the plan
   phase. A state change between the plan and the apply makes the plan
   stale, and the job does not apply it (ADR 191 decision 7). A plan with no
   change ends `succeeded` with no apply (ADR 213 decision 3).
6. **Policy and approvals.** An approval rule on the unit needs two distinct
   people. The apply waits in `awaiting-approval`, and one approval does not
   start it. A new plan resets the approvals (ADR 191 decision 6). The
   approval of a `destroy` needs a fresh MFA step (ADR 212 decision 3). An
   access limit, a Cedar `forbid` policy, refuses an apply, and the decision
   record names the policy (ADR 223). A gate in `enforce` mode refuses a
   plan that the gate does not allow (ADR 224).
7. **Pull request automation, first slice.** A push to a pull request
   starts a plan for each unit that the change touches. The fake GitHub API
   gets one commit status and one summary comment for each pull request. A
   new push edits the same comment and does not add one.
8. **Apply after the merge.** The merge of the pull request starts an apply
   from the default branch at the merge commit. The apply meets the gates
   and approvals of step 6. A push to the pull request branch does not
   apply.
9. **Pre-merge apply, the option.** On a unit that sets the pre-merge apply
   option, an approved pull request applies from its branch. On a unit with
   the default, the same request gives the error that names the option.
   Design needed: RFC 27 is a draft. The step proves the behavior that the
   design gives.
10. **Write-back.** An API edit of a config value of a unit makes a commit on
    the fake forge with the trailer of the job (ADR 101). The plan after the
    commit shows the edit. A forge error that can pass retries, and a forge
    error that cannot pass fails the job.
11. **CLI-driven remote execution.** A workspace with `execution-mode`
    `agent` runs `tofu plan` as a speculative plan on a runner. The CLI
    shows the log of the runner (ADR 254). `tofu apply` with remote
    execution runs the apply on a runner. Design needed: ADR 254 serves
    plans only, and the apply stays open in RFC 30. The step proves the
    behavior that the design gives.
12. **Events and notifications.** A webhook sink with a subscription gets
    the notifications of a job start, a wait for an approval, a job end and
    a token expiry (ADR 53 decision 12). Each delivery has the signature of
    ADR 145 decision 21. A sink that fails gets retries. No notification
    holds a secret.
13. **Output sharing.** A Pulumi job reads an output of an OpenTofu unit,
    and an OpenTofu job reads an output of a Pulumi unit, when a sharing rule
    of the producer names the consumer (ADR 273). With no rule, the read
    gives 403. A secret output needs `unit:read-secrets` of the starter.
14. **Run triggers.** A push to a pull request, a schedule and a `follows`
    trigger each start a plan through the job service (ADR 267). A trigger
    never applies. A `follows` trigger that makes a cycle is refused at save,
    and the error names the path.
15. **Console.** The browser tests of the console repository run against the
    server of the suite. The run page shows the timeline, the live log, the
    masked plan diff and the approval of step 6.

The full Pulumi and OpenTofu scenarios of milestones 1 to 4 still pass from
workstations, with no runner.

| Where   | Item                                                                        | Part                            | State |
|---------|-----------------------------------------------------------------------------|---------------------------------|-------|
| server  | [RFC 6](rfc/0006-runners-and-remote-runs.md) Runners and remote runs        | all                             | accepted (ADR 282); ADR 139 accepted, the runner in its own repository |
| server  | [RFC 5](rfc/0005-profiles.md) Profiles                                      | all                             | discussing; core accepted |
| server  | [RFC 12](rfc/0012-api-edits-and-write-back.md) API edits and write-back     | all                             | accepted in part |
| server  | [RFC 13](rfc/0013-policy-and-approvals.md) Policy and approvals             | all: the gates and approvals of ADRs 222 to 230 | accepted |
| server  | [RFC 114](rfc/0114-review-requests-and-change-orders.md) Review requests and change orders | review modes 2 and 4 | accepted (ADR 299, ADR 328) |
| server  | [RFC 28](rfc/0028-stored-plans.md) Stored plans                             | all                             | accepted |
| server  | [RFC 27](rfc/0027-pull-request-automation.md) Pull request automation       | all; first slice: a plan, one status, one summary comment; apply after the merge by default, pre-merge apply as an option | draft; owner direction 2026-10-30; design needed |
| server  | [RFC 30](rfc/0030-opentofu-remote-execution.md) CLI-driven remote execution | all                             | accepted in part (ADR 254, plans); design needed for the apply |
| server  | [RFC 23](rfc/0023-events-and-notifications.md) Events and notifications     | all                             | accepted |
| server  | [RFC 15](rfc/0015-output-sharing.md) Output sharing                         | all                             | accepted |
| server  | [RFC 24](rfc/0024-run-triggers-and-orchestration.md) Run triggers           | all                             | accepted |
| console | [RFC 52](rfc/0052-console-core.md) Console core views                       | run page with timeline and diff | draft |

## Milestone 6. Self-service

**Goal.** A platform team publishes blueprints, and other teams create and
promote units from them, inside guard rails.

**Scope.** The owner decided on 2026-10-30 that milestone 6 has no scope
cut. Each row of the table ships as planned. Review modes 1 and 3 of ADR 299
ship in this milestone (owner, 2026-11-01, ADR 328). Mode 1 is "merged
approvals", and mode 3 is "no review requests".

**Exit test.** On Linux, on SQLite and on PostgreSQL, through the real binary
over TLS, with the runner pool and the fake GitHub API of the milestone 5
test, the e2e suite does these steps for each Pulumi CLI and OpenTofu
version in its matrix:

1. **Blueprints.** A platform team publishes a blueprint for each tool
   (ADRs 110 to 117). Another team requests a unit from it through the API.
   The server checks the inputs and refuses a request with an input that
   the blueprint does not allow, with an error that names the input. A
   valid request makes the unit in its collection, and a job on a runner
   applies it with `pulumi up` or `tofu apply`. A quota (ADR 115) refuses a
   request past its limit.
2. **Ephemeral units.** A unit from a blueprint has a time to live. Before
   the end, a notification goes to the webhook sink of the suite. At the
   end, a destroy job runs on a runner, and the unit is gone from the unit
   list. A unit with an approval rule waits for the approval
   before the destroy. Design needed: RFC 43 is a draft. The step proves the
   behavior that the design gives.
3. **Environment promotion.** Two units from the same blueprint, for
   example `staging` and `production`, show the difference of their config
   through the API. A promotion copies the config of one unit to the other
   as an edit, with a write-back commit and a plan, and the gates of
   milestone 5 apply. Design needed: RFC 55 is a draft. The step proves the
   behavior that the design gives.
4. **The artifact registry.** `tofu init` gets a private provider and a
   private module from the registry (ADR 257, ADR 269). The Pulumi CLI gets
   a resource plugin from it. A provider with a bad signature or a bad
   checksum is refused.
5. **Curated upstreams.** With curation on, `tofu init` and the Pulumi CLI
   get a public artifact through the registry only when a curation rule
   allows it. An artifact that no rule allows is refused, and the error
   names the artifact. Design needed: the curation part of RFC 10 is open.
   The step proves the behavior that the design gives.
6. **Upgrade pull requests.** A unit pins an old version of a provider or a
   module that the registry has in a newer version. The server opens a pull
   request on the fake forge that changes the pin. Its plan runs as in
   milestone 5 step 7. Design needed: RFC 56 is a draft. The step proves the
   behavior that the design gives.
7. **The job proxy.** A job of a unit with proxy rules runs next to its
   job proxy. The job proxy applies the outbound guard to each rule host. A
   request that no proxy rule allows is refused, and the job log shows
   "blocked by the job proxy" with the host and the path. Design needed
   (RFC 16 P2, P4, P5, P10): the job reaches only the job proxy, the job
   proxy adds the credential and the job never holds it, the record and
   the audit entry of a phase, and a plan phase that cannot make a write
   that only the proxy rule of the apply allows. The step proves the
   behavior that the design gives.
8. **Console.** The browser tests of the console repository run against the
   server of the suite. They open the catalog, fill the create form of a
   blueprint, see the error of a bad input, and make a unit.

The full Pulumi and OpenTofu scenarios of milestones 1 to 5 still pass.

| Where   | Item                                                                            | Part                    | State |
|---------|---------------------------------------------------------------------------------|-------------------------|-------|
| server  | [RFC 14](rfc/0014-blueprints.md) Blueprints                                     | all                     | accepted; domain core built |
| server  | [RFC 43](rfc/0043-ephemeral-units.md) Ephemeral units                           | all                     | draft; design needed |
| server  | [RFC 55](rfc/0055-environment-promotion.md) Environment promotion               | all                     | draft; design needed; in milestone 6 (ADR 331) |
| server  | [RFC 10](rfc/0010-artifact-registry-and-curated-upstreams.md) Artifact registry | all, with curated upstreams | accepted in part (ADR 257); providers done; design needed for curation |
| server  | [RFC 56](rfc/0056-upgrade-pull-requests.md) Upgrade pull requests               | all                     | draft; design needed |
| server  | [RFC 16](rfc/0016-egress-proxy.md) Job proxy                                    | all                     | discussing; accepted in part (ADR 327, ADR 330); items P13 to P17 and P19 wait for the owner |
| server  | [RFC 114](rfc/0114-review-requests-and-change-orders.md) Review requests and change orders | review modes 1 and 3 | accepted (ADR 299, ADR 328) |
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
7. **Database outage.** It stops PostgreSQL in two parts, with the proxy of
   the suite in front of the database.
   - 7a. Long outage. It stops PostgreSQL for 60 s while `tofu apply` and
     `pulumi up` run. Every instance answers 503 on `/health/ready`, and 503
     (not 500) on API writes, with `Retry-After`. After the database returns,
     the instances turn ready with no restart. A command that fails in the
     outage fails with a 503 message and leaves no half-written state version
     and no lock on the workspace. A Pulumi update that the outage stopped
     stays running until `pulumi cancel` ends it. After that, the same
     commands run again and complete, and the next `pulumi preview` and
     `tofu plan` show no changes. The step measures how long each CLI version
     retries one write before the command fails.
   - 7b. Short outage. It stops PostgreSQL for less than the smallest retry
     window of 7a, with a margin. `tofu apply` and `pulumi up` both complete
     with exit 0.
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
- [ ] The shapes that are one-way doors for agents are fixed. The feature
  comes after the first tag (milestone 10), but the shapes cannot change after
  it: the meaning of `principal.kind`, the agent block in the audit hash
  chain, and the `agent` key in the `sub` of the job identity token. If RFC 121
  is not accepted by then, this is an open item of the gate.
- [ ] Install docs, with TLS from files and a reverse proxy
  ([ADR 283](adr/0283-packaging-for-v0-1-0.md)).
- [ ] Release workflows for the binaries, the image and the console tarball
  ([ADR 283](adr/0283-packaging-for-v0-1-0.md)).
- [x] The e2e suite passes for both tools on SQLite and on PostgreSQL.
- [ ] In the commit that it tags: `make regenerate-baseline`, `DocumentVersion`
  in `internal/api/apidesc/document.go` set for the release, a new pin of
  the description, and the lines of the tag in
  `internal/releaserecord/shipped.txt`. The record turns on the checks of
  the shipped migrations and of the description version, and the collapse
  script refuses from then on. `scripts/release-build.sh` refuses a version
  that the record does not list
  ([ADR 325](adr/0325-the-release-record-turns-the-shipped-migration-checks-on.md)).

## Milestone 8. Concurrent applies on one state

**Goal.** Two teams can apply to disjoint parts of one workspace at the same
time, through OpenDustpan runners, and the result is the same as if they had
applied one after the other. A person who runs the `dustpan` CLI on a laptop
can take part in the same rules. OpenDustpan builds this feature itself. It
does not implement the protocol of any other product
([ADR 302](adr/0302-milestone-8-concurrent-applies-on-one-state.md)).

Milestone 8 comes after milestone 7. It is not in the gate of the first
release ([ADR 279](adr/0279-no-release-before-milestone-7.md)).

**Design first.** An RFC for concurrent applies comes before any code. Its
scope is: the change to decision 3 of ADR 26 (the server assigns the serial
under a claim); a parent pointer and a claim ID for each state version; a
fence for each claim; one principal for each job on the TFE-compatible path;
and the change of the rule of ADR 299 that a replacement is never disjoint
(the RFC proposes a closure test); and the retry interval and the timeout
of the local wrapper. ADR 302 does not decide these points.

**Scope.**

- A job registers its saved plan as a claim before the apply. The server
  refuses overlapping claims before anything runs.
- The server serves each job the state version that its plan was made from,
  and merges each upload one resource instance at a time.
- Work that is not provably disjoint takes turns. Six cases from the
  research keep the whole-unit lock: data sources, providers configured from
  resources, root outputs, client-encrypted state, a version or lineage
  mismatch, and a local CLI run without the wrapper. They are the starting
  set. The RFC may add a case. It may not remove one without a decision of
  the owner.
- OpenTofu first. The Pulumi CLI follows after its own spike.
- The local CLI wrapper: `dustpan` wraps the real `tofu` or `pulumi` CLI on
  the machine of the user, as the runner wrapper does.
- Splitting a plan with `-target` comes later, as a speed feature only, on
  top of the claim model.

**Work items.**

- C1. The pre-flight, and the first task of the milestone: the inventory.
  It lists each feature that works only inside OpenDustpan runners today
  because it needs the CLI wrapper. Each entry names the feature, why it
  needs the wrapper and what a local run lacks now. The inventory can mark
  an entry that cannot be ported, with the reason. The owner decides what
  happens to such an entry.
- C2. The RFC for concurrent applies, then the ADR that accepts it.
- C3. The claim model and the strict disjointness rules, for OpenTofu.
- C4. The merge of an upload, one resource instance at a time.
- C5. The local CLI wrapper for OpenTofu, with claims.
- C6. The port of each feature in the inventory (C1) to the local wrapper,
  except an entry that the owner decided not to port.
- C7. A spike with the Pulumi CLI, then its claim model if the spike passes.
- C8. Docs: a guide for the wrapper, and an explanation page of the cases
  that keep the whole-unit lock.

C1, then C2, then C3 is the critical path.

**Exit test.** For a real person: a platform lead has one workspace that
two teams change, and a third person who applies from a laptop. On Linux, on
PostgreSQL, with real OpenTofu and a fake cloud provider that logs each call
with a time, the e2e suite does these steps. Each step passes or fails on the
fact that it names.

1. **Two disjoint claims.** Team A and team B each change a different
   resource in the same workspace. Both runner jobs plan and register a
   claim. Pass: the suite finds two claim records in the state "accepted"
   and no refusal.
2. **Concurrent applies.** Both applies run. Pass: the provider log shows
   that the two apply periods overlap in time, the final state has the
   change of A and the change of B, and the history shows two new state
   versions, and the history shows the claim of each state version. Fail: a resource of A or B is
   lost or reverted.
3. **A runner job with an overlapping claim is refused.** While the apply
   of A runs, team C starts a runner job that changes the resource of A.
   Pass: the server refuses the claim of C, the refusal names the claim of
   A, the job of C ends as "blocked", and the provider log has no call from
   the job of C.
4. **A strict case takes turns.** A change that reads a data source (a case
   of the strict rules) runs while the apply of A runs. Pass: it holds the
   whole-unit lock, it starts its apply only after the apply of A ends, and
   the history names the case.
5. **A local run, two cases.** A person runs `dustpan` with the wrapper
   around `tofu apply` from a laptop.
   - The change overlaps a running claim: the server refuses the claim, and
     the wrapper tries it again until the blocking claim ends, or until a
     timeout. Pass when the provider log has no call from the wrapper until
     the blocking claim ends. After that, the wrapper applies on the new
     state version. The claim record of the local run shows a start time
     after the end time of the blocking claim. If the timeout ends first,
     the wrapper exits with an error and makes no provider call.
   - The change does not overlap any running claim: pass when its claim
     is accepted, its apply period overlaps the running apply in the
     provider log, and the history shows the claim of each state version
     with no lost resource.
6. **A local run without the wrapper.** A person runs plain `tofu apply` on
   the same workspace. Pass: it takes the whole-unit lock, and a runner job
   that starts meanwhile waits for it. The history shows the lock holder.
7. **A runner-only feature in a local run.** Secret masking is a feature
   that works only in runners today: the launcher masks each secret value
   of a phase in the step log (`Masker` in `pkg/launcher`). Pass: a
   local wrapper run of a change that prints a secret value of its profile
   shows the masked value in the log that the server stores, and the
   plain text appears nowhere in the log. The job token credential of the
   launcher, which a run uses to call the server, must work from the wrapper
   in the same step. The inventory (C1) may name more features. Each one
   that the owner did not exempt gets its own check here.

## Milestone 9. Native run-all: fan-out applies

**Goal.** A team that uses Terragrunt can run `terragrunt run-all` and keep
one reviewed change for many units. Locally and in the CI of the team, the
command keeps working unchanged. On OpenDustpan runners, a discovery job finds
the Terragrunt units, and a shim turns the command into one job for each unit,
under a change order. No runner applies all the units in one process
([ADR 304](adr/0304-native-run-all-fan-out-applies-in-milestone-9.md)).

Milestone 9 comes after milestone 8. Profiles and blueprints stay the
recommended path. OpenDustpan supports Terragrunt for teams that already use
it.

**Design first.** [RFC 115](rfc/0115-terragrunt-run-all-fan-out-on-runners.md)
comes before any code. Its scope is: unit discovery, dependency order (ordered
apply jobs, or a change order that does not order its apply jobs), and a
failure in one unit. ADR 304 does not decide these points.

**Scope.**

- Locally and in the CI of a team: `terragrunt run-all` works with no change.
  Each Terragrunt unit talks to its own workspace.
- On runners: a discovery job and a shim in the runner and the launcher. The
  shim uses only the change order API. It turns `terragrunt run-all` into one
  job for each Terragrunt unit, under a change order. The TFE-compatible API
  adapter has no run-all logic.
- The documentation: a guide for Terragrunt with OpenDustpan, and a page that
  says why profiles and blueprints stay the recommended path.

**Exit test.** On Linux, on PostgreSQL, through the real binary over TLS,
with real OpenTofu, real Terragrunt and a test provider that records each
call with a time, the e2e suite does these steps. Each step passes or fails on
the fact that it names.

1. **Local run-all.** `terragrunt run-all apply` on a laptop over 3 units.
   Pass: 3 workspaces, each with 1 new state version and 1 history entry, and
   no change order.
2. **Run-all in the CI of a team.** The same command in a CI job. Pass: the
   result of step 1.
3. **Run-all on a runner.** `terragrunt run-all apply` over 3 units on an
   OpenDustpan runner. Pass: 1 discovery job, 1 change order, 3 plan jobs and
   3 apply jobs, each on its own unit. Each unit has 1 new state version and
   1 history entry. The call record of the test provider shows no unit applied
   by the job of another unit.
4. **The record names the cause.** Pass: the history entry and the audit
   entry of each plan job and each apply job name the discovery job, the
   principal that requested the wrapper job, and the reason "terragrunt
   run-all fan-out".
5. **The review holds.** Pass: the apply jobs start only after the apply
   request of a person, and the call record of the test provider has no apply
   call before it.
6. **A failure in one unit.** One of the 3 units fails at the apply. Pass: the
   failed unit has a failed apply job and no new state version, and the other
   2 units show the result that RFC 115 decides.

## Milestone 10. Agents: Dusty, MCP and agent principals

**Goal.** A person can delegate work to an agent, and the agent can act only
inside that delegation. Dusty, the built-in agent, and agents of other
vendors use the same principals, grants, jobs and audit log as a person.
Every step that an agent takes is audited, and a person confirms each change
that matters. The owner decided on 7 November 2026 that Dusty has its own
milestone.

Milestone 10 comes after milestone 9. No release waits on this milestone. It
is not in the gate of the first release
([ADR 279](adr/0279-no-release-before-milestone-7.md)). The design comes from
wide research and from the review loop of RFC 121, which is in design.

**Design first.** RFC 121 (agent principals and delegations) comes before any
code. It must be accepted as an ADR before the code of this milestone starts.

**Scope.**

- Agent principals and delegations (RFC 121, in design): who an agent is, who
  delegates to it, what the delegation allows, and how a person revokes it.
- Dusty runs and tools: Dusty runs as a job on the shared job model. Its tools
  call the same REST API as every other client, under the grants of the
  delegation.
- The MCP endpoint with OAuth, and the stdio proxy, so that an agent of
  another vendor can connect.
- "Ask the person" approvals: an agent that needs a decision stops and asks a
  person. The answer is a record in the audit log.
- The design of ADR 120 to 127 (the built-in agent, the MCP endpoint and the
  stdio proxy). It has its ports and conformance suites in `pkg/`, and no
  adapter yet.

**Exit test.** On Linux, on PostgreSQL, through the real binary over TLS, the
e2e suite does these steps. Each step passes or fails on the fact that it
names.

1. **A person delegates to Dusty.** Pass: 1 delegation is a stored record
   with a holder, a scope, a creator and a time. A person can list it and
   revoke it.
2. **Dusty proposes a change order.** Pass: the change order exists in state
   "proposed"; its proposer is the person who delegated, and it names the
   agent and the delegation.
3. **A person confirms it.** Pass: the change order moves on only after the
   confirmation of a person. With a two-person approval rule on the namespace,
   the person who delegated cannot be the second approver.
   The audit log has no apply step before it.
4. **Every step is audited.** Pass: each step has an audit entry that names
   the agent, the delegation and the reason. The hash chain of the namespace
   verifies.
5. **A revoke stops the work.** The person revokes the delegation while Dusty
   has running and scheduled work. Pass: all running work of the delegation
   stops, no scheduled work of it starts, and the next request of the agent
   is denied and written to the audit log.

## Accepted designs that wait for adapters

The owner accepted these designs on 17 and 18 October 2026. Each has its
ports and conformance suites in `pkg/`, and no SQL adapter, HTTP handler or
binary yet. Each row says what the design unblocks. The open work of each
design is in its ADRs.

| Design                                                         | ADRs       | Unblocks                                                                   |
|----------------------------------------------------------------|------------|----------------------------------------------------------------------------|
| The shared job model (`pkg/job`)                               | 100 to 103 | the Catalog, Dusty, the runner protocol and the write-back executor        |
| Runners, the runner protocol, the launcher and sources         | 130 to 138 | remote runs on runners (RFC 6), cloud sweeps (ADR 164)                      |
| Unit history, redaction, history policy and safe delete        | 150 to 153 | blame, point-in-time views, redaction, retention of job artifacts          |
| Context, the context read API, the graph and Sweep             | 160 to 165 | the context API, the graph and focus view, the state check, Sweep, findings |

## Later

Wanted, but not in an ordered milestone yet. Each row says what it waits on.

RFCs 107 to 110 hold gaps that were never in the proposal of RFC 6. So their
place here is not a scope cut of milestone 5.

| Item                                                                                               | Waits on                                                        |
|----------------------------------------------------------------------------------------------------|-----------------------------------------------------------------|
| [RFC 7](rfc/0007-one-binary-many-roles.md) One binary, many roles                                  | a second role that needs its own process (runner, RFC 6)        |
| [RFC 107](rfc/0107-run-limits-and-the-run-queue.md) Run limits and the run queue                    | runs (milestone 5)                                              |
| [RFC 108](rfc/0108-run-options-re-runs-and-run-variables.md) Run options, re-runs and run variables | runs (milestone 5)                                              |
| [RFC 109](rfc/0109-job-kinds-for-state-operations-and-many-runs.md) Job kinds for state operations | runs (milestone 5)                                              |
| [RFC 110](rfc/0110-step-results-hooks-approval-steps-and-scanners.md) Step results and scanners     | runs (milestone 5)                                              |
| [RFC 9](rfc/0009-themes-and-white-labeling.md) Themes and white-labeling                           | Console core views (RFC 52)                                     |
| [RFC 11](rfc/0011-languages-and-locales.md) Languages and locales                                  | Console core views (RFC 52)                                     |
| [RFC 25](rfc/0025-drift-detection.md) Drift detection                                              | runs (milestone 5)                                              |
| [RFC 29](rfc/0029-existing-ci.md) Existing CI as the executor                                      | runs (milestone 5)                                              |
| [RFC 31](rfc/0031-kubernetes.md) Kubernetes                                                        | runners (RFC 6)                                                 |
| [ADR 305](adr/0305-our-own-kubernetes-operator-modeled-on-the-pulumi-kubernetes-operator.md) The Kubernetes operator (RFC 31 phases 3 and 4) | the Catalog (blueprints); not yet placed in a milestone. The current order of the owner puts it after runners scoped to namespaces |
| [RFC 33](rfc/0033-customer-managed-keys.md) Customer-managed keys                                  | secrets (RFC 3, part 2)                                         |
| [RFC 37](rfc/0037-federation.md) Namespace placement and routing                                   | installation settings (RFC 68); replication (ADR 4) for installations |
| [RFC 39](rfc/0039-reading-state.md) Reading state contents                                         | accepted (ADR 41); a milestone to place it in                   |
| [RFC 40](rfc/0040-inventory-queries.md) Inventory queries                                          | the inventory projection (ADR 13) at scale                      |
| [RFC 41](rfc/0041-agent-interface.md) Interface for AI agents                                      | read-only tokens (RFC 20)                                       |
| [RFC 47](rfc/0047-discovery-and-codification.md) Discovery and codification                        | runs (milestone 5)                                              |
| [RFC 124](rfc/0124-codegen-for-discovered-resources.md) Code for unmanaged resources               | the sweep plugin milestone (RFC 123) for slices 0 to 6; the agents milestone (milestone 10) for slices 7 and 8 |
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
