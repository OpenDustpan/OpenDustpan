---
rfc: 55
title: Environment promotion and comparison
status: discussing
created: 2026-10-10
---

# RFC 55. Environment promotion and comparison

Status: discussing. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". The section "Proposal (2026-10-30)" is ready to
accept, with two owner decisions: D6 (the words) and D10 (a preview rule
applies).

## Problem

Massdriver compares two environments component by component and forks
environments; preview environments per pull request are common.

## First ideas

- Compare the config of two units made from the same blueprint (RFC 14) or
  program.
- Promote config from one unit to another as an edit (RFC 12).

## What is decided already

- `GLOSSARY.md`: **Promotion** names the operator action that makes a
  replica installation the primary installation of a namespace.
  **Environment** is the word of the Pulumi CLI for a profile, and is on the
  _Avoid_ list of **Unit**, **Stack** and **Workspace**. Copy says "stack" and
  "workspace", never "unit" (ADR 251). So this feature needs other words.
- ADR 51: a collection is a named set of units that a team manages. Each
  unit is in exactly one collection. So a collection cannot also be the
  "dev" or the "prod" of a team.
- ADR 110 to 117: a Catalog request pins a blueprint version and holds the
  answers. An update needs `unit:edit-inputs` on the unit and passes every
  check. A request in git changes through a write-back job.
- ADR 41: a read of state masks each secret value. ADR 273: a read of the
  outputs of a unit needs `unit:read-outputs`.
- ADR 224 and ADR 226: gates at fixed decision points over typed facts. The
  group `source` already holds `upstreamUnit`.
- ADR 267 decision 9: a trigger starts a plan and nothing else. A `follows`
  trigger starts a plan of a unit after another unit applies.
- RFC 111 (proposal of 2026-10-30): the upgrade path, which migrates answers
  from one blueprint version to a newer one.
- Owner, 27 October 2026 (issue 2037): server-driven upgrade waves are later
  work.

## Facts from other products

Each fact has a tag: **[fetched]** means that the page was read on
2026-10-30, **[not fetched]** means a search snippet only.

- **Kargo.** It promotes Freight: a set of references to versioned
  artifacts (images, Git commits, Helm charts). A Stage is a promotion
  target. Each Stage names where it takes Freight from: a Warehouse, or
  upstream Stages, and from an upstream Stage only after Freight is
  "verified" there. So the order is explicit, in each Stage. Auto-promotion
  is a setting that only project admins turn on. A manual approval lets
  Freight skip the verification of a Stage.
  <https://docs.kargo.io/user-guide/how-to-guides/working-with-stages>
  [fetched], <https://docs.kargo.io/user-guide/how-to-guides/working-with-freight>
  [fetched]
- **Gruntwork Patcher.** A change moves as a chain of pull requests, from dev
  to stage to prod. A merge starts the pull request of the next environment.
  The order is in the workflows, not in a stored object.
  <https://docs.gruntwork.io/2.0/docs/patcher/concepts/promotion-workflows>
  [fetched]
- **Massdriver.** `compareEnvironments` pairs the instances of two
  environments by component, and shows the version and a diff of the params
  of each. Both environments must be in one project. A fork makes a new
  environment with the params of its parent, and copies secrets only on
  request.
  <https://docs.massdriver.cloud/api/graphql/operations/queries/compare-environments>
  [fetched],
  <https://pkg.go.dev/github.com/massdriver-cloud/massdriver-sdk-go/massdriver/platform/environments>
  [fetched]. `mass environment preview` forks a base environment for a
  preview and deploys it.
  <https://docs.massdriver.cloud/cli/commands/mass_environment_preview>
  [not fetched]
- **Spacelift.** Stack dependencies are a stored graph with no loops. Outputs
  move from one stack to the next. A failure cancels the runs after it.
  <https://docs.spacelift.io/concepts/stack/stack-dependencies> [fetched]
- **Port.** A "promote to production" self-service action runs a workflow,
  with an optional approval.
  <https://docs.port.io/guides/all/promote-to-production> [not fetched]
- **HCP Terraform and env zero.** No promotion of values between
  workspaces or environments found (HCP Terraform [not fetched], env zero
  [not fetched]).

So two shapes exist: an artifact or a commit that moves along an explicit
order (Kargo, Patcher), and a copy of params with a diff (Massdriver).

## Proposal (2026-10-30)

### Owner decision D6. The words

Recommended:

- **Lane**: an ordered list of 2 to 10 units of one namespace, through which
  a change moves, for example `dev`, `staging`, `prod`.
- **Advance**: the change that copies what one unit of a lane runs to the
  next unit of the lane.
- **Comparison**: the side-by-side read of two units.

