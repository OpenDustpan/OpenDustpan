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
- **ADR 133, ADR 134 and ADR 137.** The launcher starts each step as an
  argument list from the job spec, masks each secret of 4 bytes or more in
  each log, and runs in a sandbox of the machine coordinator.
- **ADR 194.** The review view puts each `replace` and each `delete` in
  "Needs attention".
- **ADR 224 and ADR 226.** Gates run at six decision points over typed fact
  documents. A missing fact is a typed value, and each built-in gate fails
  closed on it. `state.write` runs before a write from a person's own CLI or
  CI, and its `plan` group is always `available == false`.
- **ADR 213 decision 10.** A manual lock refuses each lock request of a tool
  for each unit that it covers.
- **ADR 250.** Each security path has at least two layers, each with its own
  test. A plan, a log and each output of a tool are untrusted input.
- **RFC 25** proposes drift runs as scheduled refresh-only jobs. **RFC 91**
  proposes noise rules and lists what a rule can never collapse. **RFC 93**
  covers the provider code itself (the supply chain). **RFC 94** names this
  theme as a later use of the CI shim.

## ADRs this RFC would amend

- **ADR 226 decision 4**, to add a fact group `actions` to `change.planned`
  and `change.apply` (C1). A new fact group needs a new schema version.
- **ADR 194 decision 4**, to add each planned action invocation to "Needs
  attention" (C3).
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
  labels `enhancement`, `needs-community-input` and `pending-decision`. A
  maintainer asked for use cases three times (the last on 2026-04-28), and
  said that a working group looks at "a new OpenTofu-oriented provider
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
  indexes) or `invoke_action_trigger`. The JSON format page does not
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
| Provider function (invoke) | yes. The request has `preview: true`, and the provider decides. | nothing | no event of its own |
| `command` provider resources (`command:local:Command`, `command:remote:Command`) | no (the `run` function does) | inputs, `stdout`, `stderr` | `resourcePreEvent` and `resOutputsEvent` of the resource, output as `diagnosticEvent` |
| Resource hooks (v3.182.0 and later) | only with `on_dry_run` | hook names only | none on success, and a `diagnosticEvent` "hook failed" on failure |
| `Call` (a method of a component) | not verified | nothing | no event of its own |

Sources: <https://github.com/pulumi/pulumi/blob/master/proto/pulumi/provider.proto>
(lines 275, 276 and 563 to 565),
<https://github.com/pulumi/pulumi-command/blob/master/provider/pkg/provider/local/commandController.go>
(lines 40 to 48),
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

A `command` resource is the nearest Pulumi form of an action: it is in the
plan as a resource step with a type, and it runs on create, on update and on
a change of `triggers`. A hook and an invoke are not visible in the plan.

## What OpenDustpan can see today

| Run | OpenTofu | Pulumi CLI |
| --- | --- | --- |
| A runner job (ADR 101) | Nothing about actions, because OpenTofu has none. When it has them: `plan-display` holds the planned invocations, and the launcher can keep the `-json` stream of the apply step. | `plan-display` holds each `command` resource step. Hooks and invokes: nothing. The engine events of the apply reach the server. |
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
  because the server cannot see them before they run.
- **A3.** A2, plus Pulumi hooks and invokes from diagnostics.

**Recommendation: A2.** The record holds what both tools share: a unit, a
job, an address, a kind, a trigger and a result. It does not merge the tool
models (ADR 13): each tool keeps its own payload. A3 records failures only,
which gives a false picture.

### B. The record

