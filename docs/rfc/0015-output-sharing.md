---
rfc: 15
title: Output sharing between units
status: accepted
created: 2026-10-10
adr: 213, 273
---

# RFC 15. Output sharing between units

Status: accepted
([ADR 273](../adr/0273-output-sharing-rules-and-the-read-outputs-action.md)).

## Problem

Units read each other's outputs: a service stack reads the VPC ID from a
network stack. Each tool has its own way:

- **Pulumi CLI:** a `StackReference` reads the outputs of another stack
  through the backend. Pulumi Cloud has no permission for outputs alone. The
  nearest permissions are `stack:read` and `stack:decrypt`. Secret outputs
  come back as ciphertext, and the CLI decrypts them through the secrets
  provider of the stack that made them.
- **OpenTofu:** `terraform_remote_state` with the `cloud` or `remote` backend
  reads another workspace's state through the TFE-compatible API. TFE limits
  this with remote state sharing on each workspace (all workspaces in the
  organization, all in the same project, or specific workspaces), and with a
  separate permission to read state outputs. The `tfe_outputs` data source of
  the `tfe` provider reads only outputs, and remote state sharing does not
  apply to it.

So in both tools, a unit that needs one VPC ID often gets read access to a
whole state, which can hold secrets in plain text. Teams then either share
too much or copy outputs by hand.

## Proposal

### A separate action

Add a tool-neutral action **read outputs**, separate from **read state**
(ADR 13). It allows the outputs of the unit's latest state and nothing else.

Each tool package maps its endpoints to the two actions:

- An endpoint that returns only outputs needs read outputs.
- An endpoint that returns the whole state needs read state.

Whether each CLI asks for outputs only, or for the whole state, when it reads
another unit, needs a check against each real CLI and version with `make
e2e`. Where a CLI version reads the whole state, OpenDustpan can only give it
read state or nothing. The console says so on the sharing settings.

### Sharing is set on the unit that makes the outputs

The owner of a unit decides who reads its outputs, like TFE's remote state
sharing, with the same reach for both tools:

- every unit in the namespace,
- units with a label (for example `env=prod`), which also covers "the same
  group" once RFC 4 decides about groups,
- specific units, by unit reference,
- specific principals.

The default for a new unit is a namespace setting. Following RFC 9's posture,
a namespace can make "every unit in the namespace" its default.

### Who asks

A rule that says "unit B may read unit A's outputs" needs to know when unit B
is the caller:

- **In a job on a runner,** the job's token is bound to the unit that the job
  runs (RFC 6). The backend knows the caller is unit B, and unit-to-unit
  rules apply.
- **On a workstation,** the caller is a person's access token. The person's
  own access rules apply. A person who runs unit B needs read outputs on unit
  A too.

### Secret outputs (the hard part, for Pulumi)

A Pulumi secret output is ciphertext under the producing stack's data key. To
read it, the consuming CLI asks the backend to decrypt it with that stack's
key. But the decrypt endpoint takes any ciphertext. The producing stack's
config secrets use the same key, and their ciphertext is often in git, in
`Pulumi.<stack>.yaml`, readable by many people. A caller with decrypt on stack
A could decrypt A's config secrets, not only its outputs.

Possible answers, none chosen yet:

1. For a caller with read outputs only, the backend decrypts only ciphertext
   that is in the outputs of A's latest checkpoint. The backend then reads
   outputs from state, which the glossary says it never does. That rule would
   need a narrow exception.
2. The backend returns secret outputs already decrypted, in the outputs-only
   response, to callers with read outputs. This needs the CLI to accept that
   shape. It needs a check with `make e2e`.
3. Secret outputs are not shared. Only plain outputs are.

OpenTofu has no such problem in OpenDustpan: outputs marked sensitive are
plain text in state, and read outputs returns them as they are. If a team uses
OpenTofu's client-side state encryption, the consumer needs the key, and
OpenDustpan has no part in that.

### The graph

Every read of outputs by a job is recorded in the inventory as an edge: unit
B read unit A, at this time. Then the console can show:

- what a unit depends on, and what depends on it,
- a warning before a destroy of a unit whose outputs others read. Warn, never
  block (RFC 9),
- which consumers read an output that a new plan removes.

### Across tools

Reading a workspace's outputs from a Pulumi program might work through the
`pulumi-terraform` package's remote state reference, which speaks the TFE
remote state protocol. If it does, the TFE-compatible API serves it with the
same read outputs action. OpenTofu has no built-in way to read a Pulumi
stack's outputs. Both need a check before any promise.

