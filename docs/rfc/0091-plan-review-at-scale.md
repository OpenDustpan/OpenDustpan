---
rfc: 91
title: Plan review at scale, with one view of repeated changes across stacks and workspaces, and noise rules that expire
status: discussing
created: 2026-10-20
---

# RFC 91. Plan review at scale, with one view of repeated changes across stacks and workspaces, and noise rules that expire

Status: discussing. Design only. Nothing in this RFC is decided. No code
exists for it. The research pass of 7 November 2026 adds the facts from
research, sections J to O (risk classes, which facts are verified, review
by risk, acknowledged changes, the delta since the last plan seen, and the
new writes with their agent classes), the recommended
design, the one-way doors, the slices and the questions for the owner.

## Problem

A large change gives a plan of thousands of lines. Examples: a new common
tag on every resource in every environment, or a plan over about 70
modules in each environment. Most of the lines are noise:

- the same diff in each environment,
- the same diff on each `count` or `for_each` instance,
- a tag with a timestamp that changes on each plan,
- a set that looks fully changed because its members came back in another
  order,
- one diagnostic that repeats for each orphaned resource.

A search for "must be replaced" misses a destroy that is not a
replacement. Teams build local tools for this. A local tool must rebuild
its context each time. The server already holds the units, the stored
plans, the state version that each plan read, the change orders and the
approvals.

The outcome that this RFC asks for: a reviewer sees the few changes that
matter first. The approval binds to the plan that the reviewer saw. The
apply runs exactly that stored plan.

## What is decided already

This RFC builds on these decisions. It does not change them.

- **Stored plans** (ADR 102). A plan phase stores one plan with two parts:
  `plan-display` (`tofu show -json` or `pulumi preview --json`) and
  `plan-apply` (the OpenTofu plan file, or the Pulumi update plan of
  `--save-plan`). Each part has its SHA-256. `job.CheckPlan` refuses a
  stale, old or redacted plan at the dispatch. The runner refuses a part
  with another hash. Masked reads need `job:read-plans`. Unmasked reads
  also need `unit:read-secrets` and write a `reveal` audit entry.
- **Change orders** (ADR 190). One namespace, 1 to 100 units in each plan
  revision. A plan job for each unit, an apply job for each unit of the
  approved revision. The apply job names its stored plan
  (`job.Spec.Plan`).
- **Signatures bind to a digest** (ADR 191). The digest `cho-digest/1`
  covers, for each unit plan, the plan ID, the two part hashes, the state
  version and the commit. A signature names the digest that the approver
  saw. Another digest fails with `ErrPlanChanged`. Signature carry-over
  (ADR 225 decision 12) is `off` by default.
- **The review view** (ADR 194). Cause diff first. Each `replace` and each
  `delete` is in "Needs attention", one by one, never in a group. Identical
  in-place changes form a change group (`cog-` content key, HMAC-SHA-256
  with a key of the namespace). A change that touches a secret path is
  never in a change group. Each tool package implements
  `changeorder.PlanReader` (ADR 196 decision 4). An unreadable plan blocks
  approval with `ErrSummaryUnreadable`.
- **Gates and facts** (ADRs 224, 226). `change.planned` reads the `plan`
  fact group (`add`, `change`, `replace`, `destroy`, `resourceTypes`).
  `change.apply` checks again before each phase that changes.
- **Collections** (ADR 51). A named set of units in one namespace. Each unit
  is in exactly one collection. A collection can be the scope of a gate.

This RFC adds, on top: set and diagnostic handling, noise rules, the
binding of a signature to the view that the signer saw, and a later
view of similar changes.

## ADRs this RFC would amend

This RFC changes some decisions. If the owner accepts it, an ADR must
record each change below. A change that the owner does not want is
dropped from the RFC.

- **ADR 102 decision 2.** A stored plan gets one new optional part,
  `plan-diagnostics` (section D). With option C1 it gets a second one,
  `provider-schema`.
- **ADR 191.** A signature also holds a `viewBasis`. The approve request
  names it. A new error, `ErrViewChanged`, replaces `ErrPlanChanged` when
  only the view basis is stale (section F).
- **ADR 194 decision 1.** The order of the sections gets one new section,
  the error diagnostics, after "Needs attention" (section A).
- **ADR 194 decision 3.** The change kinds get `forget`, `import` and
  `refresh`. A resource change gets the field `movedFrom` and the flag
  `importing` (section A).
- **ADR 194 decision 4.** "Needs attention" also holds each `forget`, each
  `import` and each move.
- **ADR 225 decision 12.** A carried signature also keeps the view basis of
  the signature that it carries from (section F, **recommendation**).
- **ADR 226 decision 4.** `change.planned` and `change.apply` get the group
  `risk` in the minor version `1.1` (section J).
- **ADR 191.** A signature also holds the change keys that the signer
  acknowledged (section M). A new error, `ErrAcknowledgeMismatch`.
- **ADR 299 decision 10.** The record of what each approver saw also holds
  the change keys. An acknowledged change whose key changed stops the
  apply (section N).
- **ADR 225 decision 4.** The `unless` of an approval default cannot read a
  fact group of the origin `runner` to cancel the default (section K).

**Dependencies on RFCs that are not accepted.** RFC 122 (gates at every
step of an untrusted plan) and RFC 121 (agent principals) are open
proposals, in pull requests #2565 and #2563. This RFC uses two of their
proposals: the origin of each fact group (RFC 122) and the confirmation
page of an agent change (RFC 121 decision 25). Each place that uses them
says "as RFC 122 proposes" or "as RFC 121 proposes". Sections J to N do
not depend on them for safety: section K gives its own rule for facts
that the server did not compute, with or without RFC 122. If RFC 122 is
accepted, this RFC also asks that its rule for an unverified fact covers
the `unless` of an approval default.

The resume of a multi-unit apply (ADR 190 decision 8) is not in this
list. It goes to its own RFC (question 5, item 8). A resume in the same
revision must rest on a fact that the server recorded (the apply job
ended before the runner started the apply step), and it must run
`job.CheckPlan`, whose guard `ErrPlanStale` refuses a plan that read
another state version than the current one.

## Facts from primary sources

Checked on 2026-10-20.

### OpenTofu plan JSON (`tofu show -json`)

Source: <https://opentofu.org/docs/internals/json-format/>.

- `format_version` is `"1.0"`. The top level has `resource_changes`,
  `resource_drift`, `output_changes`, `relevant_attributes`, `checks`,
  `errored`, `prior_state`, `configuration`, `planned_values`, `variables`
  and `timestamp`.
- A resource change has `address`, `previous_address` (only when the
  address changed, for example by a `moved` block), `module_address`,
  `mode`, `type`, `name`, `index`, `deposed` and `action_reason`.
- `change.actions` is one of `["no-op"]`, `["create"]`, `["read"]`,
  `["update"]`, `["delete"]` and `["forget"]`, or a replace:
  `["delete", "create"]` or `["create", "delete"]`. The docs say that this
  form lets a scan for `delete` catch each deletion.
- `replace_paths` lists the attribute paths that forced the replace. The
  page says it is omitted when the action is not a replace, "or if no paths
  caused the replacement (for example, if the resource was tainted)". The
  page does not say it for `replace_by_request`, so the reader never relies
  on the presence or the absence of `replace_paths`.
- `importing` is an object in `change` (for example `{"id": "foo"}`). The
  page says: "If importing is present (ie. not null) then the change is an
  import operation in addition to anything mentioned in the actions
  field". So an import is not an action. A planned import can have the
  actions `["no-op"]` or `["update"]`. A `generated_config` string can
  come with it.
- `action_reason` values include `replace_because_tainted`,
  `replace_because_cannot_update`, `replace_by_request`,
  `replace_by_triggers`, `delete_because_no_resource_config`,
  `delete_because_count_index`, `delete_because_each_key` and
  `read_because_config_unknown`. The docs say these are hints only, and
  a consumer must treat an unknown value as unspecified.
- **Unknowns.** `after_unknown` has the shape of `after`, with `true` at
  each unknown leaf.
- **Sensitive values.** `before_sensitive` and `after_sensitive` have
  `true` at each sensitive leaf. The docs say a consumer must combine them
  with "before" and "after", so that no sensitive value leaks by accident.
  So the values are in `before` and `after`, and masking is the job of the
  reader (ADR 41).
