---
rfc: 15
title: Output sharing between units
status: draft
created: 2026-10-10
---

# RFC 15. Output sharing between units

Status: draft. The action names and the sharing model are proposals.

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

## Outcome

Accepted in part as [ADR 203](../adr/0203-job-tokens-no-change-plans-bulk-calls-forge-connections-and-locks.md)
decision 1 on 2026-10-18: a job token reads only the outputs whose sharing
rule names the unit of the job, intersected with the grants of the principal
that started the job. The other questions stay open.
