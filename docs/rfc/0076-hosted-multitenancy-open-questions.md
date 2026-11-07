---
rfc: 76
title: Hosted multitenancy, the open questions after the namespace decisions
status: discussing
created: 2026-10-18
---

# RFC 76. Hosted multitenancy, the open questions after the namespace decisions

Status: discussing. Design only. The research pass of 7 November 2026
adds the sections from "Research" to "Questions for the owner". The
owner decides.

## Problem

ADRs 215 to 221 record the owner decisions of 2026-10-18: OpenDustpan Cloud
is multitenant, the namespace is the tenant, and each namespace can bring
its own key. Those ADRs leave these questions open. Each one has options
and a recommendation. The owner decides. ADR 248 and RFC 88 hold the federation model
and asynchronous geo-replication. RFC 79 is dropped.

## 1. A path prefix for each namespace in the CLI backend URL

ADR 215 decision 14: the Pulumi CLI keeps one token for each backend URL,
and OpenTofu one token for each host. A person in two namespaces on one
host re-logs in, or sets a token for each namespace in the environment
(ADR 215 decision 15).

- **Option A. Only the environment variables of ADR 215 decision 15.** No
  server change.
- **Option B. Also a path prefix for each namespace**, for example
  `pulumi login https://app.opendustpan.com/acme`. The Pulumi CLI would
  keep one token for each URL. It works only if the Pulumi CLI keeps the
  path of the backend URL in each request. OpenTofu finds its API through
  `/.well-known/terraform.json` on the host, so a path prefix does not help
  OpenTofu.

**Recommendation: A now.** Measure B with `make e2e` against each
supported version of the Pulumi CLI. Add it only if every version keeps the
path.

## 2. A move of a principal of the installation into a namespace

ADR 215 decision 11: a change of `identity.principalHome` converts nothing.
A self-hosted installation that changes to `namespace` keeps its people as
principals of the installation.

- **Option A. No move.** People sign in again through the identity provider
  of a namespace and get a new principal there. The administrator makes
  the grants again.
- **Option B. A move operation**, `POST .../principals/{id}/move`, that
  gives the principal a namespace home, if each grant of the principal is
  inside that namespace.

**Recommendation: A** until a self-hosted operator asks for B.

## 3. The role `account-administrator`

`pkg/access/roles.go` has `account-administrator` for "the tenant side of a
hosted installation". With the namespace as the tenant, the namespace role
`admin` does that job (ADR 216 decision 17).

- **Option A. Delete the role.** No tag shipped it (ADR 73), so the server
  keeps no alias. A grant that holds it fails the migration check.
- **Option B. Keep it as an installation role** with the administration
  actions of the installation directory only.

**Recommendation: A.** The word "account" is a console label only (ADR 215
decision 3), so a role name with it misleads.

## 4. The notices of a suspension

ADR 215 decision 16 sends notices at the suspension, 7 days before the
`disabled` state, and 7 days and 1 day before the delete.

- **Question:** are these days right? Who can resume a `disabled`
  namespace: the operator only, or also a namespace administrator after the
  cause ends? Do exports work in the `disabled` state, so that a tenant can
  take its data before the delete? The owner gave the periods of 30, 31 and
  90 days. The notice days are a proposal.

## 5. The open parts of a namespace backup

ADR 218 decision 4 lets a namespace back up and restore in place.

- **Restore as a copy into a new namespace.** Recommendation: allow it, with
  a new namespace ID and no issuer keys (the copy case of ADR 204 decision
  4).
- **Schedules for each namespace.** Recommendation: wait for the backup
  schedule of the installation (ADR 30 defers it), then add a namespace
  scope to it.
- **An export to the tenant.** A download holds no item keys (ADR 218
  decision 4), so it opens only on its installation. A tenant that leaves
  reads its data with the tool CLIs (`pulumi stack export`, `tofu state
  pull`).
  - **Option A. Document the CLI path only.**
  - **Option B. An export with item keys** only with a customer key that
    wraps them.
  - **Option C. A decrypted export of each history item of each unit**,
    with a fresh MFA step (ADR 56) and an audit entry in the scope of the
    namespace.

  Recommendation: document the CLI path now, and add the decrypted export
  of option C before OpenDustpan Cloud opens.

## 6. The client of each KMS adapter

ADR 217 decision 7 gives one port that fits three kinds of client. The
owner asked for a measurement before a choice.

