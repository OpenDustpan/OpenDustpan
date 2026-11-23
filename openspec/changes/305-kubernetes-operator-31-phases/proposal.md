---
version: 2
---

## Why

**Persona: operator.** An Operator runs workloads on Kubernetes and wants to declare stacks and workspaces as Kubernetes resources with plan before apply, approval in OpenDustpan, and kstatus status that Flux and Argo CD already understand.

## What Changes

- Coarse placeholder only: no API, no CLI, no docs page, and no operator binary change ships in this change.
- Records that RFC 31 phases 3 and 4 stay open under ADR 305 decision 7 and wait on the Catalog and runners scoped to namespaces.

Out of scope:

- RFC 31 phases 1 and 2 interoperation work
- Catalog blueprint rollout semantics
- Runner and coordinator implementation

Depends on:

- 31-kubernetes

## Usability

Where the path still costs the person time or surprises them:

- No user path changes in this coarse change; kubectl apply, status conditions, and approval behavior belong to the refining RFC.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main already names coordinator Kubernetes jobs through runner tests and keeps no operator state. (`pkg/runner/coordinator_test.go:63 (TestKubernetesJobName)`)
- Main already refuses hostile blueprint rows in the self-service blueprint store. (`internal/store/postgres/self_service_blueprints_internal_test.go:93 (TestSelfServiceBlueprintReadRefusesHostileRows)`)
- Main already gates job phases on manual locks by phase start time. (`pkg/dustpan/manual_lock_authorizer_test.go:179 (TestManualLockPassesOnlyAPhaseThatStartedBeforeIt)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 305: Our own Kubernetes operator, modeled on the Pulumi Kubernetes Operator** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 2 (current), 4 (current), 7 (current), 10 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "OpenDustpan builds its own Kubernetes operator (owner). The operator is tool-neutral. One set of resources declares units for every supported tool: the Pulumi CLI and OpenTofu today."
- Quote: "The work runs through OpenDustpan jobs and gates (owner). The operator does not run a tool. It creates units and requests jobs through the REST API (ADR 17)."
- Quote: "The open points stay in RFC 31 phases 3 and 4. This ADR decides none of them: - The names of the resources and of the API group."
- Quote: "**Every resource reports status in the kstatus form (owner, 18 October 2026).** From the first version, each resource has `status.observedGeneration`"
- Effect: This coarse change stays inside ADR 305: tool-neutral resources, jobs and gates through the REST API, no merge of tool models, shared coordinator chart and binary, kstatus status from the first version. It decides nothing that decision 7 leaves open.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What are the names of the resources and of the API group?
   - Options: One Unit kind with a tool field under group opendustpan.io, with no blueprint kind decided now; Mirror per-tool kinds named like the Pulumi operator and tofu-controller.
   - Provisional decision (owner review 14 Nov): One Unit kind with a tool field under group opendustpan.io, with no blueprint kind decided now
   - Reason: Provisional decision (owner review 14 Nov): one Unit kind with a tool field under group opendustpan.io, with no blueprint kind decided now. Tool-neutral names follow ADR 305 decisions 1 and 13, and the blueprint relation stays open under decision 7 and RFC 111. Benchmark: Google: GKE Config Sync uses kstatus conditions and observedGeneration, and keeps approval in the control plane rather than in the cluster.
   - Persona impact: One tool-neutral kind keeps kubectl use predictable; no persona loses the direct REST path. Escape hatch: Teams keep using the REST API, the Pulumi CLI, and OpenTofu directly; the operator is an optional path.
   - Owner-tagged ADRs involved: ADR 305 (their words do not change).
2. Do the other rules of RFC 31 stay as proposals or become decisions for the operator?
   - Options: Adopt one home for each value, plan before apply, and drift as a scheduled plan as stated; Change one or more of those rules in the operator RFC.
   - Provisional decision (owner review 14 Nov): Adopt one home for each value, plan before apply, and drift as a scheduled plan as stated
   - Reason: Provisional decision (owner review 14 Nov): adopt one home for each value, plan before apply, and drift as a scheduled plan as stated. This is provisional only and not an accepted rule: ADR 305 decision 8 leaves the decision to the next ADR about the operator. Benchmark: AWS: Controllers for Kubernetes keep plan-before-apply and scheduled drift as control-plane decisions with versions and conditions fixed before v1.
   - Persona impact: Keeping plan before apply protects production stacks and workspaces; no surprise applies from the cluster. Escape hatch: Teams keep driving plans, approvals, and applies through jobs and gates without the operator.
   - Owner-tagged ADRs involved: ADR 305 (their words do not change).
3. What is the exact trust, token exchange, and grant shape for the operator identity?
   - Options: One principal per Kubernetes namespace via service-account token exchange with explicit cluster to namespace map; One principal per cluster with namespace scoping in claims.
   - Provisional decision (owner review 14 Nov): One principal per Kubernetes namespace via service-account token exchange with explicit cluster to namespace map
   - Reason: Provisional decision (owner review 14 Nov): one principal per Kubernetes namespace via service-account token exchange with explicit cluster to namespace map. The refining RFC names the map, the refused-exchange behavior, and hostile tests for a hostile tenant service account at token exchange, authorizer, and admission. Benchmark: ISO 27001: privileged workload access uses one identity per namespace with short-lived exchange and least privilege per unit.
   - Persona impact: One principal per namespace limits blast radius and matches how operators already scope service accounts. Escape hatch: Administrators can revoke the trust rule of one Kubernetes namespace without touching other namespaces.
   - Owner-tagged ADRs involved: ADR 305 (their words do not change).
4. What does the operator do on 421, fenced writes, maintenance mode, refused token exchange, rejected plans, and lost REST connectivity?
   - Options: Requeue with bounded backoff on 421, 429, and 503 and on fenced writes, set Stalled on 403, on 404 for a deleted unit, on refused exchange, and on rejected plans; Fail the resource at once on any refused write.
   - Provisional decision (owner review 14 Nov): Requeue with bounded backoff on 421, 429, and 503 and on fenced writes, set Stalled on 403, on 404 for a deleted unit, on refused exchange, and on rejected plans
   - Reason: Provisional decision (owner review 14 Nov): requeue with bounded backoff on 421, 429, and 503 and on fenced writes, and set Stalled on 403, on 404 for a deleted unit, on refused exchange, and on rejected plans. The refining RFC sets the backoff bound and retry count before Stalled, and reflects Reconciling while retrying. Benchmark: AWS: Controllers for Kubernetes retry on throttling and fence conflicts with bounded backoff and surface Stalled without local state.
   - Persona impact: Requeue keeps kubectl status predictable and avoids a surprise apply after a failover or maintenance window. Escape hatch: Teams keep driving jobs through the REST API during an outage of the operator; the operator writes no state of its own.
   - Owner-tagged ADRs involved: ADR 305 (their words do not change).
5. How does a resource refer to a unit after a rename or a delete and recreate?
   - Options: Store the stable unit ID of the stack or workspace in the resource status and bind by it, and show Stalled with reason unit gone when the ID is soft-deleted or purged; Bind by tool, namespace, and unit name only.
   - Provisional decision (owner review 14 Nov): Store the stable unit ID of the stack or workspace in the resource status and bind by it, and show Stalled with reason unit gone when the ID is soft-deleted or purged
   - Reason: Provisional decision (owner review 14 Nov): store the stable unit ID in status and bind by it, and show Stalled with reason unit gone when the ID is soft-deleted, restored, or purged, including restore in maintenance mode that raises the failover version. Benchmark: Google: Config Sync refers to managed objects by stable IDs so renames and deletes with the same name do not rebind state.
   - Persona impact: Stable ID binding avoids a recreated unit with the same name inheriting the old status and approvals. Escape hatch: Operators can still select units by tool and name in kubectl while the server binds by stable ID.
   - Owner-tagged ADRs involved: ADR 305 (their words do not change).

Questions considered and answered:

- Does a Kubernetes resource merge the Pulumi and OpenTofu models? ADR 305 decisions 1, 2, and 13 answer it: one tool-neutral set of resources modeled on Pulumi Kubernetes Operator v2, and each tool model stays in its own domain package.
- Does the operator need its own private API or its own state? ADR 305 decision 4 answers it: the operator creates units and requests jobs through the REST API, runs no tool, keeps no state, and uses no private endpoint.
- When does the operator ship and where does its code live? ADR 305 decisions 6 and 9 answer it: the operator comes after the Catalog ships and after runners scoped to namespaces, and it ships with the coordinator chart and binary with no repository of its own.
- Does ADR 305 already settle the operator identity shape? ADR 305 decision 14 proposes one principal per Kubernetes namespace via service-account token and exchange, but it does not settle the exact trust rule, exchange claims, or grant shape; open question 3 records that gap.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