Other options: "stage" (it is on the _Avoid_ list of **Phase**), "pipeline"
(on the _Avoid_ list of **Follows trigger**), "track", "path" (a code path).
Copy can still say "promote to production" in a sentence. The words name
the objects of the API.

### Decisions

1. **Comparison of any two units.** `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/comparison?with=<unit ID>`
   reads two units that the caller can read, in one namespace or, with
   `namespaces/-`, in two (ADR 210 decision 4). The server checks
   `unit:read` on each unit. It answers, for each side:
   - The tool and the name.
   - For a unit of a Catalog request: the blueprint, the version, and each
     answer, marked `same`, `different`, `left-only` or `right-only`.
   - The runner settings: the pool, the code location and the last commit
     that a job applied.
   - Each attached profile with its revision.
   - The output names. The output values only when the caller holds
     `unit:read-outputs` on both units, with each secret value masked (ADR
     41).
   - The provider and plugin versions of the last job, when a job record
     has them (RFC 93, layer 4).

   A comparison is a read. It stores nothing and writes no audit entry,
   as other reads do. It never shows a secret value, and a part that the
   caller cannot read is marked `hidden`, never left out with no mark.
2. **A lane.** A lane (`lane.Lane`, ID prefix `lan-`) has a name, a
   namespace, an ordered list of members, and a kind:
   - `catalog`: each member is the unit of a Catalog request of one
     blueprint.
   - `code`: each member is a unit with runner settings, of one tool, whose
     code locations name the same repository.

   A unit is in at most one lane. A lane has 2 to 10 members. The action
   `namespace:manage-lanes` creates, changes and deletes a lane, with an
   audit entry. A namespace has at most 200 lanes. Each member has an
   optional `soak`: the time that the member before it must have run its
   last applied change before an advance takes it (default 0, shown in the
   API). This is the verification of Kargo in its smallest form.
3. **What an advance copies.**
   - In a `catalog` lane: the blueprint version of the member before, and
     the answers of each input that the blueprint marks `"advance": true`.
     Other answers stay. A new input field `advance` in the blueprint
     document says this, so the platform team decides which inputs move
     (an image tag, a feature switch) and which stay per unit (a size, a
     name). Schema version 1 takes the field, because no tag shipped it
     (ADR 73). When the version changes, the answers that stay go through
     the upgrade path of RFC 111.
   - In a `code` lane: the commit that the member before last applied. The
     advance starts a runner job with the operation `apply` on the target,
     at that commit, in the code location of the target.
4. **An advance is a normal change.** `POST .../lanes/{lane}/advances`
   takes `{"to": "<unit ID>", "reason": "..."}`. The source is always the
   member before `to`, and its last applied revision or commit. There is no
   way to advance from another unit.
   - In a `catalog` lane, it submits a revision of the request of the
     target, with the origin `advance`, and needs `unit:edit-inputs` on the
     target and `catalog:read` on the source. A request in git receives a
     write-back job with the delivery of the request.
   - In a `code` lane, it starts the job through `job.Service.Start`, and
     needs `unit:run-apply` on the target and `unit:read` on the source.
   - Every gate of the target applies. The submit checks, the rechecks and
     the one revision in flight of the Catalog apply.
   - `POST .../lanes/{lane}/advance-checks` with the same body answers the
     document or the commit that an advance would give, and its failures,
     and stores nothing.
5. **A fact for gates.** The fact schemas of `change.propose`,
   `change.planned` and `change.apply` add the group `advance` (a minor
   version, ADR 226 decision 2): `available`, `lane`, `fromUnit`,
   `fromRevision`, `fromCommit` and `fromAppliedSeconds`. A change that is
   not an advance has `available == false`. So a team writes the real
   promotion control as a gate on the production unit, for example: deny a
   change unless `advance.available && advance.fromAppliedSeconds >= 86400`.
   The user documentation gives this gate as an example.
6. **No automatic advance.** The server never advances on its own. A
   `follows` trigger (ADR 267) can start a plan of the next unit after the
   member before applies, and a person then asks for the advance. Kargo
   lets an admin turn on auto-promotion. That is a server-driven change, and
   the owner put such changes after Milestone 6 (issue 2037).
7. **Answer to the open question.** The feature does not depend on RFC 4's
   grouping of units. A comparison takes any two units. A lane is its own
   small object, because a collection belongs to a team and holds the
   units of all its stages (ADR 51).
8. **A fork.** `POST .../namespaces/{namespace}/catalog-requests/{blueprint}/{name}/forks`
   takes `{"name": "...", "inputs": {...}, "destroyTime": {...}, "reason":
   "..."}`. It submits a create of a new Catalog request of the same
   blueprint and version, with the answers of the source request and the
   answers of the body over them. It is a normal create: it needs
   `catalog:request` on the blueprint and `catalog:read` on the source, and
   every check of `submit` runs. The new request records `forkOf`, and the
   comparison shows it. A fork copies no secret, because no answer is
   secret (ADR 111 decision 3), and the profile comes from the blueprint.
   Massdriver forks an environment the same way, and copies secrets only on
   request [fetched, link above]. A unit with no Catalog request cannot be
   forked, because its config lives in the code of the team. The answer
   names that reason.
