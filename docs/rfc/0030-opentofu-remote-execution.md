---
rfc: 30
title: CLI-driven remote execution for OpenTofu
status: draft
created: 2026-10-10
---

# RFC 30. CLI-driven remote execution for OpenTofu

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

RFC 1 serves OpenTofu with `operations: false`: every plan runs on the
workstation. HCP Terraform, OTF, Terrakube and OpenTaco (beta) let `tofu plan`
on a laptop run remotely and stream the log back. RFC 6 notes this as a
follow-up to RFC 1.

## First ideas

- Serve the TFE run, plan, apply and configuration-version endpoints that the
  CLI uses for remote operations.
- A runner (RFC 6) claims the run; the backend only queues it and streams
  logs.
- Decide whether to serve the TFE agent-pool API or keep RFC 6's own runner
  protocol.

## Open questions

- Which subset of the TFE run API does the OpenTofu CLI need? This needs a
  check with `make e2e`.
- How do runner labels map to TFE agent pools?

## Proposal for Milestone 5: the remote apply (2026-10-30)

This section is ready to accept. It answers the six points of ADR 254
decision 10, and one point that the research for this proposal found in
ADR 254 decision 6. The facts come from the OpenTofu source (`main` at
`073d2cd`, 1.14.0-dev, and the tag `v1.6.0`), go-tfe `v1.101.0`, OTF and
Terrakube, and are in the research note
[m5-precedents](../research/m5-precedents.md) with the file of each fact.
Each behavior below gets an e2e step against each OpenTofu version of the
matrix before its route lands.

### What the CLI does (summary of the research)

- The CLI runs remotely when `entitlement-set` answers `operations: true`
  and the workspace `execution-mode` is not `local`. It never reads
  `agent-pool-id`.
- `tofu apply` plans first, as `tofu plan` does, with `speculative=false`.
  When the run has changes and `actions.is-confirmable` is true, it asks
  the person. `yes` calls `POST /runs/:id/actions/apply`. Another answer
  calls `actions/discard` when `is-discardable` is true. When
  `is-confirmable` is false, the CLI stops after the plan.
- While the prompt waits, the CLI reads the run each 3 seconds. When
  `is-confirmable` turns false, it goes on with no call: a person gave the confirmation
  the run in another place.
- After the confirmation, the CLI reads the run with backoff while its
  status is `pending` or `confirmed`, and reads the queue routes each 30
  seconds. Then it reads the log of the apply.
- `-auto-approve` creates the run with `auto-apply: true`, and the CLI never
  calls `actions/apply`.
- `tofu plan -out FILE` (since OpenTofu 1.6.0) creates a `provisional`
  configuration version and a run with `save-plan: true`, and writes a JSON
  file with the run ID, which must start with `run-`. `tofu apply FILE`
  checks the host and the workspace, needs `is-confirmable`, and calls
  `actions/apply` with no prompt.
- Ctrl-C asks "Do you want to cancel the remote operation?" and calls
  `actions/cancel` when `is-cancelable` is true. OpenTofu never calls
  `force-cancel`.
- `GET <log-read-url>?limit=&offset=`: the client replaces the whole query
  string and sends **no** bearer token. OTF and Terrakube put a signature in
  the path.
- The `remote` backend uses the service `tfe.v2.1`, takes `auto-apply` from
  the workspace, refuses `-var`, `-var-file` and `-out`, drops `TF_VAR_*`,
  reads no run events and no task stages, and needs the workspace
  `terraform-version` to match the local version.

### Questions and recommendations

1. **How `is-confirmable` and `actions/apply` map to the gates (ADR 254
   decision 10, first point).**
   - (a) A remote apply is one job with the phases `plan` and `apply` (ADR
     100 decision 4), started by the principal of the token of the CLI with
     the source kind `cli`. After the plan phase, the run is `planned` with
     `is-confirmable` true when the job has changes and no `deny`.
     `actions/apply` is the request of that person to apply (ADR 190
     decision 10 reads the same way for a change order). It does not
     approve. The job then meets the gates of `change.apply`. While it waits
     for approvals of other people, the run status is `confirmed`, so the
     CLI keeps waiting, and a person approves in the console or through the
     API. A rejection or an expiry ends the run `discarded`.
   - (b) `is-confirmable` is true only when every requirement is met. Else
     the CLI stops after the plan, and a person gives the confirmation later in the
     console.

   *Recommendation: (a).* The person at the terminal sees the apply when the
   approvers approve, as with a CI job that waits for a GitHub environment
   reviewer. (b) leaves a CLI that ended and a run that someone must find.
   `actions/apply` needs `unit:run-apply` (or `unit:run-destroy`), and it is
   allowed to a principal other than the starter, as HCP Terraform allows.
   Its audit entry names that principal.
