---
rfc: 95
title: Day-two actions outside state, which change the world but not the state, and how OpenDustpan records, shows and gates them
status: draft
created: 2026-10-20
---

# RFC 95. Day-two actions outside state, which change the world but not the state, and how OpenDustpan records, shows and gates them

Status: draft. The owner has decided nothing in this RFC. No code exists for it. The
owner answers the questions at the end (ADR 15).

Read the facts first. On 2026-10-20, OpenTofu has no actions. The issue that
asks for them is open with the label `pending-decision`. So this RFC
recommends a design that waits for OpenTofu, a first slice that needs no
action support in any tool, and a spike that starts when an OpenTofu release
has actions.

## Problem

A **provider action** is an operation that a provider defines outside
create, read, update and delete. Examples: invoke a function, reboot an
instance, invalidate a cache. It changes the world. It does not change the
state. So the state history of a unit cannot show it, drift detection cannot
find it, and a rollback cannot undo it.

The theme issue (#1090) asks for this outcome: when a run invokes an action,
the plan shows it, the approver sees it, a gate can stop it, and the job
record and the audit log keep a full trace of it. A drift run never invokes
one.

This RFC asks:

1. What each supported tool has today, and what it records (facts).
2. What OpenDustpan can see of an action, for each tool and each kind of
   run, and what it cannot see.
3. How the server records an action invocation (section B).
4. How gates, approvals and manual locks control actions (section C).
5. Which lifecycle features do not apply (section D).
6. The layers for action data as untrusted input (section E).
7. The first slice and its e2e step or spike (section F).

**Words in this RFC.** The glossary word **action** is an authorization
action (ADR 17). So this RFC says **provider action** for the operation, and
**action invocation** for one run of one provider action. Copy keeps the
words of each tool. Question 1 asks for the glossary lines.

## What is decided already

- **ADR 12 and ADR 13.** Each tool keeps its own model in its own package.
  Shared code calls a stack or a workspace a unit, and never merges the tool
  models. A cross-tool record takes the tool, the namespace and the tool's
  own name, and each tool package writes it in the same transaction as its
  own writes.
- **ADR 17.** Every feature has a REST API. Every route declares its actions
  and objects.
- **ADR 100 decision 3.** A job has one of four operations: `plan`, `apply`,
  `destroy` or `refresh`. OpenTofu `refresh` is `apply -refresh-only`.
- **ADR 102.** A plan phase stores one stored plan with two parts:
  `plan-display` (`tofu show -json` or `pulumi preview --json`) and
  `plan-apply`. A gate can bind approvals to it. A stale plan never applies.
  The server keeps it 400 days by default (ADR 152).
- **ADR 134 and ADR 137.** The launcher starts each step as an
  argument list from the job spec, masks each secret of 4 bytes or more in
  each log, and runs in a sandbox of the machine coordinator.
- **ADR 194.** The review view puts each `replace` and each `delete` in
  "Needs attention".
- **ADR 224 and ADR 226.** Gates run at six decision points over typed fact
  documents. A missing fact is a typed value, and each built-in gate fails
  closed on it. A new field in a fact group is a new minor schema version
  (ADR 226 decision 2). `state.write` runs before a write from a person's own CLI or
  CI, and its `plan` group is always `available == false`.
- **ADR 213 decision 10.** A manual lock refuses each lock request of a tool
  for each unit that it covers. A phase that already runs runs to its end.
- **ADR 250.** Each security path has at least two layers, each with its own
  test, and each layer stops the attack alone. A plan, a log and each output
  of a tool are untrusted input.
- **ADR 134 decision 5.** A step is an argument list. The launcher merges the
  standard output and the standard error of a step into one log.

## Related proposals (not decided)

- **RFC 25** proposes drift runs as scheduled refresh-only jobs. **RFC 91**
  proposes noise rules and lists what a rule can never collapse. **RFC 93**
  covers the provider code itself (the supply chain). **RFC 94** names this
  theme as a later use of the CI shim.

## ADRs this RFC would amend

- **ADR 226 decision 4**, to add a fact group `actions` to `change.planned`
  and `change.apply` (C1).
- **ADR 226 decision 3**, to extend the save check of a gate (C1). A gate
  that reads `count` or `types` of the group `actions` must also read
  `complete`.
- **ADR 101 decision 7**, so that `job.ValidateSteps` refuses `TF_CLI_ARGS`
  and each `TF_CLI_ARGS_*` variable for a `refresh` job (D).
- **ADR 101 decision 8**, so that a `refresh` job takes no custom step (D).
  Today custom steps come from a blueprint or from the settings of a unit.
- **ADR 134 decision 5**, only if B2a option (a) needs a separate file
  descriptor for the `-json` stream, because decision 5 merges standard
  output and standard error into one log.
- **ADR 134 decision 6**, so that the launcher refuses `TF_CLI_ARGS` and
  each `TF_CLI_ARGS_*` variable at start of a `refresh` step (D). A new group is a new minor schema version, for
  example `change.planned/1.1` (ADR 226 decision 2).
- **ADR 194 decision 4**, to add each planned action invocation to "Needs
  attention" (C3).
- **ADR 100 decision 4**, so that a `refresh` job plans first (D). Today the
  `refresh` job of a runner has the one phase `apply`, and so it has no plan
  phase for a check. The proposal: the phases `plan` and `apply`, where the
  plan phase runs `plan -refresh-only -out` and the apply phase applies that
  plan. Question 9 asks for this.
- **ADR 100 decision 3**, only if the owner picks a fifth job operation
  `invoke` (F, question 4). The recommendation adds no operation in
  slice 1.

## Facts from primary sources

Checked on 2026-10-20, on the main branch of each repository and on the
release tags named below.

### OpenTofu

- **No release has actions.** The latest release is v1.13.1. The changelog
  of v1.13 and of the unreleased 1.14.0 on main has no actions feature.
  <https://github.com/opentofu/opentofu/blob/main/CHANGELOG.md>,
  <https://github.com/opentofu/opentofu/blob/v1.13/CHANGELOG.md>
- **Not accepted, not refused.** Issue 3309, "Support action blocks and
  action_trigger lifecycle properties", opened 2025-09-26, is open with the
  labels `enhancement`, `needs-community-input` and `pending-decision`. On
  2025-09-26, a contributor (`apparentlymart`, labelled Contributor) wrote
  that a working group looks at "a new OpenTofu-oriented provider
  protocol". The issue links commits of a third-party fork. They are
  not upstream work. The page folds 10 comments, so we did not read
  each comment. <https://github.com/opentofu/opentofu/issues/3309>
- **No RFC.** The `rfc/` directory of OpenTofu has no RFC for actions.
  <https://github.com/opentofu/opentofu/tree/main/rfc>
- **The protocol has the calls, the engine does not.** OpenTofu vendors
  plugin protocol 6.10. It defines `ActionSchema`, `rpc PlanAction`,
  `rpc InvokeAction` (a server stream) and `rpc ValidateActionConfig`.
  No code outside the generated stubs and the mocks calls them, and the
  configuration loader does not parse `action` or `action_trigger`.
  <https://github.com/opentofu/opentofu/blob/main/docs/plugin-protocol/tfplugin6.10.proto>
  (lines 410 to 413)

### The upstream design that issue 3309 asks OpenTofu to match

OpenDustpan does not support this tool. Its design is the most likely shape
of OpenTofu actions, so it is the shape that our parser and our e2e step may
need to handle.

- **Versions.** Terraform 1.14.0 (2025-11-19) added the `action` block. A
  resource triggers it with `action_trigger` in its `lifecycle` block, on
  `before_create`, `after_create`, `before_update` or `after_update`. The
  CLI runs one with `-invoke=action.<type>.<name>`. 1.16.0 (2026-08-26)
  added `before_destroy`, `after_destroy` and `on_failure` (`halt`, the
  default, `continue` or `taint`).
  <https://github.com/hashicorp/terraform/blob/v1.16/CHANGELOG.md>,
  <https://developer.hashicorp.com/terraform/language/invoke-actions>
- **State.** The docs say: "Actions do not affect resource state." The only
  effect on state is a taint, with `on_failure = taint`.
- **Plan JSON.** Two top-level keys: `action_invocations` and
  `deferred_action_invocations`. Each invocation has `address`, `type`,
  `name`, `config_values`, `config_sensitive`, `config_unknown`,
  `provider_name`, and either `lifecycle_action_trigger`
  (`triggering_resource_address`, `action_trigger_event`, `on_failure` and
  indexes) or `invoke_action_trigger`. With `-invoke -target` (v1.16) the
  invoke trigger also has `calling_resource_address`. The JSON format page does not
  document these keys yet.
  <https://github.com/hashicorp/terraform/blob/main/internal/command/jsonplan/action_invocations.go>
- **Streaming `-json` output.** Message types `planned_action_invocation`,
  `action_start`, `action_progress` (with a free-text `message`),
  `action_complete` and `action_errored` (with `error`). Each message names
  the action address, and its lifecycle trigger or `invoke`. The
  machine-readable UI page documents only `planned_action_invocation`.
  <https://github.com/hashicorp/terraform/blob/main/internal/command/views/json/message_types.go>,
  <https://github.com/hashicorp/terraform/blob/main/internal/command/views/json/hook.go>
- **Invoke is a refresh-only plan.** `-invoke` runs as a refresh-only plan
  with action targets. The engine refuses it in each other plan mode.
  `apply -invoke` takes the state lock, but `-lock=false` turns the lock
  off. It writes a state version only if the state changed. So a CLI
  invocation can leave no trace in the state or in the lock.
  <https://github.com/hashicorp/terraform/blob/main/internal/terraform/context_plan.go>
  (lines 300 to 306). This matters for drift runs (D). A lifecycle trigger
  needs a planned create, update or destroy, so a refresh-only plan without
  `-invoke` triggers nothing. We read this from the source and did not run
  it.

### The Pulumi CLI (v3.270.0)

The Pulumi CLI has no action concept. Four features run code with side
effects:

| Feature | Runs at preview? | In state | In engine events that reach the server |
| --- | --- | --- | --- |
| Provider function (invoke) | yes, if the handshake opted in (`HandshakeRequest.invoke_with_preview`). Then the request has `preview: true`, and the provider decides. | nothing | no event of its own |
| `command` provider resources (`command:local:Command`, `command:remote:Command`) | no. The provider skips create and update at preview. | the resource, with inputs, `stdout`, `stderr` | `resourcePreEvent` and `resOutputsEvent` of the resource, output as `diagnosticEvent` |
| Resource hooks (v3.182.0 and later) | only with `on_dry_run` | hook names only | none on success, and a `diagnosticEvent` "hook failed" on failure |
| `Call` (a method of a component) | not verified | nothing | no event of its own |

Sources: <https://github.com/pulumi/pulumi/blob/master/proto/pulumi/provider.proto>
(lines 275 and 276 for `invoke_with_preview`, lines 563 to 565 for `preview`),
<https://github.com/pulumi/pulumi-command/blob/master/provider/pkg/provider/local/commandController.go>
(lines 47 to 50, the skip at preview),
<https://github.com/pulumi/pulumi/blob/master/proto/pulumi/resource.proto>
(line 593), <https://www.pulumi.com/docs/iac/concepts/resources/options/hooks/>,
<https://github.com/pulumi/pulumi/blob/master/sdk/go/common/apitype/events.go>
(lines 340 to 368).

The CLI posts engine events to `.../update/{id}/events/batch`. It drops
internal events, progress events and untargeted resource events before it
sends. Each other event reaches the server, also diagnostics marked
`ephemeral`.
<https://github.com/pulumi/pulumi/blob/master/pkg/backend/httpstate/state.go>
(lines 373 to 376)

A `command` resource is the nearest Pulumi form of an action. It is a
resource: it is in the plan as a step with a type, in the state, and in the
history of the stack. It runs on create, on update and on a change of
`triggers`. A hook and an invoke are not in the plan, not in the state and
not in the history.

`pulumi refresh`, also with `--run-program`, runs no `command` create or
update and no hook on a custom resource, as we read the docs and the code.
A hook on a component under `refresh --run-program` is not ruled out. We did
not run it (question 7).

## What OpenDustpan can see today

| Run | OpenTofu | Pulumi CLI |
| --- | --- | --- |
| A runner job (ADR 101) | Nothing about actions, because OpenTofu has none. When it has them: `plan-display` holds the planned invocations, and the launcher can keep the `-json` stream of the apply step. | `plan-display` holds each `command` resource step. Hooks and invokes: nothing, so the coverage is partial. The engine events of the apply reach the server. |
| A person's own CLI or CI | Only the lock and the state versions. The TFE-compatible API has no run routes, and the CLI sends no plan and no log. So the server sees no action at all. | The engine events: `command` resource steps, their output, and hook failures. No plan before the update: `state.write` has `plan.available == false`. |

So the server can **gate** actions only in a runner job, and can **record**
them only in a runner job (both tools) or from Pulumi engine events. For a
CLI-driven OpenTofu run, the job record and the audit log can hold nothing.
The docs must say this in plain words (question 6).

## Options and recommendations

### A. One tool-neutral concept, or one for each tool

- **A1.** No core concept. Each tool package shows its own data.
- **A2.** A core record **action invocation**, with a `tool` field and the
  tool's own words in its payload. The OpenTofu package fills it from
  `action_invocations` and the `action_*` messages. The Pulumi package
  fills it from `command` resource steps only. Hooks and invokes stay out,
  because the server cannot see them. The coverage gap is part of the data
  (C1).
- **A3.** A2, plus Pulumi hooks and invokes from diagnostics.

**Recommendation: A1 now, and decide A after the spike.** Slice 1 (F) needs
no core record. The shape of an OpenTofu action is not known until a release
has it, and a core record written first would copy the shape of Terraform.
If the owner wants the record now, A2 is the choice. The record holds what both tools share: a unit, a
job, an address, a kind, a trigger and a result. It does not merge the tool
models (ADR 13): each tool keeps its own payload. Tool fields that exist
only for one tool (`onFailure`, the lifecycle trigger and its event) go in
the tool payload, not in the core fields (ADR 12). A3 records failures only,
which gives a false picture.

### B. The record

**B1. Fields** (proposal). ID with the prefix `aci-`. `unit` (a unit
reference with the stable unit ID), `job`, `phase`, `plan` (the stored plan
ID), `stateVersionRead` (from the plan), `stateVersionWritten` (the state
version of the same phase. It can be absent: an action can leave no state
version, and a CLI invoke can leave no trace). From the tool: `tool`, `address`,
`actionType`, `provider`, `payload` (the tool's own words: for OpenTofu the
`trigger`, which is `invoke` or `lifecycle` with the triggering resource and
event, and `onFailure`), `configDigest` (SHA-256 of the
canonical configuration JSON, never the values). Result: `state`
(`planned`, `started`, `completed`, `errored`, `skipped`, `not-reported`),
`startedAt`, `endedAt`, `error` (at most 4 KiB, masked), and `progress` as a
log reference, not as rows.

**B2. Planned and observed.** The plan phase writes one row for each planned
invocation with `state: planned`. The apply phase moves each row with the
`action_*` messages. A planned invocation with no message at the end of the
phase gets `not-reported`, never `completed`. An observed invocation that
the plan did not list gets a row with the flag `unplanned`, and the phase
fails (E, fail closed).

**B2a. Who guarantees a stream that a machine can read.** The launcher merges
the standard output and the standard error of a step (ADR 134 decision 5).
So the `-json` stream shares one channel with the unit code and the provider
output, and either of them can print a line that looks like `action_complete`.
The server must not trust such a line alone. Options: (a) the server builds
the apply step with a fixed argument list, and the launcher keeps the `-json`
stream on a separate file descriptor that only the tool writes. Or (b): no
such channel, so each invocation stays `not-reported` and the plan is the only
record. **Recommendation: (a). The spike checks that the tool writes its
stream to a channel that user code cannot reach.** Until then, a row never
moves to `completed` from a merged log.

**B3. Audit.** One audit entry for each invocation that starts, with the
principal of the job, the unit, the address and the result. Not one entry
for each progress message. The audit log keeps its own retention (ADR 68).

**B4. Retention, backup, redaction.** The rows follow the history policy of
job artifacts (ADR 152), as the stored plan does. The table goes in the
table list of the backup domain. A redaction of a plan or a log (ADR 151)
also redacts the `error` text of its rows.

**B5. Reads.** `GET /api/units/.../action-invocations` and a filter on the
job record. Action names (proposal): `job:read` for the metadata,
`job:read-logs` for the progress log (ADR 152 decision 10), because the
log holds free text of the provider, as a job log does. The `error` field
comes from the log, so it follows the log: **recommendation: `job:read-logs`
for `error` too.** A reader of plans only then does not see `error`. The action names must be checked against ADR 210 when
an ADR records them.

### C. Control

**C1. A fact group `actions`** at `change.planned` and `change.apply`
(proposal, a new minor schema version): `available`, `complete`, `count`,
`types` (a set of `actionType`), `triggers` (a set of `invoke` and lifecycle
events), `providers`.

The group must tell "known none" from "unknown":

- **Known none.** A strict parser reads a known plan format. The plan has no
  action keys. Then `available` is `true`, `complete` is `true` and `count`
  is `0`. Else the gate fires on every OpenTofu plan.
- **Unknown.** The plan has a top-level key that the parser does not know,
  or the parser fails. Then `available` is `false`.
- **Partial coverage.** For the Pulumi CLI, the server sees `command`
  resource steps only. Hooks and invokes stay invisible. So the group has
  `complete == false` for Pulumi, and the docs say so.

A gate that reads `count` or `types` without `complete` could read
`count == 0` as "no side effect" for Pulumi. **Recommendation:** extend the
save check of ADR 226 decision 3, so that the server refuses to save a gate
that reads `count` or `types` of the group `actions` and does not also read
`complete`. This amends ADR 226 decision 3. The alternative is that Pulumi
gives `available == false`. Then each built-in gate that fails closed on a
missing fact would fire on every Pulumi plan, so we do not recommend it.

A gate "an action of type X in collection prod needs an approval" then reads:

```cedar
context.actions.available == false || context.actions.types.contains("example_reboot_instance")
```

For the Pulumi CLI, `plan.resourceTypes` (ADR 226 decision 4) has the types
of `command` resource steps already, so a gate on `command:remote:Command`
needs no new fact. A Pulumi slice that fills `actions.types` adds nothing
over `plan.resourceTypes`, and so this RFC has no Pulumi slice.
**Recommendation:** wait for the Pulumi part, and use `plan.resourceTypes`
for the `command` gates. A namespace list of "action resource types" adds
settings and no power.

**C2. Never hidden.** A noise rule (RFC 91) can never collapse a planned
action invocation, and the reader ignores a match on one. RFC 91 covers
this case in its list "What a rule can never collapse" only after C3 amends
ADR 194 to put each planned invocation in "Needs attention". Until then, the
list has no entry for an invocation. We keep C2 as a named defense-in-depth layer. Its two
parts each stop the attack alone: the save check refuses a rule that names
an invocation, and the render ignores a match on one.

**C3. Needs attention.** Each planned invocation is in "Needs attention"
(ADR 194). The approve control shows only on a view that lists each
planned invocation. A view that could not read them (the parser failed, the
plan is too large) shows "actions unknown" and no approve control.

**C4. Locks.** What a lock can and cannot do:

- A runner job checks the manual lock at dispatch (ADR 213 decision 10). A
  phase that already runs runs to its end. So a lock that a person sets
  after the dispatch does not stop an action in that phase.
- A CLI-driven `apply -invoke` takes the state lock, so a manual lock
  refuses its lock request. But `-lock=false` skips the lock, and then the
  manual lock does nothing. The failover fence (not the manual lock) refuses
  a state write only. An invoke that changes nothing writes no state, so the
  fence refuses nothing and the action has already run.
- **The server cannot gate a CLI-driven invocation.** It can refuse the
  lock request of a person who does not use `-lock=false`, and nothing
  else. The docs must say this in plain words.

The spike (F) must prove the lock request with the real CLI.

**C5. Allowed actions.** Options: (a) gates only, with C1, or (b) a namespace
allowlist of action types that the launcher also enforces. **Recommendation:
(a).** A gate is the one policy tool (ADR 224), and the launcher already
fails closed (E). An allowlist in two places drifts.

### D. Limits, stated plainly

- A provider action is not in state. (A Pulumi `command` resource is in the
  state and in the history, but what its command did is not.) Drift detection cannot find its effect.
  A rollback of state does not undo it. A destroy does not undo it. A
  `before_destroy` trigger runs a new action, it does not undo an old one.
- The provider decides idempotency and retries. OpenDustpan never retries an
  invocation, also not after a takeover of a phase. A takeover marks each
  started row with no end `not-reported`.
- OpenDustpan records what the tool reports. It cannot prove that the
  provider did what it reported.
- A drift run (RFC 25) must invoke nothing. A denylist of arguments is not
  enough (ADR 250 asks for an allowlist). `TF_CLI_ARGS` and
  `TF_CLI_ARGS_apply` in the step environment, a custom step of a team
  (ADR 101 decision 8) and a wrapper binary can add `-invoke` after any
  check of the argument list. Each vector below meets the layers that stop
  it:

  | Vector | Fixed steps | Clean environment | Plan check |
  | --- | --- | --- | --- |
  | An argument list with `-invoke` | Stops it: the spec check refuses the argument. | Does not apply. | Stops it: the plan holds an invocation. |
  | `TF_CLI_ARGS` or `TF_CLI_ARGS_apply` | Does not apply. | Stops it: the spec check and the launcher refuse the variable. | Stops it only if the plan step reads the variable. The apply step applies a saved plan, and the spike must prove that it ignores `-invoke` from the variable. |
  | A custom step | Stops it: a `refresh` job takes no custom step. | Does not apply. | Stops it only if the custom step is not the apply step. |
  | A wrapper binary in the runner image | No effect: the step names the binary. | No effect: the binary sets its own environment. | Stops it only if the plan shows the invocation. A wrapper that acts after the plan check is outside the server's reach. |

  The runner image, and any wrapper binary in it, are outside the server's
  reach. The team owns the image. So no layer stops a wrapper binary alone,
  and the docs must say this in plain words. The three layers are:

  1. **Fixed steps.** The server builds the steps of a `refresh` job from
     the step planner of the tool, with a fixed argument list from an
     allowlist. A `refresh` job takes no custom step. The spec check
     refuses a spec with any other argument.
  2. **A clean environment.** The spec check refuses `TF_CLI_ARGS` and each
     `TF_CLI_ARGS_*` variable in the step environment of a `refresh` job,
     and the launcher refuses them at start.
  3. **A plan check.** A `refresh` job plans first: the plan phase runs
     `plan -refresh-only -out`, the server reads the plan with the strict
     parser (E), the job fails when the plan has a planned invocation, and
     the apply phase applies that stored plan only. This layer does not
     depend on the argument list or on the environment. It needs the
     amendment of ADR 100 decision 4 (question 9). Without it, a drift run
     has no independent layer, because its one phase is `apply`.
  For the Pulumi CLI, `pulumi refresh` runs no hook and no `command` create
  or update on a custom resource, as we read the docs. A component hook
  under `refresh --run-program` is not ruled out. Question 7 asks for an e2e
  step that proves both.

### E. Defense in depth (ADR 250)

Treat action data as untrusted input: the plan JSON, each `-json` message and each
Pulumi engine event come from the tool, its providers and the code of the
unit.

| Layer | What it does | Its test |
| --- | --- | --- |
| Check at the boundary | The parser reads `action_invocations` and the `action_*` messages with a strict schema. It accepts known message types, known trigger events, `onFailure` values and the fields `invoke_action_trigger`, `lifecycle_action_trigger` and `calling_resource_address` from the allowlist only. An unknown top-level key gives `available == false`, never "none". Each address must match the address grammar of the tool. A plan holds at most 1,000 invocations. Each string is at most 4 KiB of strict UTF-8. The parser refuses a plan or a message that fails, and never fixes it. | a plan with each bad field and with an unknown top-level key, sent to the parser with the other layers skipped. A plan with no action keys gives `available == true` and `count == 0` |
| Encode or contain | The server never stores `config_values`, only `configDigest`. The server deletes CR, LF and control characters from each text before it writes a log or an audit entry. The launcher masks each secret in `error` and `progress`. SQL uses placeholders only. | a `message` with CR, LF, a secret of the job and a SQL character |
| Limit the effect | Size limits as above. A progress log has the size limit of a job log. The fact group holds types, not values. A drift job has fixed steps, a clean environment and a plan check (D). | a plan with 1,001 invocations, a progress stream above the log limit, a drift job with `TF_CLI_ARGS=-invoke=...` |
| Authorize at use | Each read checks `job:read` or `job:read-plans` on the unit of the row. A runner writes rows only for the job of its claim. | a job token of unit A that writes a row of unit B |
| Fail closed and record | A parse error or an unknown key sets `actions.available` to `false`, and each built-in gate fails closed on it. A line in the merged log never sets a row to `completed` (B2a). An unplanned invocation fails the phase. Each refusal writes an audit entry. | a plan that the parser refuses: the gate denies and an audit entry exists |

### F. The first slice

**Persona.** An operator runs OpenTofu on OpenDustpan runners. The team
plans to use a provider action that reboots instances. The operator wants
an approval before each plan that reboots an instance in collection `prod`.

**What can ship before OpenTofu has actions** (slice 1, recommendation):

Slice 1 is **no code**:

1. Land the glossary terms when the RFC is accepted.
2. Fold the drift-run allowlist of D (fixed steps, a clean environment, a
   plan check) into RFC 25, because it is a rule of drift runs and not of
   actions.

If the owner wants code in slice 1, it is only the parser of C1 that tells
"known none" from "unknown", with the tests of E. A fact group with
`available` always `false` does not ship: each gate that reads it would fail
on every plan.

There is no e2e step for an action invocation, because no supported tool
release has one. CLAUDE.md says that every endpoint lands with its e2e step
first, so the record (B) and the full fact group (C1) wait.

**Spike plan, when an OpenTofu release or prerelease has actions:**

1. Add the version to the e2e matrix. Write a test provider with one action
   (an action that writes a file in the test directory), built from the
   protocol 6.10 calls.
2. Record `tofu show -json` and `tofu apply -json` for: an `invoke`, each
   lifecycle event, each `on_failure` value, a failed action and a deferred
   action. Compare the shapes with the upstream design above, and write the
   differences into this RFC.
3. Prove C4: a manual lock refuses the lock request of `tofu apply
   -invoke=...` from a person's own CLI. Show that `-lock=false` skips it,
   and that no state version exists after an invoke that changed nothing.
4. Prove that `apply -refresh-only` without `-invoke` triggers nothing.
   Prove that `tofu apply <saved plan>` rejects or ignores `-invoke` from
   `TF_CLI_ARGS_apply`.
5. Then land B and C1 with their e2e steps, and the persona's gate.

**Pulumi.** `plan.resourceTypes` (ADR 226 decision 4) carries the `command`
resource types, so a gate on them needs no new fact. This RFC has no Pulumi
slice. Hooks and invokes stay invisible.

## Open questions

The owner keeps these. Each recommendation is the author's.

1. The words: "provider action" and "action invocation" (`aci-`) in the
   glossary, because "action" is the authorization action? Recommendation:
   yes, and keep the prefix `aci-`.
2. One tool-neutral record (A2) or no core concept (A1)? Recommendation: A1
   now, and decide after the spike.
3. Wait for OpenTofu, or ship a Pulumi part first? Recommendation: wait.
   `plan.resourceTypes` covers the `command` gates today. Slice 1 is no code.
4. A fifth job operation `invoke` for an operation run of `-invoke`, with
   the approvals and the isolation of an apply? Recommendation: not now.
   Decide after the spike, when the real shape is known.
5. Does a plan with a planned invocation turn off auto-apply by default,
   as a warning does (ADR 229 decision 3)? Recommendation: yes, a built-in gate with
   outcome `warn` that a namespace can set to `enforce`.
6. CLI-driven OpenTofu runs show no action to the server, and `-lock=false`
   skips the manual lock (C4). Is a plan upload from the CI shim (RFC 94) the
   answer, or do the docs only state the limit? Recommendation: state the
   limit now, with `-lock=false` and the missing state version. Add the shim
   upload later, as advisory only, because the CLI user can skip it.
7. Does `pulumi refresh`, also with `--run-program`, run a hook or a
   `command` create? Recommendation: an e2e step that proves it, and that
   also covers a component hook under `refresh --run-program`, before any
   Pulumi drift run ships.
8. Does an unplanned invocation (B2) fail the phase, or only mark the row?
   Recommendation: fail the phase. The apply already ran the action, so the
   failure stops the next phases and tells a person.
9. Amend ADR 100 decision 4 so that a `refresh` job plans first (D, layer 3)?
   Recommendation: yes. Another independent layer would need a second source
   of truth about the plan, and none exists.

## Proposed glossary lines

- **Provider action**: an operation that a provider defines outside create,
  read, update and delete, such as a reboot. It does not change the state.
  _Avoid_: action (alone: that is an authorization action), operation,
  hook (a Pulumi word for code that runs around a resource step), invoke (a
  Pulumi word for a provider function, and the OpenTofu flag `-invoke`: say
  "invoke" only for the flag).
- **Action invocation**: one run of one provider action in one phase of a
  job, recorded with the ID prefix `aci-`. _Avoid_: action run, call.

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