- **Option A. The SDK of each cloud.** Four large dependencies.
  `CLAUDE.md` asks the owner before each new dependency.
- **Option B. Hand-written clients on `net/http`.** ADR 31 made the S3
  adapter with no new dependency, and its request signing can serve AWS
  KMS too. Each KMS needs a few calls only.
- **Option C. Adapters outside the server**, over a small protocol on a
  Unix socket, as Kubernetes KMS v2 plugins are, or over the external
  adapter protocol of ADR 208. The server ships no KMS client. The
  operator runs one adapter process for each KMS.

**The measurement.** For each of AWS KMS, Google Cloud KMS, Azure Key Vault
and Vault Transit, and for each option:

| Measure | How |
| --- | --- |
| Modules that the binary links | `go list -m all` before and after |
| Size of the binary | bytes before and after |
| Known vulnerabilities | `govulncheck` now, and the advisories of the last 12 months for those modules |
| Licenses | `make licenses` and `make trivy` |
| Lines that we keep | lines of the adapter and its tests |
| Latency of one wrap and one unwrap | p50 and p99 of 1,000 calls against the real service, from one region |
| Conformance | the `Wrapper` suite and the error-class suite pass |
| Auth paths | workload identity, static keys, the identity of ADR 217 decision 9 |
| Work for an operator | processes to run and to upgrade (option C) |

**Recommendation:** run the measurement for AWS KMS first, with options B
and C, then decide for all four.

## 7. A new data key for a Pulumi stack after a compromise

ADR 217 decision 20: a full re-encryption cannot change the data key of a
Pulumi stack, because the Pulumi CLI keeps ciphertexts of it in the stack
configuration.

- **Option A. The job lists the stacks**, and the docs give the steps: a
  new stack data key, and `pulumi config` with each secret again.
- **Option B. A second data key for the stack**: new secrets use it, and old
  ciphertexts still open with the old key until the team writes them
  again.

**Recommendation: B.** New Pulumi ciphertexts are tagged with a key
version, so the server knows which data key opens each one. A stays as the
docs for a compromise: the steps that write each secret again. `make e2e`
must show that each supported version of the Pulumi CLI keeps the tagged
ciphertexts unchanged.

## 8. `identity.principalHome: namespace` on SQLite

SQLite has no row-level security (ADR 220 decision 6).

- **Option A. Refuse it.** The server refuses to start with `namespace` on
  SQLite. The error names PostgreSQL.
- **Option B. Allow it**, with the query checks of ADR 220 decision 5
  only.

**Recommendation: A.** A team that needs tenants with their own members
runs PostgreSQL.

## 9. A lock that turns a feature off on OpenDustpan Cloud

ADR 219 lets the server lock a feature off. ADR 59 decision 1 says that the
hosted service never turns a feature off for its customers.

- **Option A. Keep ADR 59 decision 1.** The hosted operator locks limits and
  defaults only. The locks of ADR 215 decision 10 and ADR 216 decision 7
  set where principals live and what an operator grant reaches. They turn
  no feature off.
- **Option B. Allow it for a module that is not a security switch**, for
  example an experimental module before its release, with the notice of
  ADR 219 decision 4.

**Recommendation: A.** It keeps the promise of ADR 59. The owner decides.

## 10. Billing

The owner did not decide billing. ADR 215 decision 18 gives the server no
billing object and no usage counter for billing. This RFC asks nothing about
billing now. A later RFC asks it.

## 11. One cookie jar on one host

ADR 215 decision 4 puts each namespace on one host. A browser keeps one
cookie jar for each host. A person with principals in two namespaces
(ADR 215 decision 12) cannot keep two sessions in one cookie of that host.

- **Option A. A session cookie for each principal home**, with the name of
  the cookie bound to the namespace ID, for example one cookie for each
  namespace ID. The console sends the cookie of the namespace of the path. A browser sends every cookie of the host. The server reads only the cookie of the namespace in the path, and the session cookie of the installation for a route with no namespace.
- **Option B. One active namespace for each browser session.** A change of
  namespace signs the person in again.

**Recommendation: A**, with one cookie for each namespace ID.

## Research: the solved patterns, and how they fit

Checked on 7 November 2026, from public primary sources. Each source is a
comparison only, never a compliance claim.