2. **The apply phase applies the stored plan (second point).**
   *Recommendation:* yes, as ADR 102 decision 7 already says for each job.
   The apply step is `tofu apply` on the `plan-apply` part of the plan
   phase of the same job. A stale plan sends the job back to its plan phase
   (`replan`). For a CLI run, the run then ends `errored` with the message
   "The state changed after the plan. Run tofu apply again.", because the
   CLI cannot show a second plan for one run.
3. **`-auto-approve` and `autoApply` of the decision record (third point).**
   - (a) `auto-apply: true` is the request of the starter at start. The job
     applies without a second call only when the decision record of
     `change.planned` has `autoApply: true` (ADR 229 decision 3) and no
     approval requirement waits. Else the run waits in `confirmed` as in
     question 1.
   - (b) `auto-apply: true` passes the approval wait.

   *Recommendation: (a).* (b) lets a flag of the CLI skip an approval rule,
   which ADR 225 refuses. For the `remote` backend, the workspace setting
   `auto-apply` has the same meaning.
4. **The saved cloud plan of `-out` (fourth point).**
   - A run with `save-plan: true` is a plan job whose apply phase waits for
     a request: the run is `planned_and_saved` with `is-confirmable` true.
     `tofu apply FILE` calls `actions/apply`, and question 1 applies.
   - The run ID on the TFE-compatible API is `run-` and the ID of the job
     without its own prefix, so a job maps to one run and back.
   - The plan expires with the maximum plan age (24 hours by default, ADR
     102 decision 7). A run whose plan is older answers `is-confirmable`
     false, and the CLI shows the error of its status.
   - The research found that `tofu apply -auto-approve FILE` may skip
     `actions/apply`. An e2e step checks this. If it does, the run applies
     with question 3.

   *Recommendation:* this design.
5. **The `remote` backend (fifth point).**
   - (a) Serve it in Milestone 5, with its own e2e step: `tfe.v2.1` in
     service discovery, plans and applies with the limits that the CLI sets.
     The workspace answers `terraform-version` as the version that the pool
     of the unit reports for its runners, and the docs say that the
     workstation must run the same version.
   - (b) Serve the `cloud` block only.

   *Recommendation: (a).* ADR 254 decision 9 already waits for its e2e
   step, and teams that run old configurations use the `remote` backend.
6. **Runner labels and agent pools (sixth point).**
   - (a) No map. The CLI never reads `agent-pool-id`. The workspace answers
     `execution-mode: agent` and `agent-pool-id` with the ID of the runner
     pool of its unit settings, read-only. The TFE agent pool API is not
     served (ADR 254 decision 4). Labels stay text for people (ADR 130
     decision 8).
   - (b) Serve the agent pool API over runner pools, for the `tfe`
     provider.

   *Recommendation: (a).* No CLI behavior depends on it, and the pool of a
   unit has one home: the unit settings (RFC 12).
7. **The log URL needs a signature (ADR 254 decision 6).** ADR 254 decision
   6 reads the log "with the token of the CLI". The client sends no token
   to `log-read-url`.
   - (a) A signed URL: `/api/v2/logs/<job ID>/<phase>/<expiry>/<MAC>`, with
     an HMAC of the job, the phase, the principal and the expiry under a
     key of the installation. The server makes it when it answers
     `GET /plans/:id` or `GET /applies/:id`, only for a caller with
     `job:read-logs`, with an expiry of 1 hour. At each read, it checks the
     MAC, the expiry, and that the principal still holds `job:read-logs`.
   - (b) A URL with no check. Anyone with the URL reads the log.

   *Recommendation: (a).* This changes ADR 254 decision 6, so the ADR of
   this RFC records it. OTF and Terrakube sign the URL too.
8. **Cancel and discard.** `actions/cancel` cancels the job (ADR 100
   decision 11). During a step that changes resources, the job goes to
   `canceling`, and the launcher sends `SIGINT` with the grace period of 10
   minutes. `actions/discard` of a run that waits rejects the job with the
   reason "discarded by <principal>". `force-cancel` is not served, because
   no CLI sends it. *Recommendation:* this mapping.