- **Sets.** "lists, sets, and tuples all lower to JSON arrays". The docs
  say nothing about the order of set members. The plan JSON alone does not
  tell a set from a list. The provider schema does: a block has a
  `nesting_mode` of `single`, `list`, `set` or `map`
  (<https://opentofu.org/docs/cli/commands/providers/schema/>).
- **Diagnostics.** The plan JSON has no diagnostics. It has only the flag
  `errored`. Diagnostics are in the machine-readable stream of
  `tofu plan -json`, one JSON message for each line, with the type
  `diagnostic` (<https://opentofu.org/docs/internals/machine-readable-ui/>).
  The same stream has `planned_change` with an `action` and a `reason`.
  A diagnostic has `severity` (`error` or `warning`), `summary`, `detail`,
  an optional `range` (`filename`, `start` and `end` with `line`, `column`
  and `byte`) and an optional `snippet` of the source. The same page gives this shape.

### Pulumi CLI preview JSON (`pulumi preview --json`)

Sources: <https://www.pulumi.com/docs/iac/cli/commands/pulumi_preview/>,
and the source of `pulumi/pulumi` on `master`, read on 2026-10-20:
`pkg/backend/display/json.go`, `sdk/go/common/apitype/events.go`,
`sdk/go/common/apitype/history.go`, `sdk/go/common/apitype/core.go` and
`pkg/resource/stack/deployment.go`.

- `--json` "Serialize[s] the preview diffs, operations, and overall output
  as JSON". With `PULUMI_ENABLE_STREAMING_JSON_PREVIEW` set, it streams
  engine events instead.
- **The summary document** (`PreviewDigest`) has `config` (secrets
  blinded), `steps`, `diagnostics`, `duration`, `changeSummary` and
  `maybeCorrupt`.
- **A step** (`PreviewStep`) has `op`, `urn`, `provider`, `oldState`,
  `newState`, `diffReasons` (the keys that cause a diff), `replaceReasons`
  (the keys that cause a replacement) and `detailedDiff` (a map from a
  property path to a `kind` and `inputDiff`).
- **Operations** (`OpType`): `same`, `create`, `update`, `delete`,
  `replace`, `create-replacement`, `delete-replaced`, `read`,
  `read-replacement`, `refresh`, `discard`, `discard-replaced`,
  `remove-pending-replace`, `import` and `import-replacement`.
- **Diff kinds** in the engine events: `add`, `add-replace`, `delete`,
  `delete-replace`, `update` and `update-replace`. So a property that
  forces a replacement has its own kind.
- **Unknowns.** A computed value serializes as the string
  `04da6b54-80e4-46f7-96ec-b56ff0331ba9` (`computedValuePlaceholder`).
- **Secrets.** `MassageSecrets` in `pkg/backend/display/json.go` replaces
  each secret property with the string `[secret]` when `showSecrets` is
  false. The flag `--show-secrets` of the CLI sets it to true, so the
  runner must never pass it (section G).
- **Diagnostics** (`PreviewDiagnostic`): `urn`, `prefix`, `message` and
  `severity`. There is no source location. The streaming
  `DiagnosticEvent` adds `color`, `streamID` and `ephemeral`. The summary
  drops ephemeral and debug messages.
- **Policy events.** The same file has the comment "At this point in time,
  we don't handle policy events in JSON serialization". RFC 90 covers
  them.
- **`hideDiffs`.** A resource option that "specifies a list of property
  paths whose diff details Pulumi compacts in CLI output". It does not change which resources
  Pulumi updates, only how the CLI prints the diff
  (<https://www.pulumi.com/docs/iac/concepts/resources/options/hidediffs/>).
  The state of a resource holds it as `hideDiff`. This is a noise rule in
  the code of the program, which the author of the change controls.

### Facts from research: how other tools show a large plan, and review fatigue

Checked on 7 November 2026, from public primary sources. Each source is a
comparison only.

**How other tools show a large plan.**

- **Atlantis** posts the plan as forge comments. GitHub limits a comment to
  65,535 characters, so Atlantis splits long output, and
  `--max-comments-per-command` (default 100) cuts from the top
  (<https://www.runatlantis.io/docs/server-configuration>). Its apply
  requirement `approved` does not bind an approval to a plan, so an old
  approval can meet a later plan unless `--discard-approval-on-plan` is set
  (<https://www.runatlantis.io/docs/command-requirements>).
- **Spacelift** shows a delta counter of changes, and a plan policy in Rego
  reads `terraform.resource_changes[]`. A `warn` sends a tracked run with
  autodeploy to a person. A `deny` fails it
  (<https://docs.spacelift.io/concepts/policy/terraform-plan-policy>). Its
  approval policy splits `reviews.current` from `reviews.older`
  (<https://docs.spacelift.io/concepts/policy/approval-policy>). Its
  example "blast radius" policy weights each action and type (delete 10,
  update 5, create 1) and triggers above 100.
- **Open Policy Agent** has the same pattern in its Terraform tutorial: a
  weighted `blast_radius` under 30, and no change to an IAM resource
  (<https://www.openpolicyagent.org/docs/terraform>).
- **HCP Terraform** shows "structured run output" with one node for each
  resource, and gives policies the `tfplan/v2` import with
  `resource_changes`, `resource_drift` and `output_changes`
  (<https://developer.hashicorp.com/terraform/cloud-docs/policy-enforcement/import-reference/tfplan-v2>).
- **Pulumi** has `pulumi preview --diff` and `--json`, and update plans:
  `pulumi up --plan` stops when the update differs from the saved plan
  (<https://www.pulumi.com/docs/iac/concepts/update-plans/>). Its GitHub
  action has `always-include-summary`, which cuts a long comment from the
  front so that the summary stays
  (<https://github.com/pulumi/actions/blob/main/README.md>).
- **Terragrunt** writes one `tfplan.json` for each unit with
  `--json-out-dir`. Its FAQ says no built-in flag prints one combined diff,
  and shows `jq` for it
  (<https://docs.terragrunt.com/process/faq/how-do-i-review-plans-across-all-my-units/>).
- **tfcmt** labels a pull request `destroy` and lists created, updated,
  deleted, replaced, imported and moved resources
  (<https://suzuki-shunsuke.github.io/tfcmt/config>). **tf-summarize**
  groups by add, delete, change and recreate
  (<https://github.com/dineshba/tf-summarize>).

**What no tool documents.** No source groups identical changes across
`count` and `for_each` instances or across units, and no source offers
"what changed since the plan that I approved" with a key for each change.
No vendor publishes a taxonomy of risk (identity, network, stateful).
Each scoring that the sources show is a weighted sum that a team writes,
or an opaque product. Sections B, J and N fill these gaps.

**Review fatigue.**

- SmartBear and Cisco: review 200 to 400 lines at a time, for at most 60
  to 90 minutes. Above about 500 lines each hour, fewer defects are found.
  This is a vendor source with no study size
  (<https://smartbear.com/learn/code-review/best-practices-for-peer-code-review/>).
- Microsoft (Bosu, Greiler and Bird, 2015): the more files in a change,
  the lower the share of useful review comments
  (<https://www.microsoft.com/en-us/research/publication/characteristics-of-useful-code-reviews-an-empirical-study-at-microsoft/>).
- Microsoft (Bacchelli and Bird, 2013): to understand the change is the
  main problem of a reviewer, and tools do not help it much
  (<https://2013.icse-conferences.org/content/expectations-outcomes-and-challenges-modern-code-review.html>).
- McIntosh and others: low review coverage and low participation go with
  more defects after release. This is an association, and a replication at
  Sony Mobile did not find it
  (<https://rebels.cs.uwaterloo.ca/papers/icse2016_shimagaki.pdf>).
- Alert fatigue (Alahmadi, Axon and Martinovic, USENIX Security 2022, 21
  practitioners of security operations): too many alarms make people
  trust and act on them less
  (<https://www.usenix.org/conference/usenixsecurity22/presentation/alahmadi>).
- Public incidents where a plan or a destroy deleted a production database
  are reports of single teams, not postmortems of a review process, for
  example <https://repost.aws/questions/QU5HJ7WlNBQ6yJb-DHod2r7Q>. No
  public study measures how often a reviewer approves a destroy that hides
  in a large plan.

**What this means for the design.** The numbers come from code review,
not from plans, so they set a direction and no threshold. The direction is
clear in each source: a person reads few items well, and many items badly.
So the server must cut what a person reads (sections B, E and N), sort
what is left by risk (section J), and ask for an act on each item that
matters (section M), not one click for the whole plan.

## Options and recommendations

### A. The structured review model in each tool package

ADR 194 already gives the tool-neutral `UnitSummary` and `ResourceChange`.
The question is what each tool package adds, and in which order the view
sorts.

1. **Keep `ResourceChange` as it is.** The view sorts by change kind only.
2. **Add tool-neutral fields that each reader fills.** Each value path gets
   a `forcesReplace` flag, an `unknownAfter` flag and a `secret` flag. Each
   resource change gets the tool's own action and reason as opaque strings
   (`toolAction`, `toolReason`) for labels only. Each resource change
   also gets `movedFrom`, the previous key of the resource, which is empty
   when there was no move.
   - OpenTofu: `forcesReplace` from `replace_paths`, `unknownAfter` from
     `after_unknown`, `secret` from `before_sensitive` or
     `after_sensitive`, `toolReason` from `action_reason`, `movedFrom`
     from `previous_address`, `importing`, true when `change.importing` is
     present.
   - Pulumi: `forcesReplace` from `replaceReasons` and the `*-replace` diff
     kinds, `unknownAfter` from the placeholder string, `secret` from
     `[secret]`, `toolAction` from `op`, `movedFrom` from an `oldState`
     whose URN differs from the URN of the step.
3. **A tool-specific view for each tool.** The console renders each tool's
   own model. Shared views (change groups across tools) do not exist.

**Recommendation: A2.** The core never parses a plan (ADR 194 decision 3),
and the core gets only flags and kinds it can act on. `movedFrom` and the
kind `forget` are tool-neutral, so no field in the core exists for one tool
only (ADR 12). User-facing text keeps the words of each tool: "preview" and
"replace" for Pulumi, "plan" and "must be replaced" for OpenTofu. ADR 195 gives only "preview" and "plan".
`toolAction` and `toolReason` are labels, never input to a rule.
An unknown `action_reason` shows as "unrecognized reason", with the raw
string escaped. It is never an error.

**The map from tool actions to change kinds.** ADR 194 has the kinds
`create`, `update`, `replace`, `delete` and `read`. This RFC adds `forget`,
`import`, `refresh` and `move`. A kind that is not in the table below
fails closed: the reader marks the plan `unreadable` (`ErrPlanFormat`), and
the unit goes to "Needs attention" as a whole, as ADR 194 decision 6
gives. An unknown value of the tool's own action or operation is
different from an unknown `action_reason`, which is only a hint.

| Tool value | Kind | Where it shows |
| --- | --- | --- |
| OpenTofu `["create"]`, `["update"]`, `["read"]` | `create`, `update`, `read` | the other changes |
| OpenTofu `["delete"]` | `delete` | Needs attention |
| OpenTofu `["forget"]` | `forget` | Needs attention |
| OpenTofu `["delete", "create"]` and `["create", "delete"]` | `replace` | Needs attention |
| OpenTofu `["no-op"]` with `previous_address` | `move` | Needs attention |
| OpenTofu `change.importing` present, with `["delete"]`, `["delete", "create"]` or `["create", "delete"]` | `delete` or `replace`, with the flag `importing` | Needs attention |
| OpenTofu `change.importing` present, with any other actions, also `["no-op"]` | `import` | Needs attention |
| OpenTofu `previous_address` present, with `["delete"]`, `["delete", "create"]` or `["create", "delete"]` | `delete` or `replace`, with the flag `movedFrom` | Needs attention |
| OpenTofu `previous_address` present, with `["update"]` | `move`, with the paths of the update | Needs attention |
| OpenTofu `["no-op"]` without `previous_address` and without `importing` | none | not in the summary |
| Pulumi `same` with `movedFrom` (an alias rename: the URN of `oldState` differs from the URN of the step) | `move` | Needs attention |
| Pulumi `same` without `movedFrom` | none | not in the summary |
| Pulumi `create`, `update`, `read` | `create`, `update`, `read` | the other changes |
| Pulumi `delete`, `delete-replaced`, `discard-replaced`, `remove-pending-replace` | `delete` | Needs attention |
| Pulumi `replace`, `create-replacement`, `read-replacement`, `import-replacement` | `replace` | Needs attention |
| Pulumi `import` | `import` | Needs attention |
| Pulumi `discard` | `forget` | Needs attention |
| Pulumi `refresh` | `refresh` | the other changes, never in a group |
| any other string | unreadable | Needs attention, whole unit |

- A Pulumi replace has two or more steps with one URN, for example
  `create-replacement` and `delete-replaced`. The reader joins them in one
  resource change of kind `replace`. A step with no partner keeps the kind
  of the table.
- The resource options `replaceOnChanges` and `deleteBeforeReplace` of
  Pulumi appear in the preview as replace operations. They are
  `replace` in the view like any other replace.
- A resource change that has `movedFrom`, with any action, goes to
  "Needs attention" and never joins a change group.
- An OpenTofu change with `importing` present and the actions `["delete"]`,
  `["delete", "create"]` or `["create", "delete"]` has the kind `delete` or
  `replace`, and the flag `importing`. The row keeps the value paths of its
  actions. It is in "Needs attention" and never joins a change group.
- An OpenTofu change with `importing` present and other actions has the kind
  `import`, also when its actions are `["update"]`. The row keeps the paths of
  the update. It is in "Needs attention" and never joins a change group.
- An OpenTofu move (`previous_address` present) with the actions `["delete"]`,
  `["delete", "create"]` or `["create", "delete"]` has the kind `delete` or
  `replace`, with `movedFrom` set. An OpenTofu move with the action
  `["update"]` has the kind `move`, and the row keeps the value paths of the
  update. Both are in "Needs attention".

**Needs attention keys on actions, not on paths.** The section holds each
change of kind `delete`, `replace`, `forget` or `import` (also an import with the actions
`["no-op"]`), each change with `movedFrom`, and each unit with `errored: true` or an unreadable summary.
The reader never decides this from `replace_paths` or `forcesReplace`.
OpenTofu leaves `replace_paths` out when no path caused the replace, for
example for a tainted resource, and such a replace must still show. The flag `forcesReplace` is for a label on a value path and for
the limits of noise rules (section E).

**Sensitive values.** A `true` in `before_sensitive` or `after_sensitive`
at any level covers the whole subtree under that node. The reader marks
each path under it `secret`, also when the paths below are not listed.

**The order of the view** (an extension of ADR 194 decision 1):

1. The cause diff.
2. The summary: for each unit, the count of each change kind. It counts
   every change, also the collapsed ones.
3. Needs attention.
4. Error diagnostics (section D).
5. Change groups (ADR 194 decision 5).
6. The other changes, by unit, then by resource address or URN, then by
   value path.
7. Warning diagnostics, counted.
8. The blast radius (ADR 194 decision 7).
9. The policy results (ADR 194 decision 8).

### B. One view of the same change across N units

ADR 194 groups changes that are identical: same tool, type, paths, and
values before and after. A common-tag change is often identical. Other
repeats are not:

- `tags.env` gets `prod` in one workspace and `dev` in another,
- a name holds the name of the stack,
- a value is unknown until the apply.

Options for "the same change":

1. **Exact only** (ADR 194 as it is). Simple, and safe. A tag with a
   different value in each environment gives one group for each value.
2. **Same shape.** Same tool, same type, same set of value paths, and the
   same kind of change on each path (add, delete, update). The values can
   differ. The view shows one row "similar change", with the distinct
   value pairs and the count of each, for example `prod` (10), `dev` (10),
   `stage` (10).
3. **Normalized values.** As B2, but the reader replaces known
   per-unit tokens (the name of the stack or workspace, the namespace) with
   a placeholder before an exact compare.
4. **Do nothing.** Each unit shows its own list.

**Recommendation: B1 in version 1. B2 later, as a read-only row.** Exact
change groups stay as ADR 194 gives them. A similar change in a later
version is a row that the view computes at each read. It has no content
key, it is not stored, and a thread cannot anchor on it. So it adds no
one-way door. B3 is not recommended: a wrong guess of a token makes two
different changes look the same.

Rules for a similar change, when it is built:

- It never holds a `create`, `delete`, `replace`, `read`, `forget`,
  `import` or `move`, a change with `movedFrom`, or a path with
  `forcesReplace` or `secret`. This is the rule of ADR 194 decision 5,
  applied again.
- An unknown value is the token `(known after apply)` in the list of
  values. Two unknowns are never an exact match, so they never join an
  exact change group. They can join a similar change.
- **Limits** (proposal): at most 20 distinct value pairs show in the row,
  then "and N more values", with a link to the full list. At most 10,000
  members in one similar change. A larger set splits by unit.

**How the reviewer still sees every unit:**

- The row shows the count of resources and the count of units, and opens
  to a list by unit, then by resource. The list pages, and it never cuts.
  The counts and the list hold only the units whose values the reader may
  read (see the next rule).
- The summary of ADR 194 (the count of each change kind for each unit)
  counts every change, also the grouped ones.
- A view "by unit" lists each unit with all its changes. The grouping is a
  way to read, not a filter.
- A unit that has a change outside all groups gets the mark "has other
  changes" in the row.

**Units whose values the reader may not read.** A group of units can hold a
unit that the reader may not read values of (no `job:read-plans`, ADR 194
decision 9). Then a shared row would show that this unit has the same
change as the others. So, for each row that shows a value, a value path or
a diagnostic text (a change group, a similar change, a diagnostic group, an
explain row):

- The row never lists, counts or marks such a unit. Its counts and its
  list hold only the units that the reader may read.
- The changes of such a unit show only in its own per-unit list, as the
  metadata of ADR 194 decision 9: keys, types, change kinds and counts.
  That list says "values hidden" for this unit alone, which shows nothing
  about other units.
- The key of a change group (`cog-`) of a change of such a unit is not
  returned to the reader, because an equal key would link the unit to a
  group.

**Change order or collection.** The approval lives on a change order. So
the view of this RFC is the view of a change order revision.

- **S1. Change order only.** A collection is a filter of the view (show only
  the units of collection X).
- **S2. Also a collection view** of the latest stored plan of each unit of a
  collection, with no change order. It reads only. It cannot approve.

**Recommendation: S1 for version 1.** A collection view (S2) is a later
read model. It has a different risk: its plans can be of different commits
and ages, and it has no digest.

### C. Sets, compared by members

1. **Schema-aware.** The plan job also stores the provider schema
   (`tofu providers schema -json`) as an optional part `provider-schema`.
   The OpenTofu reader then knows which arrays are sets. It compares sets
   as multisets of the canonical JSON of each member, and shows "same
   members, other order" or "+2 members, -1 member".
2. **Shape heuristic.** When the before and after arrays of a path are
   permutations of each other, the reader marks the path "same members,
   other order". The path still counts as a change, and it still shows.
3. **Do nothing.**

**Recommendation: C2 in version 1, C1 later.** C2 only adds a label. A list
whose order matters keeps its change, and the reviewer reads the label.
C2 never hides a path and never makes a change a no-op. C1 adds a part to
every plan and a parse of a large document, so it waits for data on how
often C2 is not enough. For Pulumi, the reader uses `detailedDiff` from the
provider when it is present, and C2 when it is not.

### D. Repeated diagnostics

The OpenTofu plan JSON has no diagnostics. So:

1. **A new optional part `plan-diagnostics`.** The OpenTofu plan step runs
   `tofu plan -json` and the runner keeps the `diagnostic` messages of the
   stream. The Pulumi reader takes `diagnostics` from the preview JSON. The
   part holds no `snippet` (an excerpt of the source can hold values) and
   no `color`.
2. **Read diagnostics from the job log.** No new part. The log has no
   structure, so this is not recommended.
3. **Do nothing.**

**Recommendation: D1.** The view groups diagnostics by tool, severity and
text:

- OpenTofu: `summary` and `detail`. Each distinct `range.filename` with
  `range.start.line` counts as one location.
- Pulumi: `message` with no URN in the text, and `prefix`. The
  Pulumi CLI gives no source location, so each distinct URN counts as one
  location, and the row says "by resource".
- The row shows the count, the count of distinct locations, the count of
  units, and the first 20 locations (proposal), then "and N more".
- An error diagnostic is never in a count only. Each error group is in its
  own section, after "Needs attention" and before the change groups.
- The text of a diagnostic is a value. It follows the rules for values in
  section E: a unit without `job:read-plans` is not listed, not counted
  and not marked in a row that shows a text. The diagnostics of that unit
  show only in its own per-unit list, as the count of each severity with no
  text and no group key.

### E. Noise rules

A **noise rule** is data of a namespace that collapses a part of the view.
It never changes the plan, the apply, the digest or a fact.

**Fields** (proposal): `id`, `tool`, `match`, `owner` (a person or a group), `reason` (required),
`expiresAt` (required, at most 90 days after the create or the last
renewal), `createdBy`, `createdAt`, `version`. The scope of a rule is in its route
path, not in a field (section H).

**What a rule can never collapse.** The server refuses a rule at save that
can match only these, and the reader ignores a match on these at render:

- a `delete`, a `replace`, a `forget`, an `import`, a move, or any change
  that is in "Needs attention",
- a path with `forcesReplace`,
- a path with `secret` (a masked value has no value to read, so a value
  condition cannot be checked on it),
- a path with `unknownAfter`: a path rule must not hide a value that is
  known only after the apply,
- an error diagnostic,
- a path of a change that has a risk class (section J), `unclassified`
  included,
- a whole resource. A rule collapses paths. A resource whose every changed
  path is collapsed shows as one row "only collapsed changes (N paths)",
  and it still counts in the summary.

**What the server refuses at save** (proposal). The save fails with
`ErrInvalid` for a rule that:

- names no literal resource type (a wildcard type is not allowed),
- has a value path pattern with no literal segment before `**`,
- is above the count limit: at most 200 active rules in a namespace,
- has no reason, no owner or no expiry, or has an expiry above the limit.

The check runs at save and again at each render. A stored rule that fails
it at render collapses nothing.

**Expiry.** At `expiresAt`, the rule stops collapsing. The view then shows
the paths with the mark "rule X expired on <date>". An expired rule stays
as a record until a person deletes it, so the mark can name it.

**Fail open (show more).** When a rule cannot be evaluated (an unknown
match kind after a downgrade, a parse error of a stored rule, a value
condition on an unknown value, a timeout), the reader collapses nothing
for that rule, and the view marks it "rule X not applied: <reason>".

**Values and permissions.** The values of a unit need `job:read-plans` on
that unit (ADR 194 decision 9). The rule below applies to each unit on its
own, in every output:

- A unit without `job:read-plans` adds no value and no value path to a
  list of values, to a count per value, to the explain view or to the
  Markdown. A row that shows a value, a value path or a diagnostic text
  never lists, counts or marks such a unit (the rule of section B). So
  the mark "values hidden (N units)" is not on those rows: it would show
  that the hidden units have the same change. The per-unit list of the
  hidden unit has the mark, with its metadata only.
- A rule never lets a reader infer a hidden value or a hidden change. A
  count per value counts only the units that the reader may read, and the
  key of a change group of a hidden unit is not returned. A rule that
  collapses a path of a hidden unit adds nothing to a row of other units.
- Markdown that the CI shim posts to a forge holds no value and no value
  path. It holds change kinds, counts and the link to the view
  (**recommendation**). A forge comment has an audience that the server
  does not control.

**The explain view.** For each revision, a list of each collapsed path:
unit, resource, path, the rule ID and version, its owner, its reason and
its expiry. It needs the same actions as the view, and the rule above for
values: for a unit that the reader may not read values of, it holds the
count of collapsed paths. It has no rule ID, no path and no value. It also lists each path that a Pulumi `hideDiffs` option names
(section "Pulumi CLI preview JSON"). The view does not apply `hideDiffs`:
the author of the change controls it, so it is not a rule of the reviewer.
The explain view lists it as "hidden by the program" and the path shows.

**Rules of the author and rules that came late** (**recommendation**).
A noise rule can make a reviewer read less. So:

- A rule that an author of the change order wrote collapses nothing in that
  change order. Every author counts (ADR 190 decision 4: the principal that
  created the change order and each principal that attached a revision).
  The server compares each author with each principal in the **write
  history** of the rule: the creator, each principal that changed it, and
  each principal that renewed it. One match is enough. Membership of the
  `owner` group of the rule does not count as a write by itself
  (**recommendation**): a person who only belongs to that group did not
  write the rule.
- A rule created or changed after a revision was attached collapses
  nothing in that revision. The view marks it "new since attach". The
  author can attach a new revision to use it. A renewal is a change of the
  rule. So a renewal after an attach collapses nothing in that revision
  (**recommendation**).
- A person whose own rule collapses at least one row of a revision cannot
  sign that revision. "Own rule" has the same meaning as above: the person
  is in the write history of the rule (create, change or renewal). The
  server refuses the signature with a specific error, `ErrSelfApproval`
  (not `ErrPolicyDenied`), and writes an audit entry.

**Authorization and audit.**

- One action for the write of noise rules, as ADR 230 has one action,
  `namespace:manage-gates`, for all writes of gate configuration. Proposal,
  with the namespace as the object: `namespace:manage-noise-rules`. A
  collection scope or a unit scope is part of the route path, as for gates
  (ADR 230 decision 3). A read of the
  rules needs `namespace:read-noise-rules`. The action names follow the
  style of ADR 210 and must be checked against the action lists of ADR 210
  and ADR 230 when an ADR records them. A read of the view needs
  `changeorder:read` on the units.
- `namespace:manage-noise-rules` is in no built-in role except the
  namespace administrator, as a proposal (question 4).
- A create, a change and a renewal of a rule need a fresh authentication
  (ADR 227), because a rule reduces what a reviewer reads
  (**recommendation**). A delete does not, because it can only show more.
- Each rule write carries a `namespace.Fence` and the store checks it in
  the same transaction, as for every write in a namespace.
- The rules are a new table. It goes in the table list of the backup
  domain, so a backup holds the rules and a restore brings them back.
- Each create, change, renewal and deletion writes one audit entry with the
  rule, the old and the new version, and the reason. An expiry writes one
  audit entry with the actor `@server`.
- **Renewal cap** (**recommendation**): a renewal sets a new expiry at most
  90 days from the renewal. A rule can be renewed at most 3 times. After
  that, a person creates a new rule with a new reason.
- An agent never creates a rule, as an agent never resolves a thread
  (ADR 190).

**The rule language.**

1. **Paths only.** A rule names a resource type pattern and a value path
   pattern (exact segments, `*` for one segment, `**` at the end only).
2. **Paths with value conditions from a closed list.** As E1, plus at most
   one condition on the path: `before-absent` (the key is new),
   `after-matches` with a fixed pattern of a closed set (RFC 3339 timestamp,
   UUID, a hex digest), or `change-kind` is `add`.
3. **Cedar conditions,** as gates use (ADR 224).
4. **A general expression language.**

**Recommendation: E2.** E1 cannot say "collapse a timestamp tag, but show
any other value of that tag". E3 and E4 are hard to explain in the explain
view, and they can read values. E2 has no user-written regular expression,
so the cost of an evaluation stays bounded.

**Built-in rule sets.** The server ships rule sets as data, with a version,
for example "timestamp tags", "provider default tags" and "same members,
other order". No set is on by default. A person turns a set on for a
scope, and that act is a rule record with an owner, a reason and an expiry,
as above. An upgrade that changes a built-in set gives it a new version.
A scope keeps the version it turned on until a person moves it, so an
upgrade never collapses more on its own.

### F. The approval binds to the stored plan and to the view basis

The signature already binds to the digest of the stored plans (ADR 191).
The view is not the plan, so the server never signs a rendering. The
question is how the signature also records which rules rendered the view.

1. **Add the rule set version to `cho-digest`** as a new encoding
   `cho-digest/2`. Each change of a rule in the scope then changes the
   digest and voids each signature. **One-way door.**
2. **A second value in the signature, the view basis.** The approve request
   names the digest and the view basis. A view basis that is not the
   current one fails with `ErrViewChanged` and records nothing. The
   signature keeps it.
3. **Record nothing about the view.**

**Recommendation: F2.** The plan stays the only thing that the apply reads.
A rule change after a signature does not void the signature, because the
plan did not change and the signer saw the rules of that time. The audit
shows what each signer saw. A rule change before a signature makes the
signer read the new view.

**The view basis (definition).** The view basis is the SHA-256 of a
canonical list that holds, in this order:

1. the sorted list of the pairs (rule ID, rule version, unit) that apply
   to the revision. A pair applies when the scope of the rule holds the
   unit, the unit is in the revision, and the rule is not expired at that
   time. So a unit that moves into or out of a collection changes the
   basis, also when no rule changes,
2. the ID and the version of each built-in rule set that a scope of the
   revision turned on,
3. the version name of the view encoding (for example `cho-view/1`), the
   version of the reader of each tool in the revision, and the version of
   the risk catalog with each scope addition (section J).

The view answers the `viewBasis` with each `GET` of the view. The rules
for the basis:

- The query `rules=none` answers the view with no collapse. Its basis is
  the hash of parts 3 only, with empty lists for parts 1 and 2. **An
  approve request always accepts the basis of `rules=none`**, because that
  view shows every change.
- The server computes the current basis at the instant of the approve
  request, in the transaction that records the signature. It never trusts
  a basis that the client computed.
- A rule can expire between the `GET` and the approve request. The current
  basis then differs from the one in the request, and the approve fails
  with `ErrViewChanged`. The signer reads the view again. The same happens
  for a rule change, a new built-in set version, or a reader upgrade.
- The signature keeps the basis that it accepted, and the audit entry
  shows it.
- A carried signature (ADR 225 decision 12) keeps the view basis of the
  signature that it carries from (**recommendation**). A carry-over happens
  at the attach, so no approve request names a basis for it.

**The apply runs exactly that stored plan.** Nothing new: the apply job
names its stored plan, `job.CheckPlan` runs at the dispatch, and the runner
refuses a part with another hash (ADRs 102, 190). The view, the rules and
the collapses are never input to a job.

### G. Plan JSON is untrusted input (ADR 250)

The part `plan-display` comes from a runner that ran code of the team. The
code can write any string into a resource name, a tag, a value or a
diagnostic. All limits below that this RFC adds are proposals.

| Layer | What it does | Its test |
| --- | --- | --- |
| Check at the boundary | The reader streams the part with a decoder that has limits. The part has at most `job.MaxPlanPart` bytes (256 MiB). The nesting depth is at most 64 (proposal), and a string has at most 1 MiB (proposal). A plan has at most 100,000 resource changes (ADR 196 decision 4), and a resource at most 10,000 paths (proposal). The reader checks the schema. For OpenTofu, `format_version` must have the major version `1`. For Pulumi, the fields must be the known fields of `PreviewDigest`. An unknown major version, a bad type, an unknown action or operation, or a limit gives `ErrPlanFormat`. The reader never changes a document to make it valid. | Fuzz each reader. One test for each limit at the limit and at the limit plus one. One test for each unknown action and operation string. |
| Encode at use | The console escapes each string. The Markdown output escapes Markdown and HTML. It strips CR and LF from a table cell. It puts each value in a code span. It breaks `@` mentions and links. Logs strip CR and LF. Bidirectional control characters show as escapes. SQL takes placeholders only. | A hostile name, path and value through each renderer, with the reader checks skipped. |
| Plan strings to an assistant | Text of a plan can go to the built-in assistant or to an MCP tool (a name, a value, a diagnostic). It goes in a delimited field that is marked as untrusted data. It is never an instruction (the same rule as RFC 90 has for finding text). | A hostile string with an instruction in a plan, through the assistant output, with the reader checks skipped. |
| Limit the effect | The analysis runs in the durable operation of ADR 194 decision 6, with a time limit and a memory limit. The server parses a part once and keeps the summary. A rule has a bounded cost (no user regular expression). The runner never runs `pulumi preview` with `--show-secrets`, and it refuses a plan step whose arguments hold that flag. So the part holds `[secret]`, not the value, even if the reader check fails. | A plan at each limit, and a rule set at its count limit. A plan step with `--show-secrets` is refused by the runner. |
| Authorize at use | Values need `job:read-plans` on each unit (ADR 194 decision 9), for each output. A unit without it adds no value and no path. This holds for a list of values, a count per value, the explain view and the Markdown. A row that shows a value, a path or a diagnostic text never lists, counts or marks such a unit. The server does not return the key of a change group of that unit. Only the per-unit list of that unit says "values hidden", with metadata. The store checks the action of a rule on its scope in the transaction of the write. | A rule write with a token of another scope. A view read, an explain read and a Markdown read of a change order with one unit that the reader may not read. No value or path of that unit appears in any output. A second test: two units have the same change, and the reader may read one. The group row, the counts, the diagnostic group, the explain view and the Markdown name, count and mark only the readable unit. No `cog-` key of the hidden change comes back. The route check is skipped. |
| Fail closed and record | A plan that the reader cannot read is `unreadable` and blocks approval (ADR 194 decision 6). A rule that cannot be evaluated collapses nothing (fail open for the view, which is the safe side). Each refused rule write writes an audit entry. | An unreadable plan gives no signature. A broken rule shows each path. |

**Masking is kept.** The reader masks before it computes a fingerprint, a
group or a collapse. A rule never unmasks. A literal value `[secret]` in an
OpenTofu plan is a plain string, and the reader does not treat it as a
mark. The `plan-diagnostics` part drops `snippet`, because an excerpt of
the source can hold a value.

### H. The API, the console and Markdown

REST API first (ADR 17), under `/api/dustpan/v1` (ADR 210). Paths are
proposals:

- `GET .../namespaces/{namespace}/change-orders/{id}/revisions/{rev}/view`
  gets the new sections and `viewBasis`. The query `rules=none` answers the
  view with no collapse. Collapses never apply to the facts of a gate.
- `GET .../revisions/{rev}/view/explain`: the explain view.
- `GET .../revisions/{rev}/view` with `Accept: text/markdown`: a Markdown
  rendering with a link to the full view. The CI shim theme (#1091) posts
  it as a comment. It holds no value and no value path
  (section E). It is at most 65,536 characters (**recommendation**), the
  largest comment that a forge accepts, and it ends with "truncated" and
  the link when the view is longer. Markdown is a rendering. It is never a
  basis of a signature.
- `GET {scope}/noise-rules`, `GET`, `PUT` and `DELETE {scope}/noise-rules/{id}`,
  as for gates (ADR 230 decision 3). `{scope}` is one of
  `.../namespaces/{namespace}`, `.../namespaces/{namespace}/collections/{collection}`
  and `.../namespaces/{namespace}/units/{tool}/{name}`. Each variable segment
  is percent-encoded, as ADR 230 decision 3 says. A `PUT` names no scope in
  its body.

The console shows the view in the order of section A, with "collapsed by
rule X" on each collapsed row and a switch "show collapsed changes".

### I. The first slice

**What exists today.** The package `pkg/changeorder` is not on `main`.
The change order store, its plan jobs and its revisions do not exist yet.
So the first slice is smaller than a view of a change order.

**The first slice:**

1. The OpenTofu `PlanReader` with the flags, the kinds and the map of
   section A, and the checks of section G.
2. The route `GET .../jobs/{job_id}/plan/view` for one stored plan of one
   job. It answers the summary, "Needs attention", and the exact change
   groups of that one plan, with masked values for a reader who has
   `job:read-plans`.
3. The Pulumi reader follows in a second slice with its own e2e step.

The view of a whole change order (the cause diff, the cross-unit groups,
the section order, `viewBasis`) comes when the change order store is on
`main`. Noise rules, similar changes and the new parts come after that.

**The e2e step.** The e2e suite drives the real OpenTofu CLI. It needs no
cloud account: the workspace uses the built-in `terraform_data` resource,
which needs no provider download.

1. A workspace has `terraform_data` with `count = 3` and one `input`
   value. The step changes the `input` of all three to the same new value,
   plans, and reads the view. It checks one change group with 3 resources,
   and nothing in "Needs attention".
2. The step changes `triggers_replace` of one resource and plans again.
   It checks that "Needs attention" comes first, holds that one resource
   as `replace` with its `replace_paths`, and that the other two stay in
   the change group.
3. The step runs `tofu taint` on another resource and plans again. The
   OpenTofu docs say that `replace_paths` is omitted when a tainted
   resource is replaced (`replace_because_tainted`). The step checks that this resource is in
   "Needs attention" as `replace`, with no value path.

### J. The risk model: risk classes that the server computes

Sections A to I make the view readable. They do not tell a gate which
changes are dangerous. ADR 226 decision 4 gives the `plan` group: counts of
`add`, `change`, `replace` and `destroy`, and `resourceTypes`. A gate on
those counts cannot tell a deleted tag from a deleted database, or a
changed description from a changed IAM policy.

**Options.**

1. **A numeric risk score** for each change and each unit, for example 0
   to 100, as some commercial products show. A gate compares the score
   with a threshold.
2. **Risk classes.** A closed set of named classes. Each resource change
   gets zero or more classes. Each unit gets a count for each class. A gate
   names the class that it cares about.
3. **Nothing in the server.** The policy pack of each team (RFC 90)
   classifies changes and reports through `resourcePolicy`.

**Recommendation: J2.** A score hides its reason, and two teams cannot
agree what 70 means. A class names the reason, and the decision record
can show it ("approval:security because `risk.counts.identity` is 2").
This is the rule "No magic" of `CLAUDE.md`: each block names the rule. J3
leaves every team to write the same classifier, and a pack runs in the
sandbox, so its report is not a fact that the server computed.

**The classes (proposal, version 1).** A class is a name, a meaning and a
fixed rule. The rules read only the change kind, the resource type, the
value paths and the flags of section A. They never read a value, except
the closed set of value tests in the table.

| Class | The change has it when | Example |
| --- | --- | --- |
| `destroy` | the kind is `delete`, or `replace` with the delete first | a deleted bucket |
| `replace` | the kind is `replace` with the create first | a new instance before the old one goes |
| `stateful` | the kind is `delete` or `replace`, and the resource type is in the `stateful` list of the catalog | a database, a bucket, a volume, a key, a DNS zone |
| `identity` | the resource type is in the `identity` list, or a changed path matches an `identity` path pattern of the catalog | an IAM policy, a role binding, `assume_role_policy` |
| `network` | the resource type is in the `network` list, or a changed path matches a `network` path pattern | a security group rule, a firewall rule, a route |
| `exposure` | a changed path matches an `exposure` pattern and the value after is in the closed list of the pattern | `0.0.0.0/0` or `::/0` in an ingress CIDR, `publicly_accessible = true`, a public ACL |
| `protection` | a changed path matches a `protection` pattern and the value after turns protection off | `deletion_protection = false`, `force_destroy = true`, a Pulumi `protect` flag deleted |
| `secret` | a changed path is `secret` (section A) | a rotated password |
| `unknown` | a changed path of class `identity`, `network` or `exposure` is `unknownAfter` | a policy document that is known only after the apply |
| `lifecycle` | the kind is `forget`, `import` or `move`, or the change has `movedFrom` | a resource that leaves state |
| `drift` | the plan writes a path that `resource_drift` (OpenTofu) or a refresh step (Pulumi) shows as changed outside the tool | a manual hotfix that the apply reverts |
| `unclassified` | the resource type is in no list of the **shipped** catalog, with any change kind: `create`, `update`, `delete`, `replace`, `read`, `import`, `forget` or `move` | a third-party access-policy resource that the catalog does not know yet |

- `destroy` and `replace` come from the kind alone. They never depend on
  the catalog, so a type that the catalog does not know is still caught.
  This is the same rule as "Needs attention keys on actions, not on paths"
  in section A.
- `unclassified` covers every change kind. A `create` of a type that the
  catalog does not know can grant access (a collaborator, a policy
  attachment), so it is never treated as safe.
- A change can have more than one class. A deleted RDS instance with
  `deletion_protection` turned off in the same plan is `destroy`,
  `stateful` and `protection`.
- The view shows the classes of each change as labels, and sorts "Needs
  attention" by class: `stateful`, `destroy`, `identity`, `exposure`,
  `protection`, `network`, `unclassified`, then the rest.
- **A noise rule never collapses a path of a change that has a class.**
  This adds one line to the list "What a rule can never collapse" of
  section E.

**The catalog.** The lists and the path patterns are data, not code:

- The server ships a **risk catalog** with a version, for example
  `risk-catalog/1.4`. It holds the lists for the providers that the e2e
  suite and the docs name first: AWS, Google Cloud, Azure, Kubernetes, the
  built-in types of each tool, and the matching Pulumi types. Each entry
  names its source (the provider docs of the type).
- The shipped catalog has a list `known`: the types that the catalog
  knows and that have no class of their own, for example a tag resource or
  `terraform_data`. Only the shipped catalog holds `known`. A type is
  `unclassified` when it is in no list of the shipped catalog, `known`
  included.
- **A scope addition only adds a class.** A namespace, a collection or a
  unit can add entries to the lists `stateful`, `identity`, `network`,
  `exposure` and `protection`, as a path set is added (ADR 226 decision
  13). It can never add to `known`, never delete an entry of a wider scope,
  and never take `unclassified` away from a change: a type that a scope
  adds to `identity` stays `unclassified` until the shipped catalog knows
  it. So a scope addition can raise what a change needs, never lower it
  (ADR 222 decision 5).
- An upgrade of the server can add entries to the shipped catalog in a
  minor version. An entry in `known` in a minor version would lower a
  class, so a minor version never adds to `known` a type that an earlier
  version of the same major had in no list. It deletes or narrows an entry,
  or adds such a type to `known`, only in a new major version. A scope
  moves to a new major version only by a person's act, with the action
  `namespace:manage-risk-catalog`, a fresh authentication (ADR 227) and an
  audit entry. So an upgrade never lowers a class on its own.
- The decision record names the catalog version and each scope addition
  that gave a class (ADR 229 decision 1).
- The catalog is the same for both tools where the tools share a provider
  (for example `aws_db_instance` and `aws:rds/instance:Instance`). Each
  entry names the type in the words of each tool. No type of one tool is
  in the core (ADR 12).

**The fact group.** `change.planned` gets the group `risk` in a new minor
version, `change.planned/1.1` (ADR 226 decision 2):

| Field | Type |
| --- | --- |
| `available` | boolean |
| `classes` | set of class names that at least one change has |
| `counts` | record with one long for each class |
| `catalog` | the catalog version, as a string |

`change.apply` gets the same group with the values of that moment.
`available` is `false` when the summary of a unit is `unreadable`, when
the plan has no `plan-display`, or when the analysis did not end. Every
built-in gate and starter pack treats `available == false` as a match
(ADR 226 decision 3). The group is small: 12 counts and a set of at most
12 names, so it never meets the 256 KiB limit of ADR 226 decision 14.

### K. Which risk facts are verified, and what an unverified one can do

The server computes the `risk` group itself, in Go, from the summary. But
the summary reads `plan-display`, which the tool in the sandbox writes.
The server does not check today that `plan-display` agrees with
`plan-apply`, which the apply enforces. A `plan-display` that is partial but
valid (for example, with entries of `resource_changes` left out) gives
`available == true` and counts of 0. So the origin of `risk` is the origin
of its input. This RFC uses the four origins that RFC 122 proposes:
`server`, `job-proxy`, `runner` and `external`.

| Tool | Input of the summary | Origin of `plan` and `risk` |
| --- | --- | --- |
| Pulumi, today | `plan-display` | `runner` |
| Pulumi, when the server reads the update plan itself (RFC 122 option O11-b, a proposal) | the operation set and the property diff that the server computes from the update plan and the state that it holds | `server` |
| OpenTofu, today | `plan-display` | `runner` |
| OpenTofu, with a render step in a fresh sandbox (RFC 122 option O11-c, a proposal) | `tofu show -json` of the plan file, with no code of the change | `runner` |

**The rule (this RFC, for every source, trusted or not).** A fact of the
origin `runner` can only add a requirement. It can never cancel one:

- When a gate with the outcome `approval:` reads a group of the origin
  `runner` in its `when` or its `unless`, the server counts the gate as
  matched, whatever the value. So `approval:security when
  risk.counts.identity > 0` asks for security on each change until the
  origin is `server`. The decision record says why: "counted as matched:
  reads `risk`, origin `runner`".
- The `unless` of an approval default (ADR 225 decision 4) that reads a
  group of the origin `runner` exempts nothing.
- A `deny` gate and a `warn` gate can read a group of the origin `runner`
  as usual. A forged value then cancels only the layer that the gate adds.
- The check at save warns, and the simulate route (ADR 229 decision 6)
  shows, each gate whose `approval:` outcome the rule above holds on.

This is stricter than RFC 122 proposes, which holds the rule for an
untrusted source only. The reason: with a trusted source, a skipped
approval leaves no approved set of operations, so the floor of ADR 299
decision 10 has nothing to compare at the apply. If RFC 122 is accepted,
its rule and this one both hold.

So "review only what is risky" (section L) skips an approval only for a
Pulumi plan after the server reads the update plan itself. For OpenTofu,
it skips no approval until the server computes the operation set itself.
Until then, the risk classes still sort the view, ask for the right
approvers and give the acknowledgments of section M.

### L. Review by risk

With section J, a scope can ask for the right person for each risk.

- **A starter pack `review-by-risk`** (ADR 229 decision 7) ships these
  gates, in `evaluate` mode, as each pack does:

  | Gate | Outcome |
  | --- | --- |
  | `risk-unavailable` | `approval:owners when !risk.available` |
  | `risk-destroy` | `approval:owners when risk.counts.destroy > 0 \|\| risk.counts.lifecycle > 0` |
  | `risk-stateful` | `approval:stateful-owners when risk.counts.stateful > 0`, with a rule whose `count` is 2 (two distinct people, ADR 225 decisions 1 and 6) |
  | `risk-identity` | `approval:security when risk.counts.identity > 0 \|\| risk.counts.exposure > 0 \|\| risk.counts.unknown > 0` |
  | `risk-protection` | `approval:owners when risk.counts.protection > 0 \|\| risk.counts.drift > 0` |
  | `risk-unclassified` | `warn when risk.counts.unclassified > 0` (a warn blocks auto-apply, ADR 229 decision 3) |

- **Auto-apply needs each change classified.** A plan with a change of the
  class `unclassified` meets the warn, so it never auto-applies. A
  narrower scope cannot exempt a change from this gate of a wider scope
  (ADR 224 decision 6), and no scope addition takes `unclassified` away
  (section J).
- A change that matches no gate needs no approval from the pack, **only
  when the origin of `risk` is `server`** (section K). With the origin
  `runner`, each `approval:` gate of the pack counts as matched, so the
  change waits for its approvers. The approval default of the scope (ADR
  225 decision 4) still holds in both cases. The built-in gates of ADR 226
  decision 7 hold too.
- The pack is data. A team copies it, changes the rules and the approvers,
  and tests it with `gate.RunTestFile` and the simulate route (ADR 229
  decisions 6 and 8) against its last 90 days of plans before it switches
  a gate to `enforce`.

### M. Acknowledged changes: the signer acknowledges each high-risk change

The evidence on review fatigue (section "Facts from research") says that
a reviewer who sees many items approves without reading, and that a
reviewer who must act on the key item reads it. ADR 191 binds a signature
to a digest. A digest proves which plan the signer saw, not which line the
signer read.

1. **Nothing more.** The signature stays as ADR 191 gives it.
2. **An acknowledgment of each high-risk change.** The approve request
   lists the change key (below) of each change of the revision that has a
   class of the **acknowledge list** of the scope. The server compares the
   list with the set that it computes in the same transaction. A list that
   is not equal fails with `ErrAcknowledgeMismatch` and records nothing.
   The console shows a check box on each such change, and the approve
   button stays off until each box is checked.
3. **A typed confirmation** for each such change, as RFC 121 decision 25
   proposes for a destroy of a stateful resource by an agent.

**Recommendation: M2**, with M3 only for the confirmation that RFC 121
proposes, where an agent drafted the change. A check box on 5 changes is
work that a person can do. Typing on 5 changes is not, and people then
copy and paste.

- **The acknowledge list.** The installation sets a floor: `stateful`,
  `identity`, `exposure`, `protection` and `unclassified` by default. A
  namespace, a collection or a unit can add classes to the list, never
  delete one of a wider scope or of the floor. So no scope can turn
  section M off.
- **The origin shows.** The acknowledgment set comes from the same summary
  as the `risk` group, so it has the same origin (section K). The view and
  the signature record show it. With the origin `runner`, the console says
  "from the part `plan-display` that the runner wrote: not checked against the
  plan that the apply runs", and the signature record holds the origin. An
  acknowledgment of a set of the origin `runner` is evidence of what the
  signer saw, not of what the apply runs.
- **The confirmation page of an agent change.** RFC 121 decision 25
  proposes a page with "the facts that the server computed". When that
  page uses this summary, it shows the origin of each count and class,
  with the same text. A `plan-display` that a hostile program wrote can hide a
  `stateful` class, so with the origin `runner` the page asks for the typed
  confirmation of each `delete` and `replace`, whatever its class.
- A large plan with 400 `stateful` changes asks for 400 check boxes. That
  is the point: a plan with 400 deletes of stateful resources needs a
  person to see 400 lines. The view offers "check all in this group" for a
  change group of identical changes only, and the request still lists each
  key.

**The change key.** Each resource change gets two keys:

- The **pairing key** finds the same resource in two plans: the tool, the
  unit ID and the resource key (ADR 193 decision 3). It holds no value.
  Section N uses it to tell "changed" from "new" and "gone".
- The **change key** says whether the change is the same. Its form is
  `chg-<review key version>-<32 hex characters>`: the first 16 bytes of
  HMAC-SHA-256, under the **review key** of the namespace, of this input:
  - the encoding name `chg/1`, the tool, the unit ID, the resource key and
    the kind,
  - for each changed value path, in sorted order: the path, the flags
    `secret` and `unknownAfter`, and the **value digests** before and
    after.

  The input is a list of fields. Each field is its length as 4 bytes, big
  endian, then its bytes. So two different inputs never give the same
  bytes.
- **The value digest** is the HMAC-SHA-256 of the canonical JSON of the
  real value, under the review key, before any mask. So a secret that
  changes from A to B in one revision and from A to C in the next gives
  two different keys, and the key tells nothing about the value. The
  server computes it in the analysis, which already reads the real values
  of an OpenTofu plan before it masks them (ADR 194 decision 6).
- **Values that the server cannot see.** A value that is unknown until
  the apply, a Pulumi value that `plan-display` shows as `[secret]`, and a
  value that a secrets provider of the team encrypts have no value digest.
  A change with such a path gets a change key with the mark `opaque`. An
  `opaque` key never counts as "unchanged" in section N, and an
  acknowledgment of it holds only for that revision.
- **The review key.** A random 256-bit key of the namespace, kept sealed
  as a data key of the namespace (ADR 23), with versions. It is a new key,
  not the key of `cog-` (ADR 194 decision 5). A rotation of the namespace
  key changes only its wrap (ADR 217 decision 19). A full re-encryption
  after a compromise (ADR 217 decision 20) makes a new version. Each
  stored change key holds its version. Two keys of different versions are
  never compared: the delta then shows each change as changed.
- **One-way door:** acknowledgments, the records of ADR 299 decision 10
  and backups hold the key, its version and its encoding name. A new
  encoding needs a new encoding name and a new prefix.

### N. What changed since the plan that I saw

A new revision of a change order, or a new plan after a push, makes the
reviewer read the whole view again. ADR 299 decision 10 already records,
for each approval, the set of operations that the approver saw.

1. **No delta view.**
2. **A delta against the last revision that the person signed or
   acknowledged.** The view takes `since=<revision>`. For each unit, it
   pairs the changes of the two revisions by their pairing key, and
   answers each one as `new`, `gone`, `changed` (the same pairing key,
   another change key, or an `opaque` key) or `unchanged` (the same change
   key, not `opaque`). The view folds `unchanged`.
3. **A delta against each plan that the person opened.** The server
   records each read of a view.

**Recommendation: N2.** N3 records reads, which is a log of people, and a
read is not a review. N2 uses the records that ADR 191 and ADR 299 keep.

- A `changed` row shows the value paths that changed between the two
  plans, with masked values, as an update shows.
- The delta has the origin of its two summaries (section K). With the
  origin `runner`, the view says that a forged `plan-display` can fold a changed
  resource, and it offers "show all" at the top.
- The delta is a way to read. The approve request of section M still
  lists every change of the acknowledge list in the new revision, also the
  unchanged ones, and the signature still binds to the digest of the new
  revision. A carry-over of a signature stays as ADR 225 decision 12
  gives it (`off` by default).
- **At `change.apply`.** The floor of ADR 299 decision 10 compares the
  operations with the set that the approver saw. This RFC adds the change
  keys to that record:
  - an acknowledged change whose change key differs, or is `opaque` in a
    new plan, **stops** the apply for a new approval with a fresh
    authentication, as the floor does for a new delete,
  - each other change whose key differs is **named** in the decision
    record and does not stop the apply. The binding level of the scope
    (ADR 299 decision 9) decides what may differ.
- The same compare gives the plan diff between two runs of a unit with no
  change order: `GET .../jobs/{job_id}/plan/view?since=<job_id>`, for two
  jobs of the same unit.

### O. The new writes, their actions and agents

Each new write lowers or raises what a reviewer reads, so each one has a
named action, a fresh authentication (ADR 227) and an audit entry. RFC
121 proposes an agent class for each action: `allowed`, `held` or
`never`. This RFC proposes `never` for each new write, so that an agent
can never change what a person must review.

| Write | Action | Fresh authentication | Agent class (as RFC 121 proposes the classes) |
| --- | --- | --- | --- |
| A scope addition to the risk catalog | `namespace:manage-risk-catalog` | yes | `never` |
| A move of a scope to a new major version of the catalog | `namespace:manage-risk-catalog` | yes | `never` |
| An addition to the acknowledge list of a scope | `namespace:manage-gates` (ADR 230) | yes | `never` |
| A switch of a gate of the starter pack to `enforce` or back | `namespace:manage-gates` (ADR 230) | yes | `never` |
| A noise rule (section E) | `namespace:manage-noise-rules` | yes | `never` |
| An acknowledgment and a signature | the approve action of ADR 225 decision 7 | as the approval rule asks | `never` |

A scope addition, a catalog move and an acknowledge list hold a
`namespace.Fence` and go in the table list of the backup domain, as a
noise rule does (section E). The route test of ADR 17 checks each route.

## Recommended design

The server computes one plan summary for each unit plan, and every reader
uses it: the view, the Markdown, the gates and, if RFC 121 is accepted,
the confirmation page of an agent change.

1. **One summary, computed once.** The `PlanReader` of each tool gives the
   `UnitSummary` of ADR 194 with the fields of section A2 and the map of
   change kinds. The durable operation `changeorder.analyze` (ADR 194
   decision 6) adds the risk classes of section J, the pairing key and the
   change key of section M to each resource change. Metadata goes to SQL,
   content to the blob store under the item key of the plan, as ADR 194
   decision 6 says. The core never parses a plan.
2. **Risk classes, not a score** (section J). A closed set of 12 classes
   from a versioned risk catalog. A scope addition only adds a class. Only
   the shipped catalog can take `unclassified` away.
3. **Facts with an origin** (section K). `change.planned/1.1` and
   `change.apply` get the group `risk`. A fact of the origin `runner` can
   only add a requirement, for every source.
4. **Review by risk** (section L). The starter pack `review-by-risk` in
   `evaluate` mode. It skips an approval only when the origin is `server`.
5. **The view, sorted by risk.** The order of section A, with "Needs
   attention" sorted by class. Exact change groups only in version 1
   (section B1). Noise rules (section E) never collapse a classed change.
6. **An act on each change that matters** (section M2), with the origin
   shown.
7. **The delta since the last signature** (section N2), with the pairing
   key and the change key. An acknowledged change that changed stops the
   apply.
8. **Every new write is `never` for an agent** (section O).
9. **The apply runs exactly the stored plan.** No change: ADR 102, ADR 190,
   ADR 191 and the floor of ADR 299 decision 10.

**What a reviewer of a plan with 400 changes then reads.** The summary
counts, then the changes with a class (often a few), each with a check
box, then one row for each exact change group, then the other changes,
folded. On a new revision, only the changes that are new, gone or
changed since the revision that the reviewer signed.

## One-way doors before the first tag

1. **The change key `chg-` and the pairing key** (section M): the prefix,
   the encoding name `chg/1`, the length-prefixed input, the value digest
   and the review key version in each key. Acknowledgments, the records of
   ADR 299 decision 10 and backups hold them. A new encoding needs a new
   encoding name and a new prefix, as for `cog-` (ADR 194 decision 5).
2. **The names of the risk classes** (section J). Stored gates, approval
   defaults and test files name them. A rename or a deletion is a new major
   version of the fact schema (ADR 226 decision 2), and each stored gate
   must pass the type check again. A new class is a minor version. So fix
   the 12 names before the first tag. Adding classes later is safe.
3. **Catalog changes only add** (section J). This covers the list `known`
   by name: only the shipped catalog holds it, a scope never adds to it,
   and a minor version never adds a type to it that the same major version
   had in no list. If the first release can lower a class in a minor
   version or through a scope, each stored gate can relax on an upgrade or
   by the act of a narrower scope. The rule must hold from the first
   catalog.
4. **The acknowledged keys and their origin in the signature record**
   (section M). A signature is evidence. Its format holds the list and the
   origin from the first tag, or a signature of the first release cannot
   show what the signer acknowledged.
5. **The rule that a fact of the origin `runner` only adds** (section K).
   A team that writes gates in the first release learns what a gate can
   skip. A later, stricter rule would turn their auto-applies into waits.
   So the rule holds from the first tag.
6. **The view basis** (section F) gets the catalog version in part 3
   before the first tag, because a stored signature holds the basis.

Not a door: the starter pack (data that a team copies), the sort order of
the view, the classes that a scope adds to its acknowledge list, and
similar changes (B2, a read-only row with no stored key).

## Slices, each with its e2e step

The first slice of section I stays the first slice. Each slice below adds
one part and its e2e step. The e2e suite drives the real CLI with no cloud
account, so the steps use `terraform_data`, which the shipped catalog has
in `known`, and `random_pet` of the `hashicorp/random` provider, which the
e2e suite already downloads and which the shipped catalog does not hold.

1. **The OpenTofu reader and the job view** (section I). **e2e:** the three
   steps of section I.
2. **Risk classes and the `risk` fact.** The catalog, scope additions,
   the classes of section J, and the group `risk` in `change.planned/1.1`.
   **e2e:**
   - The namespace adds the path `input.role` of `terraform_data` to
     `identity`. A change of `input.role` has the class `identity`. The
     decision record shows `risk.counts.identity == 1`, the origin
     `runner` and the catalog version. The change is in "Needs attention".
   - A change of `input.name` has no class.
   - A `random_pet` with a new `length` has the class `unclassified`.
   - A request that adds `random_pet` to `known` at the namespace scope
     fails with `ErrInvalid`. A request that adds it to `identity` succeeds,
     and the change then has both `identity` and `unclassified`.
3. **Review by risk.** The starter pack, switched to `enforce` in the
   test. **e2e:**
   - The change of `input.role` waits for `approval:security`.
   - `tofu taint` on a resource gives `destroy` and waits for
     `approval:owners`.
   - The change of `input.name` has no class, but the origin of `risk` is
     `runner`, so each `approval:` gate of the pack counts as matched, and
     the change waits. The decision record says "counted as matched:
     reads `risk`, origin `runner`".
   - The change of `random_pet` meets the warn of `risk-unclassified`, and
     `autoApply` is false.
   - A collection that saves a gate to exempt `risk-unclassified` fails
     (ADR 224 decision 6).
4. **Acknowledged changes.** The two keys, the acknowledge list and
   `ErrAcknowledgeMismatch`. **e2e:** a change order with two units. The
   approve request with one key fewer fails and records nothing. The
   request with each key succeeds, and the audit entry lists the keys and
   the origin. A request that deletes `identity` from the acknowledge list
   of a collection fails.
5. **The delta.** `since=<revision>`. **e2e:**
   - Revision 1 changes three resources, and a person signs it. Revision 2
     changes one of them again and adds one. The view with `since=1` shows
     one `changed` and one `new` row.
   - A sensitive variable changes from A to B in revision 1 and from A to
     C in revision 2. The view shows it as `changed`.
   - The step re-plans the signed revision with carry-over on, after a
     change of an acknowledged resource, and checks that the apply stops
     for a new approval.
6. **The Pulumi reader**, with the same classes. **e2e:** a Pulumi YAML
   program, as the e2e suite uses, with a scope addition for a resource
   type that the program declares. If RFC 122 option O11-b is accepted and
   lands, the same step checks that the origin of `risk` is `server` and
   that a change with no class then needs no approval from the pack.

## Questions for the owner

Each question stands alone. The recommendation is first.

### Question 1. How does the server mark a risky change?

- **A. Risk classes (recommended).** 12 named classes (destroy, replace,
  stateful, identity, network, exposure, protection, secret, unknown,
  lifecycle, drift, unclassified). A gate names the class, and the record
  says which class asked for the approval.
- **B. A score from 0 to 100.** A gate compares it with a number.
- **C. Nothing in the server.** Each team writes its own policy pack.

**ELI5.** Instead of "this box is 73 dangerous", the label says "glass"
or "heavy", so you know how to carry it.

### Question 2. Who decides which resource types are risky?

- **A (recommended).** The server ships a versioned risk catalog with a
  list `known` of the types that it knows. A type that the shipped catalog
  does not know is `unclassified`, with any change kind, and never
  auto-applies. A namespace, a collection or a unit can add a type or a
  path to a risk class, never to `known`, and never delete an entry. An
  upgrade only adds in a minor version. A person moves a scope to a new
  major version.
- **B.** Each namespace can also add types to `known`. This lowers a
  class: a namespace could make an unknown type auto-apply.
- **C.** No catalog. Only the change kinds (destroy, replace) count.

**ELI5.** The project gives everyone the same list of fragile things, and
anything not on the list counts as "we do not know yet". You can add to
the fragile list, but you cannot write "safe" on a box yourself.

### Question 3. Must a signer tick each high-risk change?

- **A (recommended).** Yes. The approve request lists each change with a
  class of the acknowledge list. The installation sets the floor of the
  list (default: stateful, identity, exposure, protection, unclassified),
  and a scope can only add to it. The approve button stays off until each
  one is ticked. The page says when the list came from the part `plan-display`
  that the runner wrote.
- **B.** Only where a gate asks for it.
- **C.** No. One signature for the whole plan, as now.

**ELI5.** Before the bank sends a large payment, you tick each line that
moves money out, not only "OK" at the bottom.

### Question 4. When can a change with no risk class skip its approval?

This RFC's answer does not depend on RFC 122 being accepted. RFC 122 is an
open proposal.

- **A (recommended).** Only when the server computed the classes from the
  plan that the apply runs (the origin `server`). Today that is never: the
  classes come from the part `plan-display` that the runner writes. For Pulumi it
  becomes possible if the server reads the update plan itself (RFC 122
  option O11-b, a proposal). For OpenTofu it stays never until the server
  computes the operation set itself. Until then, the classes sort the
  view, pick the approvers and give the check boxes, and every change
  keeps its approval. This holds for every source, also a protected
  branch, and also for the plans of a review request.
- **B.** As A for an untrusted source only, as RFC 122 proposes. A change
  from a trusted source with no class can skip its approval now. The risk:
  a program on a protected branch that writes a partial `plan-display` skips the
  approval, and the apply floor has no approved set to compare with.
- **C.** Never. Each change keeps its approval default, also with the
  origin `server`.

**ELI5.** A label on a box can send it to the guard, but a label that the
sender wrote can never let a box skip the guard. Only a label that we
wrote ourselves, after we looked inside, can do that.

### Question 5. Accept the recommendations of the earlier draft?

Each item names its section, the recommendation, and the other option.

| Item | Section | Recommendation | The other option |
| --- | --- | --- | --- |
| 1 | the whole RFC | Its own theme, with its own table, actions and audit | part of the plans theme |
| 2 | B | Exact change groups only in version 1. Similar changes later, as a read-only row | similar changes (B2) in version 1 |
| 3 | F | The view basis in the signature (F2), with the basis of `rules=none` always accepted | the rules in the digest (F1), which voids each signature at each rule change |
| 4 | E | Only a namespace administrator writes a noise rule, with a fresh authentication. A rule of the author or a late rule collapses nothing | more roles can write a rule |
| 5 | E | The longest expiry of a noise rule is 90 days, with at most 3 renewals | 30 or 180 days |
| 6 | E | A noise rule never changes a fact. Facts count every change | a collapsed update does not count in `plan.change` |
| 7 | D | `plan-diagnostics` from the one `tofu plan -json` run, if the e2e suite shows that its plan file applies. Else a second command | always a second command |
| 8 | "ADRs this RFC would amend" | The resume of a multi-unit apply goes to its own RFC, with the condition given there | decide the resume here |
| 9 | E | Ship the three built-in rule sets as data, none on by default | an example in the docs only |
| 10 | B | No collection view with no change order (S2) now | build S2 now |
| 11 | A | The Pulumi reader reads the summary, not the streaming events | read the streaming events for the diff kind of each path |

- **A. Accept all 11 (recommended).**
- **B. Accept some.** Name the items to discuss.

**ELI5.** These were already worked out. One "yes" lets the work start.

## Proposed glossary lines

**Risk class**:
One of a closed set of names that the server gives a resource change of a
plan, from its change kind, its resource type, its value paths and the risk
catalog. A gate reads the count of each class as a fact.
_Avoid_: Risk score, severity

**Risk catalog**:
Versioned data that maps resource types and value paths to risk classes.
The server ships it, with the list `known`. A scope can add entries to a
risk class, never to `known`, and never delete one.
_Avoid_: Risk rules, risk policy

**Change key**:
The content key of one resource change of a plan, with the prefix `chg-`
and the version of the review key. Two plans give the same key for the same
change, also for a changed secret value, which the key covers through a
keyed digest. An acknowledgment and the delta view use it.
_Avoid_: Change hash, change ID

**Pairing key**:
The tool, the unit ID and the resource key of a resource change. It finds
the same resource in two plans, so that the delta view can say "changed".
_Avoid_: Resource ID

**Noise rule**:
Data of a namespace, a collection or a unit that collapses matching value paths
in the review view of a change order. It has an owner, a reason and an
expiry. It never collapses a delete, a replace, a forget, an import, a move, a
path that forces a replace, a secret path, a path that is unknown after the
apply, or an error diagnostic. It never changes the plan, the digest or a
fact.
_Avoid_: Suppression, filter, fold (that is of the focus view), ignore rule
(that is of Sweep), exemption (that is of a gate), exception (that is of a
scanner finding)

**View basis**:
The SHA-256 of a canonical list. The list holds the sorted pairs (rule ID,
rule version, unit) that apply to the revision, the ID and version of each
built-in rule set that a scope of the revision turned on, and the version name
of the view encoding and the version of each tool reader. It describes the
review view that a signer saw. The server computes it again at the approve
request. A signature holds it.
_Avoid_: Render hash, view hash

## Outcome

Empty until the RFC is accepted or dropped.