**A note on the word "tenant".** ADR 59 decision 6 gave each hosted
customer its own installation. ADR 215 decision 1 replaced that sentence:
the namespace is the tenant, and OpenDustpan Cloud is one shared
installation. This RFC follows ADR 215. The installation keeps one role in
this RFC: it is the unit of placement, the "cell" of the bridge model
below (ADR 248).

### R1. Silo, pool and bridge

The AWS SaaS Lens names three models
(<https://docs.aws.amazon.com/wellarchitected/latest/saas-lens/silo-pool-and-bridge-models.html>):

- **Silo**: dedicated resources for each tenant, for example one database
  for each tenant, with a shared identity, onboarding and operations.
- **Pool**: tenants share compute, storage and messaging.
- **Bridge**: some parts in silo and some in pool. The Lens names two
  reasons to silo a part: the regulatory profile of its data and its
  noisy-neighbor behavior.

The Lens also says that the tenant context is bound to the identity at
sign-in and goes with each request, so that a service applies it with no
lookup in another service
(<https://docs.aws.amazon.com/wellarchitected/latest/saas-lens/identity-and-access-management.html>).

**The fit.** OpenDustpan is a bridge with two levels:

| Level | Model | Boundary | Decided by |
| --- | --- | --- | --- |
| Inside one installation | pool | the namespace ID in each row, row-level security, a blob prefix and a namespace key | ADR 220, ADR 217 |
| Across installations | silo of a group of namespaces (a cell) | a database, a blob store and a key-encryption key for each installation | ADR 248 |

OpenDustpan Cloud starts with one installation that scales out on one
database (ADR 215 decision 21). A second installation is a second cell, not
a dedicated tier: it runs the same build and holds many namespaces (ADR 59
decision 1). The namespace is the unit of placement and of failover (ADR
248 decision 8), so a planned promotion can move one namespace to another
cell. This is the last tool against a noisy neighbor (R3), and it needs
no schema change.

GitLab.com uses the same shape. Cells let "different organizations be
served by distinct physical instances of GitLab", and each model that holds
customer data must define a sharding key so that each row belongs to one
organization (<https://docs.gitlab.com/development/cells/>). ADR 220
decision 1 is that rule: each row of a namespace holds its namespace ID.

### R2. PostgreSQL: row-level security, schemas, databases

ADR 220 chose pooled rows with row-level security. The research supports
it and adds six details that ADR 220 does not say.

- **Scale.** Crunchy Data gives database per tenant at "10s of tenants",
  schema per tenant at "100s" to "several thousand", and shared tables with
  a tenant column at "millions of tenants"
  (<https://crunchydata.com/blog/designing-your-postgres-database-for-multi-tenancy>).
  Citus reports that a schema change across thousands of schemas takes
  hours (<https://www.citusdata.com/blog/2016/12/18/schema-sharding-lessons/>).
- **D1. Set the value with a placeholder.** `SET LOCAL` takes no bind
  parameter, so code that builds it puts text into SQL. The function
  `set_config('dustpan.namespace_ids', $1, true)` takes a placeholder, and
  `true` makes it local to the transaction
  (<https://www.postgresql.org/docs/current/functions-admin.html>). PgBouncer
  in transaction mode does not support a session `SET`
  (<https://www.pgbouncer.org/features.html>), and a local setting ends
  with the transaction.
- **D2. Read the value with `missing_ok`.** The policy reads
  `current_setting('dustpan.namespace_ids', true)`, which answers NULL when
  no value is set. A NULL matches no row. This is the "no namespace set,
  no row" rule of ADR 220 decision 3, with no error path that a caller can
  catch and skip.
- **D3. A restrictive policy.** Permissive policies combine with OR, and
  `AS RESTRICTIVE` policies combine with AND
  (<https://www.postgresql.org/docs/current/ddl-rowsecurity.html>). The
  namespace policy is `AS RESTRICTIVE`, so a permissive policy that a later
  change adds can never widen it.
- **D4. Constraints bypass row-level security.** The PostgreSQL docs say
  that unique, primary key and foreign key checks "always bypass row
  security". A unique constraint on a value that a tenant chooses tells the
  tenant that another namespace holds the value. So each unique constraint
  on a namespace table holds the namespace ID, except a constraint on a
  random ID that the server makes. Namespace names stay unique in the
  installation by design (ADR 215 decision 5).
- **D5. Performance.** The namespace ID leads each index of a namespace
  table. The policy reads the setting once for each statement, in the form
  `(SELECT current_setting(...))`, which the planner runs one time. Only a
  helper function that is safe is marked `LEAKPROOF`. Supabase gives the
  same advice
  (<https://supabase.com/docs/guides/database/postgres/row-level-security-performance>).
  A test runs `EXPLAIN` on the hot queries and fails on a sequential scan
  of a namespace table.
- **D6. Backups are never partial in silence.** The operator role with
  `BYPASSRLS` runs the installation backup. A backup that runs with any
  other role sets `row_security = off`, so that PostgreSQL raises an error
  instead of a filtered dump.

### R3. Noisy neighbors

ADR 220 decisions 13 and 14 give a share of each shared resource, and an
exemption for work in progress. The research adds:

- **Timeouts for each transaction.** PostgreSQL says to set
  `statement_timeout`, `lock_timeout` and `transaction_timeout` for a role
  or a transaction, not for the whole server
  (<https://www.postgresql.org/docs/current/runtime-config-client.html>).
  The request transaction sets them with `set_config(..., true)`, from
  installation settings that the API shows. The request role has an
  `idle_in_transaction_session_timeout`.
- **Fair queues.** Kubernetes API Priority and Fairness puts each flow in
  a few of many queues by shuffle sharding, refuses with 429 when a queue
  is full, and keeps an `exempt` level so that a bad configuration never
  locks out the operators
  (<https://kubernetes.io/docs/concepts/cluster-administration/flow-control/>).
  The AWS Builders' Library shows that 2 of 8 workers for each customer
  give 28 shards, so one bad customer reaches about 1/28 of the service
  (<https://builder.aws.com/content/3F06NpJ8YeoIGP8VHTw4n81pFn8/workload-isolation-using-shuffle-sharding>).
  For OpenDustpan, the 5-second wait of ADR 220 decision 13 is a queue for
  each namespace. Shuffle sharding is for a later shared runner pool, not
  for version 1.
- **Overrides for each tenant.** Grafana Mimir has a default for each limit
  and an override for one tenant, in a runtime file that it reads again
  every 10 seconds
  (<https://grafana.com/docs/mimir/latest/configure/about-runtime-configuration/>).
  ADR 220 decision 13 lets a namespace only lower a limit. Question 5 asks
  whether the operator can raise one limit for one namespace.
- **A test with a heavy tenant.** AWS asks for tests where "a subset of
  your tenants place a disproportionate load on your system"
  (<https://aws.amazon.com/blogs/apn/testing-saas-solutions-on-aws/>). The
  load harness (`make load`) gets that case: one namespace at 10 times its
  share, and the p99 latency of a second namespace stays within 20 percent
  of a run with no heavy namespace (the number is a proposal).

### R4. Keys for each tenant

ADR 217 gives the hierarchy: a key-encryption key for the installation, an
optional customer key, a namespace key, then data keys and item keys. The
research adds one gap and one confirmation.

- **The gap: KMS calls of the operator.** With the key-encryption key in a
  KMS of the operator, each open of a namespace key version is one KMS
  call. ADR 217 decision 12 opens each namespace key again at half the
  cache time, 150 seconds by default. 10 instances and 20,000 active
  namespaces give about 1,300 calls each second against one KMS key. AWS
  KMS shares one quota of 10,000 to 100,000 symmetric calls each second for
  each account and region, and 1,800 for an external key store
  (<https://docs.aws.amazon.com/kms/latest/developerguide/requests-per-second.html>).
  Kubernetes KMS v2 solves the same problem: the KMS wraps one seed, the
  server keeps the open seed in memory and derives keys from it, and the
  `Status` call checks the key each minute
  (<https://kubernetes.io/docs/tasks/administer-cluster/kms-provider/>).
  **Proposal:** the KMS of the operator wraps one installation key with
  versions. The installation key wraps each namespace key version. The
  server keeps the open installation key in memory, opens it at the start
  and at each rotation, and calls `Status` each minute. KMS calls then do
  not grow with the count of namespaces. A customer key still wraps its own
  namespace key directly, so a customer revoke still works as ADR 217
  decision 14 says. This changes the stored wrap chain, so it is a one-way
  door.
- **The confirmation.** HCP Terraform "hold your own key" (generally
  available on 31 July 2025) makes a data key for each workspace and keeps
  the key-encryption key in the customer's KMS
  (<https://developer.hashicorp.com/terraform/cloud-docs/hold-your-own-key/concepts>).
  Pulumi Cloud manages a key for each stack, and its customer keys cover
  ESC data and AWS KMS only
  (<https://www.pulumi.com/docs/esc/concepts/customer-managed-keys/>). ADR
  217 covers more than either.

### R5. Tests that prove isolation

ADR 220 decisions 16 to 22 give a test for each layer. The research adds:

- **Each layer alone.** OWASP asks to test row-level security through the
  role and the pool mode of the application, and to check that "the second
  request cannot observe the first request's tenant context"
  (<https://cheatsheetseries.owasp.org/cheatsheets/Multi_Tenant_Security_Cheat_Sheet.html>).
  The route test of ADR 220 decision 17 runs two times: once with the
  `namespace_id` filter of the queries deleted (a build tag of the test
  only), and once on SQLite, with no row-level security. Each run must
  pass alone. This is the rule of `CLAUDE.md` that each layer stops the
  attack alone.
- **One pooled connection, two transactions.** A test runs a transaction
  of namespace A that fails at its end, then a transaction with no value on
  the same connection, and checks that the second sees no row.
- **A test for each table.** A test reads the catalog and fails for each
  table of the baseline that has neither a namespace ID with the
  restrictive policy, nor a place on an explicit list of installation
  tables. A new table can then not skip row-level security by accident.
- **A unique constraint test** for D4.
- **Canary namespaces.** OpenDustpan Cloud runs two namespaces of its own
  that try to read each other each minute through the public API, with the
  Pulumi CLI and OpenTofu. A success is an incident. AWS says that tests
  that check the enforcement all the time are essential
  (<https://aws.amazon.com/blogs/apn/testing-saas-solutions-on-aws/>). This
  is an operation of the hosted distribution, not code in the server.

### R6. How four hosted products partition tenants

| Product | Tenant | Partition | Source |
| --- | --- | --- | --- |
| GitLab.com | organization | Database split by function (`main`, `ci`), then cells. A topology service claims unique names across cells and finds the cell of a request. | <https://docs.gitlab.com/development/cells/>, <https://about.gitlab.com/blog/splitting-database-into-main-and-ci/> |
| Grafana Cloud (Mimir) | tenant ID in `X-Scope-OrgID`, set by an authenticating proxy | Shared clusters, limits with an override for each tenant, tenant IDs from a restricted set of characters, at most 150 bytes | <https://grafana.com/docs/mimir/latest/manage/secure/authentication-and-authorization/>, <https://grafana.com/docs/mimir/latest/configure/about-tenant-ids/> |
| HCP Terraform | organization | Shared service. Agents for private networks. A separate service for Europe, with no move of an existing organization. A data key for each workspace with HYOK. | <https://developer.hashicorp.com/terraform/cloud-docs/europe>, <https://developer.hashicorp.com/terraform/cloud-docs/hold-your-own-key/concepts> |
| Pulumi Cloud | organization | Shared service, a key for each stack, a choice of secrets provider for each stack | <https://www.pulumi.com/docs/iac/concepts/secrets/> |

The pattern: the tenant is the organization (here the namespace, ADR 18),
the rows are pooled, and scale-out is by cells, not by a stack for each
customer. GitLab needs a claim service for names across cells. ADR 248
decision 9 has no namespace directory with one writer. So two cells behind
one host could each make the namespace `acme`. That is a door before the
second installation behind `app.opendustpan.com`, not before the first tag.
A later RFC designs the claim of a name across installations.

### R7. Exit, suspension and delete

- Atlassian keeps data 15 days (free) or 60 days (paid) after a
  deactivation
  (<https://support.atlassian.com/subscriptions-and-billing/docs/reactivate-a-subscription/>).
  GitHub can restore a deleted repository for 90 days
  (<https://docs.github.com/en/repositories/creating-and-managing-repositories/restoring-a-deleted-repository>).
  The 30, 31 and 90 days of ADR 215 are in this range.
- GDPR Article 28(3)(g): at the end of the service, the processor deletes
  or returns all the personal data, at the choice of the controller
  (<https://gdpr-info.eu/art-28-gdpr/>). So a tenant must be able to take
  its data out before the delete. This supports exports in the `disabled`
  state (question 2) and the decrypted export of section 5 (question 3).
- OWASP gives the order of an exit: block new work, revoke credentials,
  and delete a key only after each copy under it has passed its retention
  (<https://cheatsheetseries.owasp.org/cheatsheets/Multi_Tenant_Security_Cheat_Sheet.html>).
  ADR 217 decision 18 already waits for the backups before it destroys a
  namespace key version.

## Recommended design

One line for each section of this RFC. Questions 1 to 5 below ask the
owner for each one that is not settled.

| Section | Recommendation |
| --- | --- |
| 1. Path prefix in the backend URL | A: environment variables only. Measure B with `make e2e` later. |
| 2. Move of a principal | A: no move. |
| 3. `account-administrator` | A: delete the role. |
| 4. Suspension notices | Notices on day 0, day 24, day 83 and day 89. Only the operator resumes. Exports work in `suspended` and in `disabled`. |
| 5. Namespace backup | Restore as a copy into a new namespace: yes. Schedules: wait for the installation schedule. Exit: the decrypted export of option C before OpenDustpan Cloud opens. |
| 6. KMS clients | B: hand-written clients on `net/http`, AWS KMS and Vault Transit first, plus the installation key of R4. The measurement of section 6 is the acceptance test of each adapter, not a gate before the choice. |
| 7. A new Pulumi data key | B: a second data key, with a key version in each new ciphertext. |
| 8. SQLite | A: refuse `namespace` on SQLite. |
| 9. A lock that turns a feature off on Cloud | A: keep ADR 59 decision 1. |
| 10. Billing | Out of scope. |
| 11. Cookies | A: one session cookie for each namespace ID. |
| R2 | D1 to D6 become part of ADR 220 decision 3. |
| R3 | Timeouts for each transaction. A heavy-tenant case in `make load`. |
| R5 | The tests of R5, in the same change as the tests of ADR 220. |

**Why option C for the export (section 5).** A tenant that leaves must get
its state, its history and its secrets in a form that opens outside the
installation. Option B needs a customer key, which most tenants do not
have. Option C writes each history item decrypted into an archive, with
the secret values encrypted again under a public key that the namespace
administrator gives (an age X25519 recipient, for example). The archive
then never holds a plain secret on the disk of the person who downloads it.
It needs a fresh MFA step and writes an audit entry in the namespace scope.

**Why the operator only resumes (section 4).** The operator suspended the
namespace for a reason outside the namespace. A namespace administrator who
could resume would undo that act. The namespace administrator can always
export, deprovision and read the notices.

## One-way doors before the first tag

1. **The wrap chain of a namespace key version** (R4). The records and the
   backups hold each wrap and its wrapper IDs (ADR 217 decision 4). An
   installation key between the KMS and the namespace keys must exist from
   the first tag, or each installation keeps the old chain for ever.
2. **The key version in each new Pulumi ciphertext** (section 7). The
   Pulumi CLI stores ciphertexts in `Pulumi.<stack>.yaml` in the
   repositories of the users. A format that a tag ships stays in those
   files. The tag of the key version must be in the first format.
3. **The reserved namespace names** (ADR 215 decision 6). A name that a
   later release reserves breaks each namespace that holds it. Reserve the
   names now that a console or a hosted site can need: for example `app`,
   `www`, `admin`, `static`, `assets`, `docs`, `status`, `login`, `logout`,
   `signup`, `settings`, `billing`, `support`, `help`, `new`, `explore`,
   each name with a leading `.`, `-` or `_`, and each name of one or two
   characters.
4. **The export format** (section 5, option C). A tenant keeps the archive
   and opens it with a later release. The archive has a format version in
   its manifest from the first release.
5. **Unique constraints with the namespace ID** (D4). A constraint that a
   tag ships without the namespace ID needs a migration later. ADR 322
   lets the baseline change in place until the first tag.

Not a door before the first tag: the name claim across installations (R6),
the notice days (installation settings), the limits (settings), and the
cookie names (a browser drops them at the end of the session).

## Slices, each with its e2e step

1. **Row-level security in the request path.** `set_config` with a
   placeholder, the restrictive policies, the three roles, D1 to D6, and
   the tests of ADR 220 decisions 16 to 22 and R5. **e2e:** the step of
   ADR 220 decision 21. The Pulumi CLI and OpenTofu, each with the token of
   namespace A, list, read, lock and write in A, and get a not-found error
   for each stack and workspace of B.
2. **Namespace states and notices** (ADR 215 decision 16, section 4).
   **e2e:** the operator suspends namespace A. `pulumi up` fails and prints
   the message of `namespace.suspended` with the date of the next state.
   `pulumi stack export` and `tofu state pull` work. `tofu apply` fails.
   An update that started before the suspension ends. In `disabled`, the
   export of slice 5 still works for a namespace administrator.
3. **Two namespaces on one host** (sections 1 and 11). **e2e:** one person
   with a principal in A and one in B sets `PULUMI_ACCESS_TOKEN` and
   `TF_TOKEN_<host>` for each project. Each CLI works in its namespace. The
   token of A on a path of B answers not found.
4. **The installation key and the first KMS adapter** (section 6, R4).
   **e2e:** HashiCorp Vault Transit in a container as the KMS of a customer
   key. `pulumi config set --secret`, then `pulumi up` works. The step
   disables the key in Vault. `pulumi preview` fails with the message of
   `namespace.key-revoked` before the cache time plus the grace ends. The
   step enables the key, and `pulumi preview` works again.
5. **The decrypted export** (section 5, option C). **e2e:** export
   namespace A, decrypt the archive with the test key, then
   `pulumi stack import` and `tofu state push` into a fresh installation.
   `pulumi preview` and `tofu plan` show no change.
6. **A second Pulumi data key** (section 7). **e2e:** for each supported
   version of the Pulumi CLI, a secret set before the new data key still
   opens, a new `pulumi config set --secret` gets the new key version, and
   `pulumi up` keeps both ciphertexts unchanged in `Pulumi.<stack>.yaml`.

## Questions for the owner

Each question stands alone. The recommendation is first.

### Question 1. Accept the recommendations that add no new mechanism?

Sections 1, 2, 3, 8, 9 and 11: environment variables and no path prefix
for the CLI, no move of a principal, delete `account-administrator`,
refuse `namespace` on SQLite, no lock that turns a feature off on
OpenDustpan Cloud, and one session cookie for each namespace.

- **A. Accept all six (recommended).**
- **B. Accept some.** Name the sections to discuss.

**ELI5.** These six answers each say "do not build a new thing yet". We
can build any of them later without breaking anything.

### Question 2. The suspension timeline

The owner gave 30 days read-only, `disabled` after 31 days, and a delete
after 90 days.

- **A (recommended).** Notices on day 0, day 24, day 83 and day 89. Only
  the operator resumes. A namespace administrator can export in both
  `suspended` and `disabled`, with a fresh MFA step.
- **B.** The same notices, and a namespace administrator can also resume
  after the cause ends.
- **C.** No export in `disabled`.

**ELI5.** When a shop closes your account, you get letters before each
step, and you can always take your things home until the last day. Only
the shop opens the door again.

### Question 3. How a tenant takes its data when it leaves

- **A.** Document `pulumi stack export` and `tofu state pull` only. This
  gives the current state, but no history and no audit log.
- **B.** An export with item keys, only with a customer key.
- **C (recommended).** A decrypted export of each history item of each
  unit, with the secret values encrypted to a public key of the namespace
  administrator, a fresh MFA step and an audit entry. Before OpenDustpan
  Cloud opens.

**ELI5.** When you move out, you get every box, and the box with the
jewelry is locked with your own padlock.

### Question 4. KMS clients, and one installation key between the KMS and the namespaces

- **A (recommended).** Hand-written clients on `net/http` (no new
  dependency), AWS KMS and Vault Transit first. The KMS of the operator
  wraps one installation key, which wraps each namespace key version (R4).
  The measurement of section 6 is the acceptance test of each adapter.
- **B.** The SDK of each cloud. Asks the owner for four large
  dependencies.
- **C.** Adapters outside the server, on a Unix socket.

**ELI5.** We keep one master key in the bank vault and take it out once
in the morning, instead of going to the bank for each drawer in the
building.

### Question 5. Can the operator raise one limit for one namespace?

ADR 220 decision 13 lets a namespace only lower a limit. A large tenant
of OpenDustpan Cloud can need more than the installation value.

- **A (recommended).** Yes, as an operator setting on the namespace, as
  Grafana Mimir does. The settings of the namespace show the effective
  value and that the operator set it. Each change writes an audit entry
  in the namespace scope. It changes a limit, never a feature (ADR 59
  decision 3).
- **B.** No. Each namespace has the installation values. A tenant that
  needs more runs its own installation.

**ELI5.** Everyone gets the same size of locker. If one team really needs
a bigger one, the building manager can give it, and writes it on the
door.

## Outcome

None yet.
