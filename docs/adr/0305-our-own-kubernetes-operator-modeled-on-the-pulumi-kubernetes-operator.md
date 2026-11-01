# 305. Our own Kubernetes operator, modeled on the Pulumi Kubernetes Operator

Status: accepted · 2026-11-01. Decided by the project owner on 2026-11-01 and on 2026-10-18. Decides the role of the operator in RFC 31 phases 3 and 4. The resource names and the other open points stay in RFC 31

Changes:

- Extends ADR 136: the Kubernetes runner of ADR 136 runs the jobs that the
  operator requests, and the operator ships in the chart and the binary of
  the coordinator.
- Extends ADR 110: a resource of the operator that applies a blueprint is a
  Catalog request.
- Extends ADR 111: that resource carries the request document of the Catalog.
- Extends ADR 12: the operator is a tool-neutral client with one tool model
  below it for each tool.
- Extends ADR 17: the operator uses only the REST API.

## Context

[RFC 31](../rfc/0031-kubernetes.md) asks how people declare infrastructure
as resources in a Kubernetes cluster. Its problem section lists three
choices that teams have today, and each choice has a gap:

- The Pulumi Kubernetes Operator has no approval step, no history of its own,
  and no UI. It works with the Pulumi CLI only. Its version 2.10.0 installs
  for a whole cluster only.
- tofu-controller and Burrito approve by editing YAML or by a merged pull
  request, and they have a thin UI or none.
- Crossplane asks teams to rewrite their infrastructure as code as
  compositions, and it has no plan-and-approve step before a change.

ADR 136 accepts RFC 31 phase 2. Phases 3 and 4, the operator, stayed
open. RFC 31 offered option 2 for them: be the backend and the runner for
other operators, and build no operator. It also named Crossplane as the
model of phase 4.

On 1 November 2026 the project owner decided this, in these words:

- "Crossplane is shit. I would migrate to our own k8s operator."
- "our own operator for applying IaC tool agnostic would be better. Modelled
  around this thing ofc." Here "this thing" is the Pulumi Kubernetes
  Operator.
- "It's primary use case would be for applying blueprints but I don't see why
  individual units couldn't work."

The project owner decided parts of the operator on 18 October 2026. These
decisions never reached an ADR before this one. The answers to RFC 31
questions 31.1, 31.6 and 31.8 is this text of the decisions record:

```text
Operator: after the Catalog ships, sharing the coordinator's chart and
binary (ADR 136). CRDs are kstatus-compliant from v1 (status.observedGeneration;
Reconciling, Stalled, Ready conditions), so Flux health checks work as is; a
generic Argo CD Lua health check lives in our repo and is offered upstream at
beta. Phase 1 e2e steps now.
```

The answer to question 42.3 of the same date is: "Kubernetes operators as
import sources: not now."

The project owner approved every decision of this ADR. The label "(owner)"
names a decision that the words of 1 November 2026 or of 18 October 2026
state.

The shapes below come from a read of the Pulumi Kubernetes Operator version
2.10.0: its README, its `docs/migration.md` and its API reference
`docs/stacks.md`.

## Decision

1. **OpenDustpan builds its own Kubernetes operator (owner).** The operator
   is tool-neutral. One set of resources declares units for every supported
   tool: the Pulumi CLI and OpenTofu today. Each tool keeps its own model
   below the resources (ADR 12, ADR 13). The operator merges no tool model.
   The operator has two kinds of resource (owner):
   - The primary resource applies a blueprint. The resource names the
     blueprint and gives its answers. OpenDustpan creates and updates the
     unit of the resource. The resource is a Catalog request (ADR 110). It
     carries the request document of ADR 111, with a blueprint version, the
     answers to its inputs, and whether the unit is present or absent. The
     document never holds tool code. One resource asks for one unit
     (ADR 110 decision 2), so a change to many units is many resources.
   - A second resource declares one unit directly, for a team that does not
     use blueprints. The `Stack` resource of the Pulumi Kubernetes Operator is
     its model (decision 2).
   - Both kinds go through the same jobs, gates and history (decision 4).
   - This ADR leaves the names of the resources to RFC 31.
   - A resource is mostly the inputs of one apply: the source, the Pulumi
     stack config or the OpenTofu variables, the placement in a namespace and
     a collection, and the apply policy (plan only, or apply after the
     gates). Secrets and credentials are references to a profile or to a
     Secret, never inline. The status reports the last plan or apply and the
     drift. The history stays in OpenDustpan.
2. **The model is the Pulumi Kubernetes Operator version 2
   (owner).** The operator takes these shapes:
   - A resource for a unit, with its own `spec` and `status`. The `Stack`
     resource of the Pulumi Kubernetes Operator is the model.
   - A source in the `spec`: a Git repository, a Flux source, or an inline
     program. This matches the `projectRepo`, `fluxSource` and `programRef`
     fields of that operator, and it matches RFC 31 for Flux sources.
   - A workspace pod for each unit. In our design the Kubernetes runner of
     ADR 136 makes this pod, as one Job for each claim.
   - `envRefs`: the `spec` names the environment variables of the work, and
     each value comes from a literal, a file or a Kubernetes Secret.
   - A service account for each unit. The cloud workload identity of the
     cluster then gives each unit its own role (RFC 31 phase 2).
   - A `status` that reports the last update, the update in progress, the
     outputs and the conditions.
   - The deletion setting `destroyOnFinalize`. The default of our setting is
     an open point (decision 7).