9. **Preview rules.** A **preview rule** (ID prefix `pvr-`) of a namespace
   makes one fork for each open review request of one repository, and
   destroys it when the review request closes. It holds:
   - the source request to fork, and the forge connection and repository
     whose review requests it follows,
   - `inputs`: a map from an input of the blueprint to one variable of the
     review request: `reviewRequest.number`, `reviewRequest.branch` or
     `reviewRequest.commit`. The render writes each value as data (ADR 110
     decision 8),
   - a destroy time for each fork (RFC 43), 7 days of `inactiveFor` by
     default, as a safety net,
   - at most 20 open forks. A review request over the limit receives no fork,
     and the server records the reason and sends `preview.skipped`.

   The events: a review request that opens or gets a push submits a create
   or an update of the fork `<source name>-rr<number>`, in the home
   `opendustpan`. A review request that closes or merges submits
   `ensure: absent`. The server reads the events from the forge webhook of
   ADR 267 decision 19. Each submit runs as the last saver of the rule, as
   a trigger does (ADR 267 decision 14): the server checks the current
   grants of the saver (`catalog:request` on the blueprint,
   `unit:edit-inputs` and `unit:request-destroy` on the fork) at each
   event, and fails closed. Every gate of the fork applies, with
   `source.kind == "forge"`. The action `namespace:manage-preview-rules`
   saves a rule, with an audit entry. A namespace has at most 50 rules.

   **No preview for a review request from a fork.** A review request whose
   head is in another repository than the repository of the rule makes no
   preview, at no event. Anyone can open such a review request on a public
   repository, and a preview applies with the grants of the saver and the
   profile of the blueprint. ADR 267 decision 19 already says "A push from
   a fork starts nothing" for its plan. The preview service checks this
   again on its own, as a layer that does not rely on the webhook filter of
   ADR 267: before each submit, it reads the review request from the forge
   through the forge port, and compares the head repository (its forge ID,
   not its name) with the repository of the rule. A head that differs, a
   head that the forge does not give, and a read that fails each make no
   submit. The server records the reason `fork-head` or `forge-unavailable`
   and writes an audit entry. The comparison is a pure function,
   `preview.SameRepository`, with its own test.

   Whether a preview rule may apply with no person is owner decision D10.

### Owner decision D10. A preview rule applies

ADR 267 decision 9 says that a trigger starts a plan and nothing else. A
preview rule that only plans gives no preview. A preview rule is not a
trigger of ADR 267: it does not belong to one unit (ADR 267 decision 1),
and it makes and destroys units. But it starts a change that no person
starts at that time, from the same webhook. So option 1 **amends ADR 267
decision 9**: "A trigger starts a plan and nothing else. A preview rule of
RFC 55 is the one kind of automatic start that can apply, and only to the
forks that it made." Options:

1. **The submit of a preview rule applies (recommended).** The fork is a new
   unit that only the rule writes. Each gate still applies, so a team that
   wants a person to approve each preview saves a gate. The audit entry of
   each submit names the rule and its saver. Cost: a second kind of change
   that no person starts at the time, next to the destroy of RFC 43.
2. **The rule only plans.** A person applies each fork. Cost: no automatic
   preview, which is the reason for the feature.

### Ports

- `pkg/lane`: the model, the errors, the port `lane.Store` with the suite
  `lanetest`. Each write checks the fence and appends its audit entry. A
  lane is namespace data, so a backup and a namespace backup hold it.
- `lane.Advance(lane, source, target)`, a pure function that gives the new
  document or the commit and each failure, with a table-driven test.
- `internal/comparison`: the read of two units over the inventory, the
  Catalog store, the runner settings, the profiles and the masked output
  read. It needs no new port.
- The producer of the fact group `advance` in `pkg/gate` (ADR 226 decision
  12), with its conformance cases.

### End-to-end steps (real CLIs)

1. Publish an OpenTofu blueprint and a Pulumi blueprint with the inputs
   `imageTag` (`"advance": true`) and `size`. Submit `dev` and `prod` requests
   of each in the home `opendustpan`, with different sizes, and apply.
2. Make a `catalog` lane for each blueprint: `dev`, then `prod`.
3. Submit a new `imageTag` to `dev`, and apply. Read the comparison of `dev`
   and `prod`: `imageTag` is `different`, `size` is `different`, the version
   is `same`.
