---
rfc: 111
title: Blueprint rollouts and the migration of answers
status: discussing
created: 2026-10-30
---

# RFC 111. Blueprint rollouts and the migration of answers

Status: discussing. A proposal that is ready to accept is in "Proposal". RFC 14
is accepted, and its outcome says that a question that ADRs 110 to 117 do
not decide needs a new RFC. This is that RFC for two open questions of
[ADR 112](../adr/0112-blueprints-the-document-versions-and-expr.md):

1. Does a rollout of a new version to many requests need a batch?
2. Who migrates the answers of existing requests when a new version renames
   or deletes an input, or adds a required one?

The owner decided on 27 October 2026 that fleet migrations, server-driven
upgrade waves and batch upgrade tooling are later work, outside Milestone 6
(issue 2037). This RFC stays inside that line. It proposes no wave, no cap
on open changes, no stored target query and no done-check.

## Problem

A platform team publishes version 4 of the blueprint `postgres`. Forty
Catalog requests pin version 2 or 3. ADR 112 decision 2 says that an
upgrade is a new revision with a higher version, and that the requester
submits it. So today:

- Each consumer team must notice the new version and submit one upgrade
  for each request. The platform team cannot see all the plans of an
  upgrade before any of them applies, which RFC 14 ("Versions and
  rollouts") promised.
- When version 4 renames the input `size` to `instanceSize`, each
  consumer must find the new name and give the answer again. A mistake
  fails at submit, so nothing breaks, but forty people do the same work.

## What is decided already

- ADR 110 decision 2: one request, one unit. A change to many units is many
  requests.
- ADR 110 decision 7: the Catalog starts jobs through `job.Service` only.
  The idempotency key is `<request ID>/<revision>`.
- ADR 111 decision 4: a document change that a server cannot ignore needs a
  new schema version. Until 1.0, a version that no tag shipped can change
  (ADR 73). No tag has shipped (ADR 279).
- ADR 112 decision 2: a published version never changes. A retire stops new
  requests and upgrades to that version. A request that pins it keeps it.
- ADR 113 decision 8: the checks run at `submit`, `approval` and `apply`.
- ADR 116 decision 3: one revision in flight for each request.
- ADR 117 decision 2: an upgrade is an update and needs `unit:edit-inputs`
  on the unit.
- ADR 190: a change order holds the plans of 1 to 100 units of one
  namespace, one review, and the approvals of its jobs. Version 1 runs
  runner jobs only, with a code location at a commit (ADR 191 decision 2).
- ADR 225: approval rules, approval defaults, ownership rules, and the
  approver kind `unitOwners`.
- ADR 228: Expr computes values only.
- RFC 56 (proposal of 2026-10-30) gives a request in the home `git` an
  upgrade review request.

## Facts from other products

Each fact has a tag: **[fetched]** means that the page was read on
2026-10-30, **[not fetched]** means a search snippet only.

- **HCP Terraform no-code provisioning.** A new no-code version shows a
  notice on each workspace. "HCP Terraform does not automatically update
  workspaces." A workspace admin upgrades one workspace at a time. The CLI
  prompts for values of new input variables. No rename or delete mapping is
  documented. A plan without an error auto-applies.
  <https://developer.hashicorp.com/terraform/cloud-docs/workspaces/no-code-provisioning/provisioning>
  [fetched],
  <https://developer.hashicorp.com/validated-designs/terraform-operating-guides-scaling/nocode-provisioning>
  [fetched]
- **Spacelift Templates.** A published version is read-only. A platform team
  updates one deployment at a time. An update to a version is allowed only
  "if it has exactly the same stack keys and have no new required inputs".
  The version view lists the deployments of each version.
  <https://docs.spacelift.io/concepts/template/workbench> [fetched]
- **Spacelift blueprints (older).** "No ongoing relationship between the
  blueprint and the created stacks."
  <https://docs.spacelift.io/concepts/blueprint> [fetched]
- **env zero.** "Changes to a template only affect new deployments. To apply
  template changes to an active environment, you must redeploy it." No bulk
  redeploy and no migration are documented.
  <https://docs.envzero.com/guides/admin-guide/templates> [fetched]
- **Backstage Software Templates.** A template run is a task that creates
  code. Nothing updates an existing component.
  <https://backstage.io/docs/features/software-templates/> [fetched]
- **Port.** Blueprints and actions have no versions. A data migration maps
  fields with JQ over all entities of a blueprint, with a preview, and
  reports a count of failed entities. There is no revert.
  <https://docs.port.io/build-your-software-catalog/customize-integrations/configure-data-model/migrate-data/>
  [fetched]
- **Crossplane.** Each change of a Composition makes a numbered
  `CompositionRevision`. `compositionUpdatePolicy: Automatic` (the default)
  moves each composite resource to the latest revision. `Manual` needs an
  edit of `compositionRevisionRef` for each one. Labels give channels. There
  is no batch with a plan. A breaking schema change needs conversion
  webhooks.
  <https://docs.crossplane.io/latest/composition/composition-revisions/>
  [fetched],
  <https://docs.crossplane.io/latest/composition/composite-resource-definitions/>
  [fetched]
- **Kubernetes CRD versions.** "If the storage version changes, existing
  objects are never converted automatically." A storage version migration
  writes each object again.
  <https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definition-versioning/>
  [fetched]

No product found has a batch upgrade that shows every plan before any
apply. Two products map stored values: Port (JQ, with a preview) and
Kubernetes (conversion webhooks).

## Question 1. A batch for a rollout

### Options

1. **No batch.** Each upgrade stays one submit by a holder of
   `unit:edit-inputs`, as in version 1 of ADR 112. This matches HCP
   Terraform and Spacelift Templates. Cost: none. It does not give the
   "plans to look at before any apply" of RFC 14.
2. **A rollout is a change order of upgrade revisions (recommended).** The
   platform team starts a rollout of version N for a set of requests. For
   each request in the home `opendustpan`, the server makes one upgrade
   revision, and one change order holds the plan job of each of these
   revisions. Reviewers read all plans in one review, with the change
   groups of ADR 194. A person asks for the apply (ADR 190 decision 10).
   Each request in the home `git` receives an upgrade review request instead
   (RFC 56), because a change order runs runner jobs only (ADR 190
   decision 5). Cost: an amendment of ADRs 190, 191 and 116 (below).
3. **A rollout object with waves.** Waves, a cap on open changes, a stop on
   failures and a stored target set. This is the fleet migration of issue
   2037, which the owner put after Milestone 6. Not proposed.

### Proposal for question 1: option 2

**Owner decision D7**, because it amends accepted ADRs. The
recommendation is option 2.

1. **The rollout call.** `POST /api/dustpan/v1/namespaces/{namespace}/blueprints/{blueprint}/rollouts`
   takes `{"version": N, "requests": ["<name>", ...], "reason": "..."}`.
   An empty `requests` list means each request of the blueprint that pins a
   version lower than N. The call needs the new action `blueprint:roll-out`
   on the blueprint. It is not in a built-in role except the administrator
   of the namespace (ADR 117 decision 3).
2. **What the call does.** For each named request, in one call:
   - It skips a request with a revision in flight, a request that pins N or
     higher, and a request with `ensure: absent`. Each skip has a reason in
     the answer. A rollout never cancels a revision in flight.
   - It builds the upgrade document: the last applied document, with
     `blueprint.version` set to N and the answers migrated (question 2).
     A request with no upgrade path is skipped with the reason
     `no-upgrade-path`.
   - It runs the checks of `submit` on each document (ADR 113 decision 8).
     A document that fails is skipped with each failure.
   - For the requests in the home `git`, it starts one upgrade review
     request each (RFC 56).
   - For the requests in the home `opendustpan`, it makes one change order
     with 1 to 100 units (ADR 190 decision 3). More than 100 requests give
     more than one change order, each with at most 100 units, in the order
     of the request names. The answer lists each change order.
3. **The revisions.** Each upgrade revision has the origin `rollout`, the
   rollout ID (prefix `rol-`) and the principal that started the rollout.
   It is a normal revision of its request. One revision in flight still
   holds: a submit of the consumer for that request fails with
   `catalog.ErrInFlight` until the change order ends or the consumer
   cancels the revision.
4. **Consent of the consumer.** The platform team does not hold
   `unit:edit-inputs` on the units of other teams, and must not need it. So
   each rollout change order has a requirement from the built-in approval
   rule `blueprint-rollout` at the installation scope: one approval, by
   `unitOwners` of each unit, with `authorMayApprove: false`. The rule
   holds for each unit of the change order, so the owners of each unit
   approve the change of their unit. A unit with no owner in its context
   cannot be in a rollout. The call skips it with the reason `no-owner`.
   This fails closed: no owner never means no approval. The rule is a
   built-in rule with the reserved ID `blueprint-rollout`. An installation
   administrator can raise its count, with a fresh authentication and an
   audit entry. Nobody can delete it.
5. **The Catalog decision joins the change order.** Each upgrade revision
   has its own gates at `change.propose`, from the blueprint and the
   namespace. As ADR 190 decision 6 does for `job.start`, each approval
   requirement of a revision joins the requirements of the change order. A
   `deny` skips that request at the rollout call.
6. **Rechecks.** The Catalog recheck at `approval` and at `apply` (ADR 113
   decision 8) runs for each revision, through the precondition port. A
   failed recheck rejects the apply job of that unit only. The change order
   then ends `partially-applied` (ADR 190 decision 7), and the revision
   ends `rejected` with each failure.
7. **States.** A revision of a rollout takes its state from its jobs: the
   plan job gives `queued` and `running`, the change order in review gives
   `awaiting-approval`, and the apply job gives the rest, through
   `catalog.StateOf`. A change order that ends `closed` or `expired` before
   the apply ends each revision `canceled` or `expired`.

**Amendments that option 2 makes.**

- ADR 190 decision 5 and ADR 191 decisions 2 and 3: a unit plan can name a
  Catalog revision in place of a code location at a commit. Its plan job
  and its apply job run the rendered snapshot of that revision (ADR 110,
  change 3 of the job model). The attach of such a unit plan takes no base
  commit. The cause diff (ADR 194 decision 2) of such a unit is the diff of
  the two request documents: the version and each answer that changed.
- ADR 116 decision 4: the map of decision 7 above for a revision whose jobs
  run under a change order.
- ADR 110, change 1 of the job model: a job can carry both a Catalog origin
  and a change order origin. The job model already refuses either origin
  from the REST API (ADR 190 decision 15).
- ADR 117 decision 2: the action `blueprint:roll-out`.
- ADR 225 decision 1: the reserved rule ID `blueprint-rollout`.

### Ports for question 1

- `catalog.Rollouts`, implemented in the internal wiring with
  `changeorder.Service`: `Start(ctx, actor, catalog.RolloutPlan)` attaches
  the revisions as unit plans. No import between `internal/catalog` and
  `pkg/changeorder` (ADR 190 decision 1).
- `catalog.RolloutStore`, the record of a rollout: its ID, the blueprint,
  the version, the principal, the reason, each request with its result
  (`change-order`, `review-request` or `skipped` with a reason). Each write
  checks the fence and appends the audit entry in its transaction. It gets
  the suite `catalogtest.RunRolloutStore` before any adapter.
- `catalog.PlanRollout(requests, version, chain)`, a pure function that
  gives each request its document or its skip reason. A table-driven test
  covers each skip reason.

## Question 2. The migration of answers

### Options

1. **The requester gives valid answers.** Version 1 of ADR 112. HCP
   Terraform prompts for new variables, and Spacelift Templates refuses an
   upgrade that adds a required input. Cost: none. Each consumer repeats
   the same edit.
2. **A declared migration in the blueprint version (recommended).** The
   platform team writes, in version N, how answers of one earlier version
   become answers of N. Each value is an Expr expression over the old
   answers. The server builds the upgrade document, and the normal checks
   run on it. Precedent: the JQ mapping of Port, with a preview.
3. **A conversion webhook.** The server sends the old answers to a service
   of the platform team, as Kubernetes does. Cost: a network call from the
   server to an address that a principal gives (the outbound guard), an
   untrusted answer, and a new timeout to handle. Expr can already compute
   a value from other values (ADR 228), so a service adds risk and no
   power that a rename, a default or a split needs.
4. **The server rewrites stored answers in place.** As a storage version
   migration. Rejected: a revision never changes (ADR 110 decision 4), and
   a request file in git changes only with a commit (ADR 110 decision 5).

### Proposal for question 2: option 2

1. **The field `upgrade`.** A blueprint document gets the optional field
   `upgrade`:

   ```json
   "upgrade": {
     "from": 3,
     "inputs": {
       "instanceSize": "answers.size == 'small' ? 'db.t4g.small' : 'db.m7g.large'",
       "tier": "'standard'"
     },
     "drop": ["legacyBackups"]
   }
   ```

   - `from` names one earlier published version of the same blueprint.
   - `inputs` maps an input of this version to an Expr expression. The
     expression sees `answers` (the answers of the `from` document, with its
     defaults) and `request` (ADR 112 decision 6). It gives a value of the
     type of the input. ADR 228 holds: it computes a value only.
   - `drop` lists each input of the `from` version that this version does
     not have.
   - An input of this version with the same name and type as an input of
     the `from` version, and with no entry in `inputs`, keeps its answer.

   Schema version 1 of the blueprint document takes the field, because no
   tag shipped it (ADR 111 decision 4, ADR 73).
2. **Publish checks (no hidden rule).** A publish with `upgrade` refuses:
   - a `from` that is not a published version lower than this one,
   - an input of the `from` version that this version does not have and that
     `drop` does not list, so a deleted input is always named,
   - an input in `drop` that this version has,
   - a required input of this version with no default, no entry in
     `inputs` and no input of the same name and type in `from`,
   - an expression that does not compile against the input types of
     `from`, or that gives another type than its input.

   Each refusal is `catalog.ErrInvalid` with the input name. The limits of
   ADR 112 decision 12 hold for each expression. A blueprint version holds
   at most 64 entries in `inputs` and 64 in `drop`.
3. **The upgrade path.** The server follows `from` links back from N. The
   path from version v to N exists when the links reach v. Each step runs in
   order: the output of one step is the `answers` of the next. A path holds
   at most 32 steps. A request at a version that the links do not reach
   has no upgrade path, and its upgrade needs the answers of a person, as
   in version 1.
4. **The result is a proposal, never a silent change.** The migrated
   document is the document of a new revision. The normal checks of
   `submit` run on it. A person sees the diff of the answers before any
   apply: in the change order review (question 1), in the diff of the
   upgrade review request (RFC 56), or in the answer of the check route.
   The audit entry of the revision lists the names of the inputs that the
   migration set or dropped (ADR 117 decision 6), never the values.
5. **A consumer can use it too.** The check route (ADR 117 decision 5)
   takes `{"upgradeTo": N}` with a request name, and answers the migrated
   document and its failures. It stores nothing. So a consumer gets the same
   document that a rollout would give, and can submit it.
6. **An expression that fails to run** gives a failure of that input with
   `catalog.ErrRuleFailed`, never a guessed value. The rollout skips the
   request with the failure.

### Ports for question 2

- `catalog.Migrate(path []Version, doc Document) (Document, []Failure, error)`,
  a pure function on top of the Expr evaluator (work item M6-1). A
  table-driven test covers each publish refusal and each step rule.
- `catalog.UpgradePath(store, ref, from, to)` reads the `from` links
  through `BlueprintStore.Version`. It needs no new store method.

## End-to-end steps (real CLIs)

The e2e suite runs these with the real OpenTofu CLI and the real Pulumi
CLI, on SQLite and on PostgreSQL:

1. Publish version 1 of an OpenTofu blueprint and of a Pulumi blueprint,
   each with the input `size`. Submit three requests of each in the home
   `opendustpan` and apply them on a runner.
2. Publish version 2 of each, which renames `size` to `instanceSize`, drops
   `legacy`, and adds a required input `tier` with a mapping.
3. Call the check route with `upgradeTo: 2` for one request, and check the
   migrated answers.
4. Start a rollout of version 2 as a platform principal with
   `blueprint:roll-out` and no `unit:edit-inputs`. Check one change order
   with three units of each tool, and a plan job for each that ran
   `tofu plan` or `pulumi preview` on the rendered snapshot.
5. Try to approve as the principal that started the rollout, and see the
   refusal. Approve as an owner of each unit. Ask for the apply. Check that
   each state version holds the new variable or config key, and that each
   request pins version 2.
6. Start a second rollout while a consumer has a revision in flight, and
   check the skip reason `in-flight`.

## Security tests (ADR 250)

The rollout call and the migration take input from a principal and from
stored documents, so each step has its own test with the other steps
skipped:

1. **Boundary.** The rollout body: an unknown field, a version that is not
   published, more than 10,000 request names, a duplicate name. The
   `upgrade` field: each publish refusal of decision 2.
2. **Contain.** A migrated answer is data only: the render writes it with a
   JSON encoder (ADR 110 decision 8). A test migrates an answer that holds
   HCL and Pulumi YAML syntax and checks the rendered program text.
3. **Limit.** A path of 33 steps, an expression over the limits of ADR 112
   decision 12, and a rollout of 101 requests that must split into two
   change orders.
4. **Authorize at use.** A rollout by a principal with
   `blueprint:publish` and no `blueprint:roll-out`. An approval by an owner
   of one unit counts for that unit only. A unit with no owner is skipped.
5. **Fail closed.** An expression that fails to run, an Expr version that
   does not compile (ADR 112 decision 11), and a store error each skip the
   request and write an audit entry. None of them starts a job.

## Work items

Each item fits one session. Size: S is less than 500 changed lines, M is
500 to 1,500, L is more. Items in one conflict group touch the same files,
so they run one after the other. M6-1 is the Expr evaluator, M6-2 the SQL
`BlueprintStore`, and "Catalog routes" is the item that adds the REST
routes of ADR 117.

| ID | Item | Size | Conflict group | Depends on |
| --- | --- | --- | --- | --- |
| B1 | The field `upgrade`: parse, publish checks, `catalog.Migrate`, `catalog.UpgradePath`, table tests | M | `catalog-core` | M6-1 |
| B2 | The check route takes `upgradeTo` | S | `api-catalog` | B1, Catalog routes |
| B3 | A change order unit plan that names a Catalog revision. The attach, and the plan job and the apply job on the rendered snapshot. The cause diff of the documents | M | `changeorder` | the ADR of owner decision D7 |
| B4 | `catalog.RolloutStore`, its suite, and the SQLite and PostgreSQL adapters with one migration each | M | `catalog-store` | M6-2 |
| B5 | The rollout service, `catalog.PlanRollout`, the route, the action `blueprint:roll-out`, the built-in rule `blueprint-rollout` | M | `catalog-core`, `identity-actions` | B1, B3, B4, U2 of RFC 56 |
| B6 | The e2e steps above, and the user documentation page "Roll out a blueprint version" | M | `e2e`, `docs` | B5 |

Order: B1, then B2 and B4 (parallel, after M6-2), then B3 (after the ADR),
then B5, then B6.

## Proposed glossary lines

**Rollout**:
One call of the platform team that offers a newer blueprint version to many
Catalog requests: one change order for the requests in OpenDustpan, and one
upgrade review request for each request in git. It never applies without
the approval of the owners of each unit.
_Avoid_: Campaign, fleet migration (that is later work), wave, batch (alone)

**Upgrade path**:
The chain of `from` links of the blueprint versions between the version that
a request pins and a newer version. Each step migrates the answers of one
version to the next.
_Avoid_: Migration plan, conversion

## Open questions

- Question 1 is owner decision D7 (it amends ADRs 190, 191 and 116).
- Does a rollout also accept a retired target version? The proposal says no,
  as ADR 112 decision 2 says for an upgrade.

## Outcome

None yet.
