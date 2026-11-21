## Context

A runner operator tries to run runner pools on Kubernetes and to manage stacks and workspaces from the cluster. The runner model and routes exist, but no Kubernetes hosting path is defined, so the operator has no supported cluster story.

This change refines RFC 31 phases 1 and 2 after runners land: Kubernetes-hosted runner pools and interoperation e2e against existing operators. It changes no server code, no route, and no schema now.

## Decisions

1. **No code now.** The hosting path ships after runners land (RFC 6). Runner pools keep their current hosting until then.
2. **Phases 1 and 2 only.** Kubernetes-hosted runner pools plus interoperation e2e. Operator resource names, blueprint rollout, Crossplane compositions, and import from other operators stay out: Crossplane is out per ADR 305 decision 5, import is out per decision 11, and phases 3 and 4 stay in 305-kubernetes-operator-31-phases.
3. **Runners claim over the runner protocol.** Kubernetes hosting changes where the runner process runs, not how it claims jobs: leases with fencing tokens and the fence check stay as they are.
4. **Interoperation e2e proves the path.** The refinement lands with an e2e step against existing operators, since the real cluster behavior is the contract.

## Structure

No new structure now. The bug class is a cluster-hosted runner that claims outside the lease and fence rules; keeping the runner protocol unchanged across hosting paths is the structure that removes it. When runners land, the Kubernetes pool is a pool configuration value built only through its constructor, which checks the namespace scope and the claim rules.

No custom analyzer and no new tool.

## Risks

- Operators wait for a supported cluster story until runners land. The docs page says so and points at current pool hosting.
- A pool scoped to the wrong namespace runs work from another namespace. The constructor checks the namespace scope, and claims carry the fence.
