---
version: 1
---

## Why

**Persona: runner operator.** A runner operator tries to run runner pools on Kubernetes and to manage stacks and workspaces from the cluster. Today the runner model and routes exist on main but no Kubernetes hosting path is defined, so the operator has no supported cluster story.

The user path this change must make work:

1. **Docs page.** Runner operator installs a runner pool on Kubernetes from the docs page.
2. **API.** Runner claims jobs over the runner protocol and runs tool steps.
3. **Console.** Developer reads plan and apply results in history and inventory.

Evidence for the claims above:

- RFC 31 waits on runners and is not shipped. Proof: docs/roadmap.md:819 row states RFC 31 Kubernetes waits on runners (RFC 6)
- Main has a runner domain and runner routes but no decided Kubernetes hosting path. Proof: pack lists pkg/runner and route ANY /runner/v1 with no Kubernetes hosting route

## What Changes

- Refine RFC 31 phases 1 and 2 after runners land: Kubernetes-hosted runner pools and interoperation e2e against existing operators.

Out of scope:

- Operator resource names, versions and conditions beyond kstatus (change 305-kubernetes-operator-31-phases)
- Blueprint rollout relation (change 305-kubernetes-operator-31-phases)
- Crossplane compositions (ADR 305 decision 5 out)
- Import from other operators (ADR 305 decision 11 out)

Depends on:

- runners (RFC 6)

## Usability

Where the path still costs the person time or surprises them:

- Cluster install path is unknown until the RFC defines Kubernetes-hosted runner install.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main names Kubernetes job names in the runner coordinator (`pkg/runner/coordinator_test.go:63 (TestKubernetesJobName)`)
- Main schedules durable work with a first-claim delay (`pkg/durable/scheduler_test.go:175 (TestSchedulerWaitsTheFirstDelayBeforeItsFirstClaim)`)
- Main runs an operation started with a runner claim (`pkg/durable/runner_claimed_test.go:16 (TestRunnerRunsAnOperationStartedWithItsClaim)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 305: Our own Kubernetes operator, modeled on the Pulumi Kubernetes Operator** (owner-tagged (all decisions)).

- Decisions that apply: 4 (current), 6 (current), 9 (current), 12 (current), 14 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The work runs through OpenDustpan jobs and gates (owner)."
- Quote: "The place in the order (owner). The operator comes after the Catalog ships (owner, 18 October 2026)."
- Quote: "The operator ships with the coordinator (owner, 18 October 2026)."
- Quote: "The phase 1 e2e steps start now (owner, 18 October 2026)."
- Quote: "The identity of the operator (PM, for the owner)."
- Effect: This coarse change refines only RFC 31 phases 1 and 2: Kubernetes-hosted runners and interoperation e2e. It follows the job path, order, coordinator packaging, phase 1 e2e and workload identity decisions. Tool-neutral operator resources and kstatus reporting stay with change 305-kubernetes-operator-31-phases.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What are the resource names, API group, versions and conditions beyond kstatus, and how does a resource that applies a blueprint relate to blueprint rollouts?
   - Options: Leave names, versions and blueprint relations to RFC 31 phases 3 and 4; Decide them in this change.
   - Provisional decision (owner review 14 Nov): Leave them to change 305-kubernetes-operator-31-phases and ship Kubernetes-hosted runners with phase 1 interoperation e2e first.
   - Reason: ADR 305 decision 7 leaves these points open in RFC 31 phases 3 and 4 and decides none of them; Google practice keeps them stable from the first version. Benchmark: Google GKE practice: keep resource names, versions and conditions stable from the first version and report kstatus conditions.
   - Persona impact: Runner operator keeps machine pools and existing operators when Kubernetes hosting is not ready; no forced move. Escape hatch: Teams keep machine runners and existing operators; no forced move to new resources.
   - Owner-tagged ADRs involved: ADR 305 (their words do not change).

Questions considered and answered:

- How does Kubernetes execute work against OpenDustpan? ADR 305 decision 4 answers it: the operator creates units and requests jobs through the REST API; the Kubernetes runner runs the jobs. This change covers only the runner hosting side.
- Is interoperation e2e with existing operators an open scope choice? ADR 305 decision 12 answers it: phase 1 e2e against the Pulumi Kubernetes Operator, tofu-controller and Burrito starts now. It is decided work in this change.
- Is a Kubernetes runner pool scoped to a namespace or to the installation? ADR 305 decision 6 answers it: the operator comes after the Catalog and after runners scoped to namespaces.
- How does the cluster authenticate to OpenDustpan? ADR 305 decision 14 answers it: the operator signs in with its Kubernetes service account token through workload-identity trust rules and token exchange, with one principal per Kubernetes namespace.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
