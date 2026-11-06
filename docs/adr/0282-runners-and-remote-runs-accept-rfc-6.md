# 282. Runners and remote runs: the project accepts RFC 6

Status: accepted · 2026-10-30. Decided by the project owner on 30 October 2026. Accepts [RFC 6](../rfc/0006-runners-and-remote-runs.md). Accepts ADR 139 and supersedes its decision 4. Moves the open gaps of RFC 6 to RFCs 107 to 110

Changed by:

- ADR 304 changes decision 5: the answer to the `terragrunt run-all` question.

## Context

[RFC 6](../rfc/0006-runners-and-remote-runs.md) asks how OpenDustpan starts
an infrastructure-as-code tool for a team: a plan for each pull request, an
apply after a merge, a scheduled refresh, a destroy at a set time. Its status
is `draft`, and its outcome says "accepted in part".

ADRs 100 to 103 and 130 to 139 decide most of RFC 6:

| ADR | What it decides |
| --- | --- |
| [100](0100-a-shared-job-model-on-durable-operations.md) | The job model on durable operations: `pkg/job`, the job states, the expiry of queued and dispatched jobs, and the glossary entry for Job. |
| [101](0101-two-executors-write-back-and-runners.md) | Two executors: write-back and runners. Steps, step timeouts, cancel, and the mask of profile secrets in the log. |
| [102](0102-job-plans-and-logs.md) | Job plans and logs: storage, the masked readable plan, redaction and retention. |
| [103](0103-job-gates-actions-and-audit.md) | Job gates, the job actions and the audit entries. |
| [130](0130-runner-pools-runners-and-coordinators.md) | Runner pools, runners, labels and coordinators. |
| [131](0131-the-runner-protocol.md) | The runner protocol: pull only, long poll, claims as leases, log chunks. |
| [132](0132-runner-protocol-versions-and-skew.md) | Runner protocol versions and version skew. |
| [133](0133-runner-identities-and-tokens.md) | Runner identities, join tokens, handoff tokens and job sessions. |
| [134](0134-the-launcher-contract.md) | The launcher contract inside the job container. |
| [135](0135-the-source-pipeline.md) | The source pipeline. It supersedes the "Source" item of RFC 6. |
| [136](0136-the-kubernetes-coordinator.md) | The Kubernetes coordinator: one Kubernetes Job for each job. |
| [137](0137-the-machine-coordinator.md) | The machine coordinator: a microVM, a gVisor container or a sandboxed process for each phase. |
| [138](0138-runner-actions-audit-and-api.md) | The runner actions, the audit entries and the runner API. |
| [139](0139-runner-dependencies-and-the-runner-module.md) | The dependencies of the coordinators and the runner module. Its status was `proposed` until this ADR. |

Other ADRs answer more of RFC 6. ADR 152 gives the retention of job logs.
ADR 213 decision 10 makes a manual lock stop new jobs of a unit, a
collection or a namespace. ADR 254 gives speculative remote plans for
OpenTofu. ADR 267 gives the run triggers.

On 30 October 2026 the project owner read RFC 6 and accepted it: "RFC6
lgtm". The owner gave the scope of this ADR: accept the parts that ADRs
100 to 103 and 130 to 139 decide, and move the open gaps to new draft RFCs.

The section "Gaps from the competitor review" of RFC 6 lists items that no
ADR decides. They need a design before they can be decided.

ADR 139 has the status `proposed`, but ADR 15 says that an ADR has no
"proposed" status. The owner decided both of its open points:

- **Dependencies, 18 October 2026.** The owner expected `client-go` and
  go-containerregistry, and accepted the options that a spike shows to be
  better (the context of ADR 139). The spike chose those two.
- **Boundary, 26 October 2026.** The owner decided "a separate GitHub repo
  instead of a Go submodule" for the runner. The project manager relayed
  this decision. This replaces the direction of 18 October 2026 that the
  boundary is a Go module, not a new repository.

## Decision

1. **The project accepts RFC 6.** Its decided parts are the decisions of ADRs 100
   to 103 and 130 to 139, with the amendment of decision 4 to ADR 139. This ADR adds no new rule to the job model or to
   runners. Where RFC 6 and one of those ADRs differ, the ADR applies.
2. **The principle of RFC 6 is a decision.** The backend never runs an
   infrastructure-as-code tool. A runner that the customer hosts runs it,
   in the network of the customer. A runner opens each connection. The
   backend never opens a connection into the network of a runner.
3. **The project accepts ADR 139 (owner, 18 October 2026).** The runner uses
   `client-go` v0.37.1 (with `k8s.io/api` and `k8s.io/apimachinery`
   v0.37.1) and go-containerregistry v0.22.1, as ADR 139 decisions 1 to 3
   say. Decision 4 of this ADR changes where the runner code lives. The
   other decisions of ADR 139 stay.
