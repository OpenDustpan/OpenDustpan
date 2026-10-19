---
rfc: 31
title: Kubernetes: runner, operator, and blueprints as resources
status: draft
created: 2026-10-10
---

# RFC 31. Kubernetes: runner, operator, and blueprints as resources

Status: draft. The phases, the resource names and the repository layout are
proposals.

## Problem

Teams that run Kubernetes manage much of their platform as resources in the
cluster, reconciled by Argo CD or Flux. For IaC they have three choices today,
and each has a gap:

- **Pulumi Kubernetes Operator:** `Stack`, `Program`, `Workspace` and `Update`
  resources, a pod per stack. No approval step, no UI or history of its own,
  Pulumi Cloud by default, cluster-wide install only, Pulumi only.
- **tofu-controller and Burrito:** OpenTofu in the cluster. Approval by editing
  YAML or by merging a pull request; state in a Kubernetes Secret or in the
  layer's own backend; a thin UI or none.
- **Crossplane and Upbound:** platform APIs as Kubernetes resources. Teams
  rewrite their IaC as compositions, and there is no plan-and-approve step
  before a change. The multi-cluster console is paid.

OpenDustpan should not half-support Kubernetes, and it should not become Argo
CD or Flux.

## Four roles of Kubernetes

| Role | Answer |
| --- | --- |
| Where OpenDustpan runs | A Helm chart and high availability (RFC 35, RFC 36). |
| Where jobs run | A first-class runner: a pod per job (phase 2). |
| Where people declare infrastructure | An operator with resources for units and for blueprints (phases 3 and 4). |
| A target that IaC deploys to | Not ours. The tools' providers do this. |

## Integrate with Argo CD and Flux, do not replace them

- Our resources are ordinary custom resources, so Argo CD and Flux apply them
  with no extra work.
- Ship an Argo CD health check for each resource kind, so that Argo CD shows
  "progressing", "waiting for approval" and "degraded" correctly.
- Read Flux `GitRepository`, `OCIRepository` and `Bucket` artifacts as sources
  (RFC 54).
- Send events that Flux's notification controller and Argo CD notifications can
  route.

## Rules for the operator

- **The operator is an API client (ADR 17).** It holds no state and uses no
  private endpoints. Everything it does, a script can do.
- **It signs in per Kubernetes namespace.** It exchanges a projected service
  account token through OIDC (RFC 20) for a short-lived OpenDustpan token. It
  never uses a cluster-wide admin token, so fine-grained authorization and
  policy apply to every request that a cluster makes.
- **One home for each value (RFC 12).** When a resource in the cluster declares
  a unit's inputs, the cluster is their home. An API edit to those inputs is
  refused with the name of the resource that manages them, for example
  "Managed by Database/orders in namespace payments."
- **Plan, then apply, unless policy says otherwise.** Status conditions carry
  the gate: `Planned`, `WaitingForApproval`, `Applied`, `Failed`. Approval
  happens in OpenDustpan (RFC 13), never through an annotation that anyone who
  can edit the resource could set.
- **Drift is a scheduled plan (RFC 25),** not a continuous apply. Automatic
  remediation is opt-in, through edit policy.
- **Each tool keeps its own words (ADR 13).** A `Stack` resource for the Pulumi
  CLI and a `Workspace` resource for OpenTofu. No resource that merges them.

## Phases

1. **OpenDustpan as the backend for the existing operators.** Add e2e steps that
   run the Pulumi Kubernetes Operator (any backend URL), tofu-controller
   (`backendConfig`) and Burrito (per-layer backend) against OpenDustpan. Their
   users get state history, locks and the console at once. Nearly free.
2. **A Kubernetes runner (RFC 6).** One pod per job from a pod template; a
   service account per unit, so that cloud workload identity (IRSA, GKE
   Workload Identity, Azure Workload Identity) gives each unit its own cloud
   role; the egress proxy (RFC 16) as a sidecar where a unit uses it.
3. **The operator, with `Stack` and `Workspace` resources.** Source, config,
   profile references and runner labels in the spec; plan summary, outputs (as
   allowed by RFC 15) and conditions in the status; outputs also written to a
   Secret when the resource asks.
4. **Blueprints as resources.** A blueprint's input schema (JSON Schema, RFC
   14) becomes the OpenAPI v3 schema of a generated custom resource
   definition. A developer applies `kind: Database` with a few fields. The
   operator makes a unit from the blueprint, a runner plans it, policy
   decides, and the outputs go to the resource's status and a Secret. This is
   Crossplane's model, with the team's existing Pulumi programs and OpenTofu
   modules, plans, approvals and history.