9. **The arguments of a run.** `is-destroy` maps to the operation
   `destroy`, `refresh-only` to the operation `refresh` with the phases
   `plan` and `apply` for a CLI run (an addition to the table of ADR 100
   decision 4, because the person must see the plan before the
   confirmation), `target-addrs` and `replace-addrs` to `-target` and
   `-replace` arguments. Each address passes a parser of resource addresses
   (an allowlist of the grammar), and the launcher gets them as separate
   arguments, never as text for a shell. *Recommendation:* this mapping.

### Ports

- `internal/api/tfe`: the run, plan and apply resources over
  `job.Service`, with a table-driven map of job states to run statuses and
  actions (`tfe.RunStatusOf`, `tfe.RunActionsOf`).
- `job.Service.Confirm` exists for the agent confirmation. The request of
  question 1 is a new method, `job.Service.RequestApply`, with its
  `jobtest` cases: it needs the plan to be done, the action of the
  operation, and it records the principal.
- `job.Spec.Args`: the fixed arguments of question 9, from an allowlist
  that `job.ValidateArgs` holds.
- `internal/logurl`: the signed URL of question 7, with its own tests.

### End-to-end steps

Each step drives the real OpenTofu CLI with the `cloud` block on a workspace
with `execution-mode: agent` and a test runner.

1. `tofu apply`, answer `yes`: the log of the plan, the prompt, the log of
   the apply, a final state version written by the job token.
2. `tofu apply`, answer `no`: the run ends `discarded`, no state version.
3. A gate needs one approval of another person: after `yes`, the CLI waits.
   The test approves through the API, and the CLI shows the apply.
4. `tofu apply -auto-approve` with no requirement: no prompt. With a `warn`
   gate in `enforce` mode: the run waits in `confirmed`.
5. `tofu plan -out plan.tfplan`, then `tofu apply plan.tfplan`. A second
   case with `-auto-approve` checks the finding of question 4.
6. Ctrl-C during the apply: `yes` to the cancel question, the job ends
   `failed` or `canceled` as ADR 100 decision 7 says, and the state lock is
   released.
7. `tofu apply -destroy`, `tofu apply -refresh-only`, `-target` and
   `-replace`.
8. The `remote` backend: `tofu plan` and `tofu apply` with the workspace
   `auto-apply` off and on.
9. A state write from a workstation between the plan and the `yes`: the
   run ends `errored` with the message of question 2.

### Security tests (ADR 250)

| Step | Test with hostile input sent straight to the step |
| --- | --- |
| Check at the boundary | `target-addrs` with `;`, a newline, `-chdir=x`, a quote and 10,000 entries: refused. A run ID with another prefix, and the ID of a job of another namespace: 404. |
| Contain | Each address reaches the launcher as one argument. Run variables never reach a log, an answer or an audit entry (ADR 254 decision 5). |
| Limit | The log URL expires after 1 hour. A URL with a changed job, phase, expiry or MAC is refused. An archive above the limit of the pool is refused (ADR 254 decision 3). |
| Authorization at the point of use | `actions/apply` without `unit:run-apply`, `actions/discard` without `unit:run-apply`, and a log URL of a principal who lost `job:read-logs`: refused. |
| Fail closed | An unknown job state maps to the run status `errored`, never to `applied`. An error of the gate engine keeps the run in `confirmed` and writes the record (ADR 224 decision 13). |

### Work items

| ID | Item | Size | Conflict group | Depends on |
| --- | --- | --- | --- | --- |
| RX-0 | ADR 254 slice 1, speculative plans (decided, not built) | L | api-tfe | M5-1 |
| RX-1 | The signed log URL (question 7) | S | api-tfe | RX-0 |
| RX-2 | Remote apply: `RequestApply`, the status map, `actions/apply` and `actions/discard`, e2e steps 1, 2, 3 and 9 | L | api-tfe | RX-1 |
| RX-3 | `auto-apply` with `autoApply` of the decision record, e2e step 4 | S | api-tfe | RX-2 |
| RX-4 | Saved cloud plans, e2e step 5 | M | api-tfe | RX-2 |
| RX-5 | Cancel, destroy, refresh-only, target and replace with `job.ValidateArgs`, e2e steps 6 and 7 | M | api-tfe | RX-2 |
| RX-6 | The `remote` backend, e2e step 8 | M | api-tfe | RX-2 |
| RX-7 | The workspace answers of question 6, docs page "Run OpenTofu remotely" | S | api-tfe | RX-2 |

## Outcome

Accepted in part as [ADR 254](../adr/0254-speculative-remote-plans-for-opentofu-first.md)
on 2026-10-20: speculative plans on runners come first. The apply, the
saved cloud plan, the `remote` backend and the map of runner labels to
agent pools stay open (ADR 254 decision 10).
