## Context

ADR 368 decision 6 replaces "a replacement is never disjoint" with a closure test in the one `ConflictChecker`. Main has the port (`pkg/reviewrequest/ports.go:137`) and pending conformance cases, but no adapter, no dependency edges in `PlanClosure` and no plan JSON decoder. A wrong "disjoint" lets two applies run that read and write the same instance: a stale value in the cloud, or a lost change in the state. So this is security code (ADR 250), and every unproven step is a conflict.

## Decisions

1. **One graph type that computes its own closure.** `reviewrequest.PlanGraph` has unexported fields. Only `reviewrequest.NewPlanGraph(ops []Operation, reads []string, edges []Edge, unresolved []string) (PlanGraph, error)` makes a value. The constructor:
   - validates each address against the OpenTofu address grammar (allowlist) and refuses an empty plan, a duplicate operation and an edge to an address that is not in the plan or the prior state;
   - computes the downstream closure of each replaced address by a breadth-first walk over the reverse edges, with a limit of 50,000 nodes and 200,000 edges;
   - marks the graph not provable when the walk reaches an address in `unresolved`, a data source, a provider configuration that reads a replaced address, or a limit.

   A caller can never pass a closure by hand, so a closure that misses a reader cannot exist. The zero value of `PlanGraph` is not provable, so a forgotten constructor call is a conflict.
2. **The closure test.** `reviewrequest.ProveReplaceDisjoint(a, b PlanGraph) ConflictResult` holds for a replacement X of `a` only when:
   1. X is not in the write set or the read set of `b`;
   2. each address of the closure of X is in the write set of `a`;
   3. no address of the closure is in the read set or the write set of `b`, and no address of `b` has an edge to an address of the closure;
   4. both graphs are provable.

   The checker runs the test in both directions. One failure is a conflict. `Resources` names the colliding addresses. `Reason` is one of a fixed set: `closure-reaches-other-plan`, `closure-not-in-write-set`, `closure-unproven`.
3. **The first adapter answers "conflict" for each case that it does not prove.** `internal/conflictcheck.Checker` implements `reviewrequest.ConflictChecker`. It proves disjoint only (a) two plans with create and update operations on separate addresses and no shared read, and (b) a replacement that passes decision 2. A delete, a provider change, a `moved` block or alias, a data source, a change of a parent resource, an unseen reader (ADR 299 decision 41) and an error each give a conflict. The switch on the operation kind has no default branch that answers disjoint: an unknown kind is a conflict, and `exhaustive` in golangci-lint checks the switch.
4. **A strict decoder of the plan JSON.** `internal/tofuplan.Decode(r io.Reader, limit int64) (reviewrequest.PlanGraph, error)` reads the `plan-display` part. It reads at most 64 MiB, refuses a `format_version` that it does not know, and reads only `resource_changes`, `configuration` (references and `depends_on`), `prior_state` (`dependencies`) and `relevant_attributes`. An expression that the decoder cannot resolve to resource addresses (a module output that the plan does not resolve, a function call over a data source) goes into `unresolved`. It never runs a binary.
5. **The register route uses the checker.** The apply claim register route of two-disjoint-claims calls the checker for a claim with a replacement, in place of its fixed 409 `claim.closure-unproven`. A checker error is a conflict.

## Structure

- `PlanGraph` with unexported fields and the constructor of decision 1: the structure that makes a hand-written, incomplete closure impossible.
- The zero value is not provable.
- No custom analyzer and no new tool (ADR 364). `exhaustive` covers the switch on `OperationKind`.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| Decoder (`internal/tofuplan`) | Size limit, known `format_version`, address allowlist, unresolved expressions marked, no binary run. | `TestDecodeHostilePlans`: a 65 MiB plan, an unknown format version, an address with a newline and a `../`, a reference cycle, an edge to an unknown address, a module output with no resolution, duplicate operations. Each gives an error or a not-provable graph, never a provable one. |
| Domain (`reviewrequest.NewPlanGraph`, `ProveReplaceDisjoint`) | Constructor checks, own closure walk with limits, zero value not provable, both directions. | `TestProveReplaceDisjointHostile`: a graph that hides a reader behind 60,000 nodes, a closure that reaches the other plan through three hops, a provider configured from the replaced address, a data source in the closure, the zero value, `Check(a, b)` against `Check(b, a)`. Each is a conflict. Each guard has a mutation test: remove the guard and the test fails. |
| Adapter (`internal/conflictcheck`) | Only two proofs; each other case and each error is a conflict. | `reviewrequesttest.RunConflicts` with each never-disjoint case as a real test, plus `TestCheckerUnknownKindIsConflict`. |
| HTTP handler (register route) | Body size limit, a checker error is a conflict, the answer names addresses and a fixed reason, never a plan value. | `TestRegisterClaimHostileReplace`: a checker that returns an error, a checker that panics (recovered as a conflict), a claim whose plan part is missing. Each gives 409 or 422 and stores no claim. |

A second layer outside this change: the merge of concurrent-applies-merge-core refuses a write-write and a stale read at the upload. It does not rely on this check.

## Risks

- A plan JSON shape of a new OpenTofu version: the decoder refuses an unknown `format_version`, so the apply takes turns.
- False conflicts: an unresolved module output is a conflict. This is the price of failing closed.
