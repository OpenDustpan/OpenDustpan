---
rfc: 91
title: Plan review at scale, with one view of repeated changes across stacks and workspaces, and noise rules that expire
status: draft
created: 2026-10-20
---

# RFC 91. Plan review at scale, with one view of repeated changes across stacks and workspaces, and noise rules that expire

Status: draft. Nothing in this RFC is decided. No code exists for it. The
owner answers the questions at the end.

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
  `refresh`. A resource change gets the field `movedFrom` (section A).
- **ADR 194 decision 4.** "Needs attention" also holds each `forget`, each
  `import` and each move.

The resume of a multi-unit apply (ADR 190 decision 8) is not in this
list. It is an open question (question 8).

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
     from `previous_address`.
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
"replace" for Pulumi, "plan" and "must be replaced" for OpenTofu (ADR 195).
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
| OpenTofu any actions with `change.importing` present, also `["no-op"]` | `import` | Needs attention |
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
- An OpenTofu change with `importing` present has the kind `import`, also
  when its actions are `["update"]`. The row keeps the paths of the update.
  It is in "Needs attention" and never joins a change group.

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

**Fields** (proposal): `id`, `scope` (namespace, collection or unit),
`tool`, `match`, `owner` (a person or a group), `reason` (required),
`expiresAt` (required, at most 90 days after the create or the last
renewal), `createdBy`, `createdAt`, `version`.

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
count of collapsed paths and the rule ID, with no path and no value. It also lists each path that a Pulumi `hideDiffs` option names
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
  author can attach a new revision to use it.
- A person whose own rule collapses at least one row of a revision cannot
  sign that revision. "Own rule" has the same meaning as above: the person
  is in the write history of the rule (create, change or renewal). The
  server refuses the signature with a specific error, `ErrSelfApproval`
  (not `ErrPolicyDenied`), and writes an audit entry.

**Authorization and audit.**

- One action for the write of noise rules, as ADR 230 has one action,
  `namespace:manage-gates`, for all writes of gate configuration. Proposal,
  with the namespace as the object: `namespace:manage-noise-rules`. A
  collection scope or a unit scope is a field of the rule. A read of the
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
3. the version name of the view encoding (for example `cho-view/1`) and the
   version of the reader of each tool in the revision.

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
- `GET`, `POST .../namespaces/{namespace}/noise-rules`, and `GET`, `PATCH`,
  `DELETE .../noise-rules/{id}`. A collection scope or a unit scope is a
  field of the rule, as for gates (ADR 230).

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

## Open questions

Each question has a recommendation of the author. The owner decides.

1. Is this its own theme, or part of the plans theme? **Recommendation:**
   its own theme. It adds a new kind of data (noise rules) with its own
   actions, table and audit, and the plans theme is mostly decided.
2. Are similar changes (B2) in version 1? **Recommendation:** no. Version 1
   has exact change groups only (ADR 194). A later version adds similar
   changes as a read-only row, with no content key and no thread anchor, so
   no one-way door is opened.
3. Does a signature record the view basis (F2), or does the digest cover
   the rules (F1)? **Recommendation:** F2, with the view basis as section F
   defines it, including the rule that the basis of `rules=none` is always
   accepted.
4. Who can create a noise rule? **Recommendation:** only a namespace
   administrator. A rule that the author of a change order created or
   changed, or that was created or changed after a revision was attached,
   collapses nothing in that revision. A person who is in the write history
   of a rule that collapses a row of a revision cannot sign it. The
   action is `namespace:manage-noise-rules`, and a write needs a fresh
   authentication.
5. What is the longest expiry of a rule: 30, 90 or 180 days?
   **Recommendation:** 90 days, with at most 3 renewals of 90 days each.
6. Does a noise rule also apply to the facts of a gate, for example to not
   count a collapsed update in `plan.change`? **Recommendation:** no. Facts
   count every change.
7. Is the part `plan-diagnostics` worth a second command in each OpenTofu
   plan step, or can the plan step run `tofu plan -json` once and keep both
   the stream and the plan file? **Recommendation:** run it once, if the
   e2e suite shows that the plan file from the same run applies without
   change. Else keep the second command.
8. The resume of a multi-unit apply is out of this theme. ADR 190 decision
   8 says that the author attaches a new revision for the units that did
   not apply. Other options exist: resume in the same revision for a unit
   whose apply never started, or a resume revision that pins the commit of
   each code location. **Recommendation:** take this to its own RFC. If an
   option for a resume in the same revision is chosen there, it must rest
   on a fact that the server recorded (the apply job ended before the
   runner started the apply step), and it must run `job.CheckPlan`. Its
   guard `ErrPlanStale` already refuses a plan that read another state
   version than the current one.
9. Does the server ship built-in rule sets, or only an example in the docs?
   **Recommendation:** ship the three sets of section E as data, none on by
   default.
10. Is a collection view with no change order (S2) wanted later?
    **Recommendation:** not now. Decide when the change order view is in
    use.
11. Does the Pulumi reader read the streaming engine events instead of the
    summary, to get `diffKind` with `*-replace` on each path? The summary
    gives `replaceReasons` only as keys. **Recommendation:** no in version
    1. The summary has `detailedDiff`, and Needs attention does not depend
    on the paths.

## Proposed glossary lines

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
The SHA-256 of the list of active noise rules, the built-in rule sets, and the
view encoding and reader versions that rendered the review view that a signer
saw. The server computes it again at the approve request. A signature holds
it.
_Avoid_: Render hash, view hash

## Outcome

Empty until the RFC is accepted or dropped.