## Options

1. **A read outputs action, sharing set on the producer, unit-bound job
   tokens** (the proposal).
2. **Do nothing.** Reading another unit needs read state. Simple, and it
   shares too much.
3. **Copy outputs into a profile** (RFC 5). After each update, chosen outputs
   go into a profile that consumers open. It works for every tool and every
   CLI version, but the copy can be stale, and it is a second home for a value
   (RFC 12).

## Open questions

- Which of the three answers for Pulumi secret outputs, and is option 1's
  exception to "never reads state" acceptable?
- Which CLI versions read outputs only? This decides how much the action is
  worth at first.
- Are references across namespaces allowed, with both namespaces' consent?
- Is the job token's unit binding the same identity that RFC 13's policy input
  calls the principal for a job?

## Gaps from the competitor review

Added 2026-10-10 from the research doc "IaC and TACOS landscape". Not yet
worked into the proposal above.

- Typed output contracts: versioned schemas for outputs, with version ranges
  on the consumer (Massdriver).
- A blast-radius view over the dependency edges (Harness, Atmos Pro).
- Downstream runs when an output changes are RFC 24.

## Proposal for Milestone 5: sharing rules and job-token reads (2026-10-30)

This section is ready to accept. ADR 213 decision 1 and ADR 273 decide the
rules, the checks, the routes and the answers. The rules, the setting and
the `Checker` for a principal are in the code. The `Checker` denies each
read by a job token today ("read by a job token is not served yet"). This
section gives the build of the job path and answers the two points that
ADR 273 left open. Facts about other products are in the research note
[m5-precedents](../research/m5-precedents.md).

### Questions and recommendations

1. **Whose grants decide check 2 for each kind of job start in Milestone
   5.** ADR 273 decision 5 names the starter, the last saver of a trigger
   and the attachment authority. Milestone 5 adds starts that these names
   do not cover yet:

   | Start | Principal of check 2 |
   | --- | --- |
   | A person or a token through the job API | the starter |
   | A trigger of the kind `schedule`, `follows` or `review-request-push` | the last saver of the trigger (ADR 267 decision 14) |
   | The apply of a change order of a review request after the merge (RFC 27) | the principal who asked for the apply: the linked person who merged, or the person who pressed apply |
   | A pre-merge apply by a comment command (RFC 27) | the principal linked to the account of the comment |
   | A remote run of the OpenTofu CLI (RFC 30) | the principal of the token of the CLI |

   *Recommendation:* this table, as one function `job.GrantsPrincipal`,
   with a table-driven test. A start whose principal the function cannot
   name fails closed at each read.
2. **`tfe_outputs` in a job (ADR 273 "Not decided here").** The `tfe`
   provider reads outputs with its own token, and remote state sharing does
   not apply to it in HCP Terraform. In a job, the provider has no token
   unless it reads the credentials helper of the launcher.
   - (a) Serve `tfe_outputs` to the job token where an e2e step shows that
     the provider of the pinned version reads the token from the CLI
     configuration of the launcher. Else the docs say that `tfe_outputs`
     works on a workstation only.
   - (b) Never in a job.

   *Recommendation: (a)*, with the e2e step first. The answer has the
   fields of ADR 273 decision 7 and, for a sensitive output, `null` when
   check 2 has no `unit:read-secrets`. HCP Terraform answers `null` for a
   sensitive value to a reader with no state access.