3. **The operator fills the gaps of RFC 31 (owner).**

   | Gap in the Pulumi Kubernetes Operator | What the operator here does |
   | --- | --- |
   | No approval step | A change goes through the gates, the access limits and the approval rules of OpenDustpan before it applies. The approval happens in OpenDustpan, not in the cluster. |
   | No history of its own | Each run is a job of OpenDustpan, with its plan, log, history and audit record. |
   | Pulumi only | The Pulumi CLI and OpenTofu use the same resources and the same jobs. |
   | Cluster-wide install only | A namespace-scoped install is an open point (decision 7). |
4. **The work runs through OpenDustpan jobs and gates (owner).** The
   operator does not run a tool. It creates units and requests jobs through
   the REST API (ADR 17). The Kubernetes runner of ADR 136 runs the jobs.
   The operator keeps no state of its own and uses no private endpoint. A
   script can do everything that the operator does.
5. **Crossplane compositions are out (owner).** They are not an alternative
   for the operator, and they are not a target of OpenDustpan. Reasons from
   RFC 31:
   - A team must rewrite its infrastructure as code as compositions.
   - A composition has no plan-and-approve step before a change.

   A team that uses Crossplane moves to the operator. A tool for this move
   is a later task. RFC 42 holds the migration questions. Decision 11 holds
   for operators as import sources.
6. **The place in the order (owner).** The operator comes after the Catalog
   ships (owner, 18 October 2026). The Catalog is the blueprint feature of
   ADR 110. The current order of the owner (1 November 2026) puts the
   operator after runners scoped to namespaces. That order is: milestones 4
   to 6, the wireframes, runners scoped to namespaces, then the backlog. This
   ADR gives the operator no milestone number. The roadmap lists it as not
   yet placed in a milestone.
7. **The open points stay in RFC 31 phases 3 and 4.** This ADR decides none
   of them:
   - The names of the resources and of the API group.
   - The versions of the resources, and the conditions that fit decision 10.
   - How a resource that applies a blueprint relates to the open questions of
     [RFC 111](../rfc/0111-blueprint-rollouts-and-the-migration-of-answers.md):
     a rollout of a new blueprint version to many requests, and who migrates
     the answers when a version renames, deletes or adds an input. The
     resource might overlap with both.
   - A namespace-scoped install of the operator.
   - How a Kubernetes namespace maps to an OpenDustpan namespace and to a
     collection. Decision 10 of ADR 51 stays in force: the operator does not
     map a Kubernetes namespace to a collection. A change of that decision
     needs a new ADR.
   - How the resource shows the drift status.
   - The deletion policy, and the default of `destroyOnFinalize`.

   The proposals of RFC 31, for example orphan as the default deletion policy
   (31.10), stay proposals.
8. **The other rules of RFC 31 stay as proposals.** They are the sign-in per
   Kubernetes namespace, one home for each value, plan before apply, and
   drift as a scheduled plan. The next ADR about the operator decides each of
   them.
9. **The operator ships with the coordinator (owner, 18 October 2026).** The
   operator shares the chart and the binary of the Kubernetes coordinator
   (ADR 136, ADR 139). It has no repository of its own. This answers the
   open question of ADR 136 and replaces the proposal of RFC 31 for a
   separate repository. The core server binary does not carry the operator.
10. **Every resource reports status in the kstatus form (owner, 18 October
    2026).** From the first version, each resource has `status.observedGeneration`
    and the conditions `Reconciling`, `Stalled` and `Ready`. So Flux health
    checks work with no change. A generic Argo CD Lua health check lives in
    our repository. We offer it upstream when the resource reaches beta. The
    conditions of RFC 31 (`Planned`, `WaitingForApproval`, `Applied`,
    `Failed`) are open: they must fit this form.
11. **Import from other operators is not in scope now (owner, 18 October
    2026).** A Kubernetes operator is not an import source. This holds for
    the `Stack` resources of the Pulumi Kubernetes Operator. A team keeps its
    operator and moves only its state (RFC 31 phase 1), or it declares new
    resources.
12. **The phase 1 e2e steps start now (owner, 18 October 2026).** They run
    the Pulumi Kubernetes Operator, tofu-controller and Burrito against
    OpenDustpan. This is the test that the interoperation note below needs.

## Interoperation note

The Pulumi Kubernetes Operator itself may already work with OpenDustpan
today. The `spec.backend` field of its `Stack` resource takes the URL of a
self-managed Pulumi Cloud-compatible service. The `spec.envRefs` field can
give `PULUMI_ACCESS_TOKEN` from a Secret. The ESC environments of that
operator, and the registration of an OIDC issuer in Pulumi Cloud, do not
apply to OpenDustpan. This is not tested. The e2e steps of decision 12 test
it. This note decides nothing.

## Consequences

- RFC 31 option 2 ("phase 1 and 2 only") does not apply now. The operator of
  phases 3 and 4 has the acceptance of the owner in principle. The details
  stay open.
- The model of phase 4 is no longer Crossplane. The text "This is
  Crossplane's model" in RFC 31 describes the earlier proposal. A blueprint
  as a resource is the primary use of the operator, and a unit as a resource
  is the second.
- The operator needs an API that already exists in full. The REST API of
  units, jobs, gates and tokens must cover every action of the operator
  before the operator starts (ADR 17).
- A team that wants Kubernetes resources before the operator exists can use
  phase 1: keep its operator and use OpenDustpan as the backend.
- The roadmap lists the operator in the table "Later", with the Catalog as
  the thing it waits on. A later ADR gives it a milestone.
- The operator needs the Catalog, so it can start only after the blueprint
  feature of ADR 110 ships.

## Not decided here

Each point of decision 7, and each rule of decision 8, stays open in
[RFC 31](../rfc/0031-kubernetes.md).