**B1. Fields** (proposal). ID with the prefix `aci-`. `unit` (a unit
reference with the stable unit ID), `job`, `phase`, `plan` (the stored plan
ID), `stateVersionRead` (from the plan), `stateVersionWritten` (the state
version of the same phase, or empty). From the tool: `tool`, `address`,
`actionType`, `provider`, `trigger` (`invoke`, or `lifecycle` with the
triggering resource and event), `onFailure`, `configDigest` (SHA-256 of the
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

**B3. Audit.** One audit entry for each invocation that starts, with the
principal of the job, the unit, the address and the result. Not one entry
for each progress message. The audit log keeps its own retention (ADR 68).

**B4. Retention, backup, redaction.** The rows follow the history policy of
job artifacts (ADR 152), as the stored plan does. The table goes in the
table list of the backup domain. A redaction of a plan or a log (ADR 151)
also redacts the `error` text of its rows.

**B5. Reads.** `GET /api/units/.../action-invocations` and a filter on the
job record. Action names (proposal): `job:read` for the metadata,
`job:read-plans` for `error` and the progress log, as for plan values
(ADR 194 decision 9). The action names must be checked against ADR 210 when
an ADR records them.

### C. Control

**C1. A fact group `actions`** at `change.planned` and `change.apply`
(proposal): `available`, `count`, `types` (a set of `actionType`),
`triggers` (a set of `invoke` and lifecycle events), `providers`.
`available` is `false` when the tool cannot say (a tool with no actions, a
plan that the parser could not read). A gate "an action of type X in
collection prod needs an approval" then reads:

```cedar
context.actions.available == false || context.actions.types.contains("example_reboot_instance")
```

For the Pulumi CLI, C1 fills `types` from `command` resource steps, as
`command:remote:Command`. Hooks and invokes stay outside, and the docs say
so. The alternative is a namespace list of "action resource types" (more
power, more settings). **Recommendation:** C1, with the fixed list of the
`command` provider types in slice 2, and no namespace list.

**C2. Never hidden.** A noise rule (RFC 91) can never collapse a planned
action invocation, and the reader ignores a match on one. Add it to the RFC
91 list "What a rule can never collapse". Two layers: the save check
refuses a rule that names an invocation, and the render ignores a match on
one.

**C3. Needs attention.** Each planned invocation is in "Needs attention"
(ADR 194). The approve control shows only on a view that lists each
planned invocation. A view that could not read them (the parser failed, the
plan is too large) shows "actions unknown" and no approve control.

**C4. Locks.** A manual lock refuses the lock request of the tool. An
OpenTofu `-invoke` is a refresh-only apply, so we expect it to lock the
state, and so a manual lock to cover it. The spike (F) must prove this with
the real CLI. A runner job checks the manual lock at dispatch (ADR 213), so
a job never invokes an action on a locked unit.

**C5. Allowed actions.** Options: (a) gates only, with C1, or (b) a namespace
allowlist of action types that the launcher also enforces. **Recommendation:
(a).** A gate is the one policy tool (ADR 224), and the launcher already
fails closed (E). An allowlist in two places drifts.

### D. Limits, stated plainly

- A provider action is not in state. Drift detection cannot find its effect.
  A rollback of state does not undo it. A destroy does not undo it. A
  `before_destroy` trigger runs a new action, it does not undo an old one.
- The provider decides idempotency and retries. OpenDustpan never retries an
  invocation, also not after a takeover of a phase. A takeover marks each
  started row with no end `not-reported`.
- OpenDustpan records what the tool reports. It cannot prove that the
  provider did what it reported.
- A drift run (RFC 25) must invoke nothing. Three layers, each one alone:
  1. The job spec of a `refresh` job has no `-invoke` argument. The spec
     check refuses one.
  2. The launcher refuses an argument list for a `refresh` step that holds
     `-invoke` in any form (also `-invoke=` and a response file).
  3. The plan phase of a `refresh` job fails when its `plan-display` has a
     planned invocation, before any apply phase.
  For the Pulumi CLI, `pulumi refresh` runs no hook and no `command`
  create or update, as we read the docs. Question 7 asks for an e2e step
  that proves it, also with `--run-program`.

### E. Defense in depth (ADR 250)

Treat action data as untrusted input: the plan JSON, each `-json` message and each
Pulumi engine event come from the tool, its providers and the code of the
unit.

| Layer | What it does | Its test |
| --- | --- | --- |
| Check at the boundary | The parser reads `action_invocations` and the `action_*` messages with a strict schema. It accepts known message types, known trigger events and `onFailure` values from the allowlist only. Each address must match the address grammar of the tool. A plan holds at most 1,000 invocations. Each string is at most 4 KiB of strict UTF-8. The parser refuses a plan or a message that fails, and never fixes it. | a plan with each bad field, sent to the parser with the other layers skipped |
| Encode or contain | The server never stores `config_values`, only `configDigest`. The server deletes CR, LF and control characters from each text before it writes a log or an audit entry. The launcher masks each secret in `error` and `progress`. SQL uses placeholders only. | a `message` with CR, LF, a secret of the job and a SQL character |
| Limit the effect | Size limits as above. A progress log has the size limit of a job log. The fact group holds types, not values. A drift job gets no `-invoke` (D). | a plan with 1,001 invocations, a progress stream above the log limit |
| Authorize at use | Each read checks `job:read` or `job:read-plans` on the unit of the row. A runner writes rows only for the job of its claim. | a job token of unit A that writes a row of unit B |
| Fail closed and record | A parse error sets `actions.available` to `false`, and each built-in gate fails closed on it. An unplanned invocation fails the phase. Each refusal writes an audit entry. | a plan that the parser refuses: the gate denies and an audit entry exists |

### F. The first slice

**Persona.** An operator runs OpenTofu on OpenDustpan runners. The team
plans to use a provider action that reboots instances. The operator wants
an approval before each plan that reboots an instance in collection `prod`.

**What can ship before OpenTofu has actions** (slice 1, recommendation):

1. The fact group `actions` with `available` only, always `false` for
   OpenTofu, and a guide page that says: "OpenTofu has no actions. A gate
   that reads `actions` fails closed until it does."
2. C2 and the drift layers of D, because both are cheap and protect the day
   that actions arrive.
3. The e2e step: a `refresh` job spec with `-invoke` is refused by layer 1,
   and the launcher refuses the same argument list by layer 2, with the
   real OpenTofu CLI of the e2e matrix (latest, 1.12.7, 1.11.14).

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
3. Prove C4: a manual lock refuses `tofu apply -invoke=...` from a person's
   own CLI.
4. Prove that `apply -refresh-only` without `-invoke` triggers nothing.
5. Then land B and C1 with their e2e steps, and the persona's gate.

**Pulumi.** The Pulumi part of C1 (`command` resource types) can come in
slice 2 with an e2e step against the real Pulumi CLI and the `command`
provider, if the owner wants Pulumi first (question 3).

## Open questions

The owner keeps these. Each recommendation is the author's.

1. The words: "provider action" and "action invocation" (`aci-`) in the
   glossary, because "action" is the authorization action? Recommendation:
   yes.
2. One tool-neutral record (A2) or no core concept (A1)? Recommendation: A2,
   with the Pulumi CLI limited to `command` resources.
3. Wait for OpenTofu, or ship the Pulumi part of C1 first (slice 2)?
   Recommendation: slice 1 now, and Pulumi `command` types next, because
   they are in the plan today.
4. A fifth job operation `invoke` for an operation run of `-invoke`, with
   the approvals and the isolation of an apply? Recommendation: not now.
   Decide after the spike, when the real shape is known.
5. Does a plan with a planned invocation turn off auto-apply by default,
   as a warning does (ADR 224)? Recommendation: yes, a built-in gate with
   outcome `warn` that a namespace can set to `enforce`.
6. CLI-driven OpenTofu runs show no action to the server. Is a plan upload
   from the CI shim (RFC 94) the answer, or do the docs only state the
   limit? Recommendation: state the limit now, and the shim later.
7. Does `pulumi refresh`, also with `--run-program`, run a hook or a
   `command` create? Recommendation: an e2e step that proves it before any
   Pulumi drift run ships.
8. Does an unplanned invocation (B2) fail the phase, or only mark the row?
   Recommendation: fail the phase. The apply already ran the action, so the
   failure stops the next phases and tells a person.

## Proposed glossary lines

- **Provider action**: an operation that a provider defines outside create,
  read, update and delete, such as a reboot. It does not change the state.
  _Avoid_: action (alone: that is an authorization action), operation,
  hook (that is a Pulumi word).
- **Action invocation**: one run of one provider action in one phase of a
  job, recorded with the ID prefix `aci-`. _Avoid_: action run, call.

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