3. **A Pulumi program in a job that reads OpenTofu outputs (ADR 273 "Not
   decided here").** The `terraform` package of Pulumi takes the token of
   the remote state as an argument of the program. ADR 213 says that the
   job token never goes in a variable.
   - (a) In a job, the program reads the outputs through the profile block
     `outputs/opendustpan` (ADR 207 decision 7), which the launcher writes
     as stack config. The program does not hold a token.
   - (b) The launcher puts the job token in the Pulumi config of the step.

   *Recommendation: (a).* (b) puts a credential in a file that the program
   can print, and a stack config is part of the stored plan. The docs page
   shows the profile block as the way for a job.

### Ports

The ports exist (`outputshare.Checker`, `Attachments`, `States`,
`stack.OutputCiphertexts`). The build adds:

- In `internal/outputsharesvc`, the job path of the `Checker`: it reads the
  job of the job-ID claim through a small port `JobReader` (the unit, the
  state, the trigger, the change order origin and the attachments of the
  spec), applies question 1, and denies a job that is not running.
- In `internal/api/tfe` and `internal/api/pulumi`, the reduced answers of
  ADR 273 decision 7 for a job token of another unit, and the decrypt check
  of decision 6.
- `graph.Edges.Record` with the evidence `job` at each read (ADR 153
  decision 4).

### End-to-end steps

ADR 213 and ADR 273 name the steps. Each runs in a job on a test runner
with the real CLI:

1. A Pulumi stack reference in a job reads a plain and a secret output of
   another stack, with a rule and with the grants. With no rule, the read
   fails. A decrypt of a ciphertext that is not an output fails with 403.
2. A `terraform_remote_state` data source in a job reads the outputs of
   another workspace. The state file that the job gets holds no resource.
3. The step of question 2 for `tfe_outputs`.
4. The step of question 3: a Pulumi program reads an OpenTofu output
   through the profile block.
5. The edge with the evidence `job` shows in the graph of the consumer.

### Security tests (ADR 250)

| Step | Test |
| --- | --- |
| Check at the boundary | A job token whose job-ID claim names a job of another namespace, a job that ended, and a job of another unit than the token. |
| Contain | The reduced Pulumi deployment for a producer with 10,000 resources holds only the root stack outputs. The reduced OpenTofu state holds no `resources`. |
| Limit | A decrypt batch of more than the batch limit is refused. A ciphertext of the producer's config (not an output) is refused. |
| Authorization at the point of use | A rule that a person removes during the job: the next read fails. A starter who loses `unit:read-outputs`: the next read fails. |
| Fail closed | An error of `JobReader`, of `States` or of the identity store denies and writes the audit entry in the producer namespace. |

### Work items

| ID | Item | Size | Conflict group | Depends on |
| --- | --- | --- | --- | --- |
| OS-1 | The job path of the `Checker`, `JobReader`, `job.GrantsPrincipal`, conformance cases | M | outputshare | M5-1 |
| OS-2 | OpenTofu routes: the reduced workspace, state version and download answers, e2e step 2 | M | api-tfe | OS-1 |
| OS-3 | Pulumi routes: the reduced stack and export answers, the decrypt check, e2e step 1 | M | api-pulumi | OS-1 |
| OS-4 | The edge with the evidence `job`, e2e step 5 | S | graph | OS-2, OS-3 |
| OS-5 | `tfe_outputs` in a job (question 2) and the profile block path (question 3), with their e2e steps and the docs page section | M | api-tfe | OS-2, PF-7 of RFC 5 |

## Outcome

Accepted in part as [ADR 213](../adr/0213-job-tokens-no-change-plans-bulk-calls-forge-connections-and-locks.md)
decision 1 on 2026-10-18: a job reads the outputs of another unit on two
paths, each with two checks at each read. A read by the tool with the job
token needs a sharing rule that names the unit of the job and the grants of
the principal that started the job. A profile block `outputs/opendustpan`
needs the sharing rule and the `unit:read` of the authority of the profile
attachment. The server answers a job-token read with the root outputs only,
and records the edge with the evidence `job`. ADR 273 answers the other questions.

Accepted as
[ADR 273](../adr/0273-output-sharing-rules-and-the-read-outputs-action.md)
on 2026-10-22. The owner and the product manager decided the open questions
(15.1 to 15.7):

1. **15.1:** a separate action `unit:read-outputs`, with sharing rules on the
   unit that produces the outputs.
2. **15.2:** a caller that can only read outputs decrypts only ciphertext that
   the root outputs of the latest checkpoint hold (answer 1 of "Secret
   outputs").
3. **15.3:** the action covers only the routes that read outputs. A stack
   reference and `terraform_remote_state` read a whole state, so on a
   workstation they need read state. The console states the limit.
4. **15.4:** a rule across namespaces names units or a collection of the other
   namespace, never a principal and never a whole namespace. The producer
   decides alone.
5. **15.5:** a job token reads only what a rule names, limited to the grants
   of the principal that started the job.
6. **15.6:** a namespace setting decides the rule of a new unit. The default
   is `nobody`.
7. **15.7:** OpenDustpan supports Pulumi reads of OpenTofu outputs where e2e
   passes. Typed output contracts get their own RFC.

The e2e run of 22 October 2026 found that no CLI version reads outputs only.
ADR 273 gives the routes of each version. The option "specific principals"
of the proposal is dropped: a principal reads outputs with a grant.