4. Save a gate on `prod` that denies a change unless `advance.available`.
   Submit an edit of `prod` by hand and see the denial.
5. Advance to `prod`. Check that `tofu apply` and `pulumi up` ran, that
   `imageTag` is the value of `dev`, and that `size` kept the value of
   `prod`.
6. Make a `code` lane of two OpenTofu units on one repository. Apply a new
   commit on the first, advance, and check that the second applied that
   commit.
7. Fork the `dev` request of each tool with one changed answer, and check
   the new unit, its answers and its `forkOf`.
8. Save a preview rule on the repository of the fake GitHub host. Open a
   review request: a fork `dev-rr1` applies with `imageTag` set to the
   commit. Push a commit: the fork updates. Close it: the fork destroys,
   with `tofu destroy` and `pulumi destroy` in the runner jobs.

### Security tests (ADR 250)

1. **Boundary.** A lane with 1 or 11 members, a member twice, a unit of
   another namespace, units of two blueprints in a `catalog` lane, two
   repositories in a `code` lane, and an advance `to` the first member.
2. **Contain.** An advanced answer is data only in the render (ADR 110
   decision 8). The commit of a `code` advance is a full commit ID from the
   job record, never from the request body.
3. **Limit.** 201 lanes in a namespace, and a comparison of two units with
   10,000 outputs each, which the server cuts with a mark.
4. **Authorize at use.** An advance with `unit:edit-inputs` on the target and
   no read on the source. A comparison where the caller can read one unit
   only: the answer is a 404, never a partial answer. The 404 is for a
   whole unit that the caller cannot read. The mark `hidden` of decision 1
   is for a part of a unit that the caller can read, for example the
   output values: with `unit:read-outputs` on one side only, both sides
   are `hidden`.
   A preview rule whose saver lost `catalog:request` makes no fork. A
   review request whose head repository has another forge ID than the
   repository of the rule makes no fork: a test calls the preview service
   with such an event directly, with the webhook filter of ADR 267
   skipped. A test also gives `preview.SameRepository` a head with the same
   name and another ID, and a head with no ID. A fork
   that names a source request that the caller cannot read is a 404.
5. **Fail closed.** A source with no applied revision, or under its `soak`,
   refuses the advance with the reason. A fact producer that fails gives
   `advance.available == false`, and a gate that reads it denies.

### Work items

Sizes and conflict groups as in RFC 111.

| ID | Item | Size | Conflict group | Depends on |
| --- | --- | --- | --- | --- |
| P1 | `internal/comparison` and its route | M | `api-units` | M6-2, Catalog routes |
| P2 | `pkg/lane`: model, `lane.Store`, `lanetest`, `lane.Advance` | M | `lane` | none |
| P3 | SQLite and PostgreSQL adapters of `lane.Store`, one migration each, the backup table list | M | `store-migrations` | P2 |
| P4 | The input field `advance` of a blueprint, and the advance of a `catalog` lane | M | `catalog-core` | P3, B1 |
| P5 | The advance of a `code` lane, the routes, the action `namespace:manage-lanes`, and the fact group `advance` | M | `gate-facts`, `identity-actions` | P3, the job model start of a runner apply |
| P6 | The e2e steps above, and the docs page "Compare, fork and advance stacks and workspaces" | M | `e2e`, `docs` | P1, P4, P5, P7, P8 |
| P7 | The fork route and `forkOf` | S | `catalog-core`, `store-migrations` | M6-2, Catalog routes |
| P8 | Preview rules: the store, its suite and adapters, the events of the forge webhook, the saver check, the limits, `preview.skipped`, the action (after D10) | L | `preview`, `store-migrations`, `identity-actions` | P7, E3 of RFC 43, the review-request webhook of ADR 267 |

Order: P1, P2 and P7 in parallel, then P3, then P4, P5 and P8, then P6.

### Proposed glossary lines (if the owner takes the words of D6)

**Lane**:
An ordered list of 2 to 10 units of one namespace, through which a change
moves, for example from a test stack to a production stack (RFC 55). A unit
is in at most one lane.
_Avoid_: Environment (that is a profile of the Pulumi CLI), pipeline, stage,
promotion path (a promotion is of an installation)

**Preview rule**:
A rule of a namespace that makes a fork of one Catalog request for each
open review request of one repository, and destroys the fork when the
review request closes.
_Avoid_: Preview environment, ephemeral environment, PR environment

**Advance**:
The change that copies the blueprint version and the marked answers, or the
applied commit, of one unit of a lane to the next unit. It is a normal
change of the next unit, with all its gates.
_Avoid_: Promote (that is of an installation), deploy, sync

## Open questions

- Does this depend on RFC 4's grouping of units into environments? Answered
  by decision 7: no.

## Outcome

None yet.