4. **The runner lives in its own repository (owner, 26 October 2026).**
   This supersedes ADR 139 decision 4 (a nested Go module).
   - The `dustpan-runner` binary, with its coordinators, `client-go` and
     go-containerregistry, lives in its own repository,
     `OpenDustpan/dustpan-runner`. It is not a nested module of this
     repository.
   - The server module never requires `client-go`, go-containerregistry
     or the runner module. The depguard rule `runnerdeps` and the module
     check `scripts/runner-deps-guard.py` of ADR 139 decision 5 stay, and
     apply to all of this repository.
   - The runner is a separate binary, not a role of the `opendustpan`
     binary, as ADR 139 decision 4 says.
   - The version skew between a runner and the server follows ADR 132.
   - The repository `OpenDustpan/dustpan-runner` does not exist yet. The
     owner creates it.
5. **Answers to the open questions of RFC 6.**

   | Open question of RFC 6 | Answer |
   | --- | --- |
   | A fresh container for each job, or the host? | ADR 136 and ADR 137: a Kubernetes Job, a microVM, a gVisor container or a sandboxed process for each job or phase. |
   | A job kind for `terragrunt run-all` that makes many runs? | Open. It moves to RFC 109. |
   | Is a job a domain package of its own? | ADR 100 decision 1: `pkg/job`. |
   | Which first trigger? | ADR 267: run triggers start a plan through the job service. |
   | Who can approve an apply? | ADR 103 and ADRs 222 to 230: gates and approval rules. |
   | Runner tokens from ADR 6, or a kind of their own? | ADR 133: join tokens, handoff tokens and job sessions. |
   | How long the backend keeps job logs, and secrets that the tool prints | ADR 102 and ADR 152 give retention and redaction. A mask that a step asks for at run time is open. It moves to RFC 110. |
   | Does the Pulumi deployments API matter? | Not in this scope. A runner uses the update API of the Pulumi CLI. A later RFC can ask for the deployments API. |

6. **The open gaps move to four draft RFCs.** Each gap of the competitor
   review that no ADR decides goes to one stub RFC:

   | RFC | Gaps |
   | --- | --- |
   | [RFC 107](../rfc/0107-run-limits-and-the-run-queue.md) Run limits and the run queue | A stop after a time with no log output. Run priority. Serialization of jobs by a key. |
   | [RFC 108](../rfc/0108-run-options-re-runs-and-run-variables.md) Run options, re-runs and run variables | Replace or target resources. Allow an empty apply. A retry with the same parameters. Run-scoped variables. A switch for debug logs. Standard run variables, such as the job ID and the commit. A tool version pin for each unit. |
   | [RFC 109](../rfc/0109-job-kinds-for-state-operations-and-many-runs.md) Job kinds for state operations and many runs | Jobs for import, `state rm` and `state mv`. A job kind for `terragrunt run-all`. |
   | [RFC 110](../rfc/0110-step-results-hooks-approval-steps-and-scanners.md) Step results: hooks, approval steps and scanners | Hooks on success and on failure. A step that asks for an approval. Scanner results with the run. A mask that a step asks for at run time. |

7. **Gaps that other decisions answer.** These gaps of RFC 6 get no new RFC:
   - Run timeouts and the expiry of queued jobs: ADR 100 and ADR 101.
   - A pause for each unit, collection or namespace: the manual lock of
     ADR 213 decision 10.
   - A refresh-only run: the `refresh` operation of ADR 100 decision 3.
   - Ephemeral runners: ADR 136, one Kubernetes Job for each job.
   - A global switch that stops applies: the manual lock of a namespace
     (ADR 213 decision 10) and freeze schedules (ADR 141). A switch for the whole
     installation stays with [RFC 27](../rfc/0027-pull-request-automation.md).

## Consequences

- RFC 6 gets `status: accepted` and `adr: 282`. Its outcome names this ADR.
- The roadmap row of RFC 6 in milestone 5 says "accepted (ADR 282)". The
  four new RFCs are not in milestone 5. The roadmap places them later.
- The glossary entries Runner, Job and Remote run follow ADR 100 decision 19,
  not the proposed entries of RFC 6.
- ADR 139 gets the status `accepted`, with a note that decision 4 of this
  ADR supersedes its decision 4.
- The items of the ADR 139 consequences that set up a nested module in
  this repository (`cmd/dustpan-runner/go.mod`, the checks in two modules,
  the `gomoddirectives` replacement, the second Dependabot directory and
  the release from the same tag) do not apply here. The runner repository
  sets up its own checks, release and license files.
- No code of the coordinators lands before `OpenDustpan/dustpan-runner`
  exists.
