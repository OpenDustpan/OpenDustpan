## Context

An operator runs workloads on Kubernetes and wants to declare stacks and workspaces as Kubernetes resources with plan before apply, approval in OpenDustpan, and kstatus status that Flux and Argo CD already understand.

This change is a coarse placeholder: no API, no CLI, no docs page, and no operator binary change ships in it. RFC 31 phases 3 and 4 stay open under ADR 305 decision 7 and wait on the Catalog and on runners scoped to namespaces. It changes no server code, no route, and no schema now.

## Decisions

1. **No code now.** Phases 3 and 4 ship after phases 1 and 2 (from 31-kubernetes) plus the Catalog and namespace-scoped runners land. The operator binary keeps its current behavior until then.
2. **Plan before apply stays the rule.** The operator creates units and requests jobs through the REST API; it runs no tool itself (ADR 305, decision 4). Drift stays a scheduled plan as a proposal, decided later.
3. **kstatus status only.** The operator reports status in the shape Flux and Argo CD already read. No second status vocabulary appears beside it.
4. **Approval stays in OpenDustpan.** No cluster-side approval path appears; approval steps gate the job as in 110-step-results-scanners.

## Structure

No new structure now. The bug class is a cluster-side state change that bypasses jobs, gates, and audit; routing all operator work through OpenDustpan jobs and gates is the structure that removes it. When the gates pass, the operator resource is an unexported declaration built only through its constructor, which checks the unit reference by stable ID and the namespace scope.

No custom analyzer and no new tool.

## Risks

- Operators wait for declared stacks until the Catalog and runners land. The docs page says so and points at the REST API path.
- A resource that names a unit by name follows a rename or a delete-and-remake. The constructor takes the stable ID, since a name is a label for people only.