## Repository and dependencies

The operator lives in its own repository, like the console (ADR 8). A
controller framework (controller-runtime) is a large dependency that the core
server must not carry. The operator depends only on OpenDustpan's public API.

## Migration from the existing operators

Users of the Pulumi Kubernetes Operator, tofu-controller and Burrito need a
path that does not lose state or stop their pipelines. Phase 1 already lets
them keep their operator and move only the state. Moving their resources to
ours is part of RFC 42, and it is planned early, not at the end.

OpenDustpan does not accept another project's resource types (such as
`pulumi.com/v1` `Stack`) to make migration easier. That would use another
project's API group as ours. A migration tool converts them instead.

## Options

1. **The four phases above** (the proposal).
2. **Phase 1 and 2 only.** Be a good backend and runner for other operators;
   no operator of our own. Cheaper, and it leaves the self-service story to
   Crossplane.
3. **Become a GitOps engine** like Argo CD or Flux. Rejected: a second product,
   and those projects already do it well.

## Open questions

- API group and resource names: `opendustpan.io`? Which version to start at?
- Does one operator install serve one OpenDustpan namespace, or map many
  Kubernetes namespaces to many OpenDustpan namespaces?
- Who generates the custom resource definitions for blueprints: the operator at
  run time, or a build step that the platform team runs?
- How does the operator show a plan to a developer who only uses `kubectl`:
  a summary in the status, a link to the console, or both?
- Is the Argo CD health check shipped in our repository or contributed
  upstream?

### Lifecycle of a managed unit

The draft so far covers how a resource creates a unit. These questions cover
update and delete. Each recommendation is proposed, not decided.

**31.9 Lifecycle.** What does the operator do for each change to a resource?

- A new resource starts a plan, then an apply.
- A change to the spec starts a new plan. The approval rules (RFC 13) cover
  that plan before the apply starts.
- A deleted resource starts a destroy through a finalizer.

Proposed: all three, as listed. The operator is not limited to create.

**31.10 Default deletion policy.** What happens to the stack or workspace and
its infrastructure when the resource is deleted?

1. Orphan: keep the unit and its infrastructure. Release the resource.
2. Destroy: destroy the infrastructure, then remove the unit.
3. Destroy only after an approval.

Comparable: tofu-controller destroys only when `destroyResourcesOnDeletion` is
set. Crossplane has `deletionPolicy`, with the values `Delete` and `Orphan`.

This is a one-way door. A stray `kubectl delete`, or a GitOps prune that
removes a resource from the cluster, could destroy real infrastructure, and a
destroy cannot be undone. Proposed: orphan is the default. A resource opts in
to destroy, and a destroy waits for an approval unless policy says otherwise.

**31.11 Gates on a destroy.** Proposed: a destroy that starts from a resource
deletion goes through the same gates, access limits and approval rules as a
destroy that starts in the console. The operator has no separate path. The
approval happens in OpenDustpan, not in the cluster.

**31.12 A failed destroy.** A failed destroy leaves the finalizer in place, and
the resource stays in the `Terminating` state. Open questions: which condition
and event does the operator show, and how does a person resolve it? Options:
retry the destroy, orphan the unit and remove the finalizer, or remove the
finalizer by hand. Proposed: the operator sets a `Failed` condition that names
the job and links to its log. A person with the right permission chooses
retry or orphan in OpenDustpan. The operator never removes the finalizer
without that decision.

**31.13 Changes outside the resource.** A person can change the same stack or
workspace in the console or with the CLI. Options:

1. Treat the change as drift against the resource, and show it in the status.
2. Refuse the change, with the name of the managing resource, as for inputs
   (rule "One home for each value").

Proposed: refuse a change to the inputs that the resource declares. Treat a
change to the state, such as a CLI run outside the operator, as drift.

**31.14 Adoption.** How does a resource take over infrastructure that exists
already? Options: an import step before the first apply, or a resource that
refers to an existing stack or workspace. Proposed: an import into the stack
or workspace, then a resource that manages it, so that the first plan shows no
change for the imported objects.

**31.15 Related open themes.** Other issues affect the answers above.

- Provider and module supply chain (#1089): a job that the operator starts
  uses only the mirror.
- Day-two actions outside state (#1090): the operator never invokes an action
  on a drift reconcile.
- Run-time secret injection, and secrets kept out of state (#1091).
- Stale plans and lock contention on large shared states (owner research).
  A reconcile loop that plans often could make both worse. Consider a
  minimum interval and a check for a running job before a new plan.

## Outcome

Phase 2 is answered by ADR 136 (the Kubernetes coordinator), on 2026-10-18.
The other phases stay open.
