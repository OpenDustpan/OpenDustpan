# 260. Provider actions wait for a supported tool that has them

Status: accepted · 2026-10-20. Decided by the product manager under the owner's delegation of 20 October 2026. Accepts part of RFC 95 (question 3 and slice 1)

## Context

Decided by the product manager under the owner's delegation of 20 October
2026, with the owner's decision rule.

[RFC 95](../rfc/0095-day-two-actions-outside-state.md) asks how OpenDustpan
records, shows and gates a provider action: an operation of a provider
outside create, read, update and delete, which changes the world and not
the state. Its question 3 asks: "Wait for OpenTofu, or ship a Pulumi part
first?" Its recommendation is to wait, with a slice 1 that is no code.

The facts on 2026-10-20 (RFC 95, "Facts from primary sources", and a new
check of the change logs):

- **OpenTofu.** No release has actions. Issue 3309 ("Support action blocks
  and action_trigger lifecycle properties") is open with the label
  `pending-decision`. OpenTofu vendors the plugin protocol calls of
  actions, but its engine does not call them.
- **Terraform CLI.** Version 1.14.0 added the `action` block. OpenDustpan
  does not support this tool (RFC 53), and CLAUDE.md supports a tool only
  when the e2e suite drives its real CLI.
- **Pulumi CLI.** RFC 95 read v3.270.0 and found no action concept. The
  change log has no feature with the name "actions". The nearest new
  feature is `pulumi do`: v3.242.0 (2026-05-19, pull request 23176 of
  `pulumi/pulumi`) added it to run provider functions without state and
  print JSON, and a reviewer called it experimental. v3.243.0 added
  resources, v3.246.0 added `--stateless`, and announced that stateful becomes the
  default. v3.254.0 added stateful create, delete and `upsert`. We read
  the CLI source (`pkg/cmd/pulumi/do/do_resource_upsert.go`, lines 845 to
  873). A stateful `pulumi do` runs a normal `backend.UpdateStack`, or
  `PreviewStack` with `--dry-run`. So it sends `begin-update`, engine events
  and checkpoints to the backend. The checkpoint holds a new `snippets`
  section, and the deployment declares the feature `snippets`
  (`sdk/go/common/apitype/core.go`, lines 167 to 171). On `master`,
  stateful is the default (`do.go`, line 486). A stateless `pulumi do`
  sends nothing to the backend. A
  `command` resource is the nearest form of an action inside an update,
  and `plan.resourceTypes` (ADR 226 decision 4) already holds its type, so
  a gate can stop it today.

CLAUDE.md says that every endpoint lands with its e2e step first. No
supported tool can make an action invocation for that step.

## Decision

1. **Wait.** OpenDustpan builds no record, fact, gate or route for
   provider actions until a release of a supported tool has them. This
   answers RFC 95 question 3.
2. **Slice 1 is no code.** It is the two steps of RFC 95 section F: the
   glossary lines when the owner accepts the RFC, and the move of the drift-run
   allowlist into RFC 25.
3. **The trigger of the spike.** The spike plan of RFC 95 starts with the
   first OpenTofu release or prerelease that has actions. For the Pulumi
   CLI, a stateful `pulumi do` already reaches the update routes today.
   An e2e step on the latest CLI checks that the checkpoint path accepts
   a deployment with the feature `snippets`. The result goes into RFC 95.
4. **The limit is in the docs.** The user documentation of runs says
   that OpenDustpan does not see a provider action, and that a `command`
   resource of the Pulumi CLI is visible to a gate through its resource
   type.
5. **The other questions of RFC 95 stay open.** Questions 1, 2 and 4 to 9
   wait for the spike, or for the owner.

Benchmark: AWS and Google Cloud add a control for a new API only after the
API exists, and map it from the real API. A design copied from a tool that
OpenDustpan does not support could fix the wrong shape.

## Consequences

- A team that uses a `command` resource of the Pulumi CLI can stop it with
  a gate on `plan.resourceTypes` today.
- A team that uses actions of the Terraform CLI gets no support, as for
  each other feature of that CLI.
- When OpenTofu ships actions, the spike of RFC 95 runs first, and an ADR
  follows it.
