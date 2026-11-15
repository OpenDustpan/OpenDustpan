## Context

This is part 3 of the split of run-all-on-runner. The earlier parts in depends_on make one discovery job, one change order, and one plan plus one apply job per workspace. This part only orders the start of apply jobs on stored dependency edges. RFC 115 in the pack is a draft stub with no outcome on dependency order.

## Decisions

1. **Shim placement.** Keep all run-all logic in the runner shim and the launcher. The TFE-compatible API adapter keeps no run-all logic. The shim uses only the change-order API to start plan and apply jobs.
2. **Dependency gate.** Gate apply start in the server on stored dependency edges written at discovery. An apply job leaves queued only when each apply job it depends on succeeded. A failed dependency marks each dependent apply blocked with no apply call. Edges hold only stable workspace IDs and the write rejects a cycle. A blocked apply becomes queued again after each failed dependency is retried to success. The gate state is stored in the same transaction as the job state so a restart or two completions at once start each apply at most once. Discovery writes at most 100 dependency edges per change order; a write with more than 100 edges is rejected with the NEW error code `change-order.too-many-edges`, stores no edges, and repeats nothing of the input.

## Structure

Unexported type dependencyGate with constructor newDependencyGate(edges) that checks the edge list is acyclic, has at most 100 edges and holds only stable workspace IDs. Only the constructor can make a value, and start checks read only that value. No name is stored, only the stable ID of each workspace.

## Risks

- Apply jobs start out of Terragrunt order and break dependent infrastructure.
- A retry of one apply job applies the state of another workspace.
- A blocked apply waits without a record and the user cannot see why.
