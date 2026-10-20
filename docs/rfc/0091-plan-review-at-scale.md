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
replacement, and it misses an attribute change that forces a later
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

So this RFC adds four things on top: a view that collapses repeats that are
similar but not identical, set and diagnostic handling, noise rules, and
the binding of a signature to the noise rules that rendered the view.

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
- `replace_paths` lists the attribute paths that forced the replace. It is
  omitted when the action is not a replace.
- `action_reason` values include `replace_because_tainted`,
  `replace_because_cannot_update`, `replace_by_request`,
  `replace_by_triggers`, `delete_because_no_resource_config`,
  `delete_because_count_index`, `delete_because_each_key` and
  `read_because_config_unknown`. The docs say these are display hints, and
  a consumer must treat an unknown value as unspecified.
- **Unknowns.** `after_unknown` has the shape of `after`, with `true` at
  each unknown leaf.
- **Sensitive values.** `before_sensitive` and `after_sensitive` have
  `true` at each sensitive leaf. The docs say they "should be combined with
  "before" and "after" to prevent accidental display of sensitive values".
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
  and `byte`) and an optional `snippet` of the source
  (<https://opentofu.org/docs/cli/commands/validate/>).

### Pulumi CLI preview JSON (`pulumi preview --json`)

Sources: <https://www.pulumi.com/docs/iac/cli/commands/pulumi_preview/>,
and the source of `pulumi/pulumi` on `master`: `pkg/display/json.go`,
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
- **Secrets.** The JSON display replaces each secret property with the
  string `[secret]` (`MassageSecrets`). The CLI masks before the server
  sees the part.
- **Diagnostics** (`PreviewDiagnostic`): `urn`, `prefix`, `message` and
  `severity`. There is no source location. The streaming
  `DiagnosticEvent` adds `color`, `streamID` and `ephemeral`. The summary
  drops ephemeral and debug messages.
- **Policy events.** The source says: "At this point in time, we don't
  handle policy events in JSON serialization". RFC 90 covers them.
- **`hideDiffs`.** A resource option that "specifies a list of property
  paths whose diff details Pulumi compacts in CLI output". It "does not
  affect what resources are updated, only how those updates are displayed"
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
   (`toolAction`, `toolReason`) for display only.
   - OpenTofu: `forcesReplace` from `replace_paths`, `unknownAfter` from
     `after_unknown`, `secret` from `before_sensitive` or
     `after_sensitive`, `toolReason` from `action_reason`.
   - Pulumi: `forcesReplace` from `replaceReasons` and the `*-replace` diff
     kinds, `unknownAfter` from the placeholder string, `secret` from
     `[secret]`, `toolAction` from `op`.
3. **A tool-specific view for each tool.** The console renders each tool's
   own model. Shared views (change groups across tools) do not exist.

**Recommendation: A2.** The core never parses a plan (ADR 194 decision 3),
and the core gets only flags it can act on. User-facing text keeps the
words of each tool: "preview" and "replace" for Pulumi, "plan" and
"must be replaced" for OpenTofu (ADR 195). `toolAction` and `toolReason`
are display strings, never input to a rule.

**The order of the view** (an extension of ADR 194 decision 1):

1. Needs attention: each `delete`, each `replace`, and (new) each
   `update` with a path that has `forcesReplace` in a later step, each
   OpenTofu `forget`, each `previous_address` (a move), and each unit with
   `errored: true`.
2. Error diagnostics (section D).
3. Change groups and similar changes (section B), largest first.
4. The other changes, by unit, then by resource address or URN, then by
   value path.
5. Warning diagnostics, counted.

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
   same kind of change on each path (add, remove, update). The values can
   differ. The view shows one row "similar change", with the distinct
   value pairs and the count of each, for example `prod` (10), `dev` (10),
   `stage` (10).
3. **Normalized values.** As B2, but the reader replaces known
   per-unit tokens (the name of the stack or workspace, the namespace) with
   a placeholder before an exact compare.
4. **Do nothing.** Each unit shows its own list.

**Recommendation: B1 and B2 together.** Exact change groups stay as ADR 194
gives them. A similar change holds two or more exact change groups, or
single changes, of the same shape. B3 is not recommended: a wrong guess of
a token makes two different changes look the same.

Rules for a similar change:

- It never holds a `create`, `delete`, `replace`, `read` or `forget`, or a
  path with `forcesReplace` or `secret`. This is the rule of ADR 194
  decision 5, applied again.
- An unknown value is the token `(known after apply)` in the list of
  values. Two unknowns are never an exact match, so they never join an
  exact change group. They can join a similar change.
- Its key is a content key, as for a change group, with a new prefix (for
  example `cos-`) and the same HMAC key of the namespace. The key covers
  the tool, the type and the sorted pairs of path and change kind. **One-way
  door** once a thread can anchor on it (question 4).
- **Limits** (proposal): at most 20 distinct value pairs show in the row,
  then "and N more values", with a link to the full list. At most 10,000
  members in one similar change. A larger set splits by unit.

**How the reviewer still sees every unit:**

- The row shows the count of resources and the count of units, and opens
  to a list by unit, then by resource. The list pages, and it never cuts.
- The summary of ADR 194 (the count of each change kind for each unit)
  counts every change, also the grouped ones.
- A view "by unit" lists each unit with all its changes. The grouping is a
  way to read, not a filter.
- A unit that has a change outside all groups gets the mark "has other
  changes" in the row.

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
- Pulumi: `message` with the URN removed from the text, and `prefix`. The
  Pulumi CLI gives no source location, so each distinct URN counts as one
  location, and the row says "by resource".
- The row shows the count, the count of distinct locations, the count of
  units, and the first 20 locations, then "and N more".
- An error diagnostic is never in a count only. Each error group is in its
  own section, before the change groups.

### E. Noise rules

A **noise rule** is data of a namespace that folds a part of the view.
It never changes the plan, the apply, the digest or a fact.

**Fields** (proposal): `id`, `scope` (namespace, collection or unit),
`tool`, `match`, `owner` (a person or a group), `reason` (required),
`expiresAt` (required, at most 90 days after the create, as a proposal),
`createdBy`, `createdAt`, `version`.

**What a rule can never fold.** The server refuses a rule at save that can
match only these, and the reader ignores a match on these at render:

- a `delete`, a `replace`, an OpenTofu `forget`, or a move,
- a path with `forcesReplace`,
- a path with `secret` (a masked value has no value to read, so a value
  condition cannot be checked on it),
- an error diagnostic,
- a whole resource. A rule folds paths. A resource whose every changed path
  is folded shows as one row "only folded changes (N paths)", and it still
  counts in the summary.

**Expiry.** At `expiresAt`, the rule stops folding. The view then shows the
paths with the mark "rule X expired on <date>". An expired rule stays as a
record until a person removes it, so the mark can name it.

**Fail open (show more).** When a rule cannot be evaluated (an unknown
match kind after a downgrade, a parse error of a stored rule, a value
condition on an unknown value, a timeout), the reader folds nothing for
that rule, and the view marks it "rule X not applied: <reason>".

**The explain view.** For each revision, a list of each folded path: unit,
resource, path, the rule ID and version, its owner, its reason and its
expiry. It needs the same actions as the view. It also lists each path that
a Pulumi `hideDiffs` option names (section "Pulumi CLI preview JSON"). The
view does not apply `hideDiffs`: the author of the change controls it, so
it is not a rule of the reviewer. The explain view lists it as "hidden by
the program" and the path shows.

**Authorization and audit.**

- A new action to create a rule, apart from the actions of gates and change
  orders, for example `noiserule:create` on the scope. A change, a renewal
  and a removal need it too. A read needs `changeorder:read` on the scope.
- No built-in role gets `noiserule:create` except the namespace
  administrator, as a proposal (question 5).
- Each create, change, renewal and removal writes one audit entry with the
  rule, the old and the new version, and the reason. An expiry writes one
  audit entry with the actor `@server`.
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

**Recommendation: E2.** E1 cannot say "fold a timestamp tag, but show any
other value of that tag". E3 and E4 are hard to explain in the explain
view, and they can read values. E2 has no user-written regular expression,
so the cost of an evaluation stays bounded.

**Built-in rule sets.** The server ships rule sets as data, with a version,
for example "timestamp tags", "provider default tags" and "same members,
other order". No set is on by default. A person turns a set on for a
scope, and that act is a rule record with an owner, a reason and an expiry,
as above. An upgrade that changes a built-in set gives it a new version.
A scope keeps the version it turned on until a person moves it, so an
upgrade never folds more on its own.

### F. The approval binds to the stored plan and to the rule set version

The signature already binds to the digest of the stored plans (ADR 191).
The view is not the plan, so the server never signs a rendering. The
question is how the signature also records which rules rendered the view.

1. **Add the rule set version to `cho-digest`** as a new encoding
   `cho-digest/2`. Each change of a rule in the scope then changes the
   digest and voids each signature. **One-way door.**
2. **A second value in the signature, the view basis.** The view answers a
   `viewBasis`: the SHA-256 of a canonical list of the rule ID and version
   of each rule that applied, with a version name such as `cho-view/1`. The
   approve request names the digest and the view basis. A view basis that
   is not the current one fails with a new error, for example
   `ErrViewChanged`, and records nothing. The signature keeps it.
3. **Record nothing about the view.**

**Recommendation: F2.** The plan stays the only thing that the apply reads.
A rule change after a signature does not void the signature, because the
plan did not change and the signer saw the rules of that time. The audit
shows what each signer saw. A rule change before a signature makes the
signer read the new view.

**The apply runs exactly that stored plan.** Nothing new: the apply job
names its stored plan, `job.CheckPlan` runs at the dispatch, and the runner
refuses a part with another hash (ADRs 102, 190). The view, the rules and
the folds are never input to a job.

**Resume of a multi-unit apply.** A change order ends `partially-applied`
when some apply jobs fail (ADR 190 decision 7). Options:

- **R1. As decided:** the author attaches a new revision for the units that
  did not apply, and the reviewers sign again.
- **R2. Resume in the same revision:** the server dispatches again the apply
  job of each unit whose apply job changed nothing (it ended before the
  runner started the apply step, for example after the loss of a runner),
  with the same stored plan, when `job.CheckPlan` still passes. A unit
  that ended `plan-stale` cannot pass that check, so it needs R3. No new
  signature, because the digest is the same. A unit whose apply started
  is never resumed this way.
- **R3. A resume revision with pinned commits:** as R1, but the attach takes
  the commit of each code location from the approved revision, and
  refuses a unit plan of another commit with `ErrInvalid`. So the new
  plans never mix commits.

**Recommendation: R3, with R2 for the narrow case of an apply that changed
nothing.** R3 keeps "one commit for each code location in one revision".
R2 saves a review round when a runner was lost before the apply started.

### G. Plan JSON is untrusted input (ADR 250)

The part `plan-display` comes from a runner that ran code of the team. The
code can write any string into a resource name, a tag, a value or a
diagnostic.

| Layer | What it does | Its test |
| --- | --- | --- |
| Check at the boundary | The reader streams the part with a decoder that has limits. The part has at most `job.MaxPlanPart` bytes (256 MiB). The nesting depth is at most 64, and a string has at most 1 MiB. A plan has at most 100,000 resource changes (ADR 196 decision 4), and a resource at most 10,000 paths. The reader checks the schema. For OpenTofu, `format_version` must have the major version `1`. For Pulumi, the fields must be the known fields of `PreviewDigest`. An unknown major version, a bad type or a limit gives `ErrPlanFormat`. The reader never changes a document to make it valid. | Fuzz each reader. One test for each limit at the limit and at the limit plus one. |
| Encode at use | The console escapes each string. The Markdown output escapes Markdown and HTML. It removes CR and LF from a table cell, puts each value in a code span, and breaks `@` mentions and links. Logs remove CR and LF. Bidirectional control characters show as escapes. SQL takes placeholders only. | A hostile name, path and value through each renderer, with the reader checks skipped. |
| Limit the effect | The analysis runs in the durable operation of ADR 194 decision 6, with a time limit and a memory limit. The server parses a part once and keeps the summary. A rule has a bounded cost (no user regular expression). | A plan at each limit, and a rule set at its count limit. |
| Authorize at use | Values need `job:read-plans` on each unit (ADR 194 decision 9). The explain view and the Markdown output need the same actions as the view. The store checks the action of a rule on its scope in the transaction of the write. | A rule write with a token of another scope, and a view read of a unit with no `job:read-plans`, with the route check skipped. |
| Fail closed and record | A plan that the reader cannot read is `unreadable` and blocks approval (ADR 194 decision 6). A rule that cannot be evaluated folds nothing (fail open for display, which is the safe side). Each refused rule write writes an audit entry. | An unreadable plan gives no signature. A broken rule shows each path. |

**Masking is kept.** The reader masks before it computes a fingerprint, a
group or a fold. A rule never unmasks. A literal value `[secret]` in an
OpenTofu plan is a plain string, and the reader does not treat it as a
mark. The `plan-diagnostics` part drops `snippet`, because an excerpt of
the source can hold a value.

### H. The API, the console and Markdown

REST API first (ADR 17), under `/api/dustpan/v1` (ADR 210). Paths are
proposals:

- `GET .../namespaces/{namespace}/change-orders/{id}/revisions/{rev}/view`
  gets the new sections and `viewBasis`. The query `rules=none` answers the
  view with no fold. Folds never apply to the facts of a gate.
- `GET .../revisions/{rev}/view/explain`: the explain view.
- `GET .../revisions/{rev}/view` with `Accept: text/markdown`: a Markdown
  rendering with a size limit and a link to the full view. The CI shim
  theme (#1091) posts it as a comment. Markdown is a rendering. It is never
  a basis of a signature.
- `GET`, `POST .../namespaces/{namespace}/noise-rules`, and `GET`, `PATCH`,
  `DELETE .../noise-rules/{id}`. A collection scope or a unit scope is a
  field of the rule, as for gates (ADR 230).

The console shows the view in the order of section A, with "folded by
rule X" on each folded row and a switch "show folded changes".

### I. The first slice

**Persona.** A platform engineer adds a common tag to the default tags of
one shared module. The change order holds 30 OpenTofu workspaces. In one
workspace, the same change also replaces one resource.

**What the slice holds:**

1. The OpenTofu `PlanReader` with the flags of A2.
2. The view of ADR 194 with exact change groups across units, the "by
   unit" list, and section A order. No similar changes, no noise rules, no
   new parts.
3. The route `GET .../revisions/{rev}/view`.

**The e2e step.** The e2e suite drives the real OpenTofu CLI. It makes 3
workspaces from one module (not 30, for the run time of the suite), plans
the tag change in a change order, and reads the view. It checks that the
view has one change group with 3 units, and no other change. It then adds
a change that forces a replace in one workspace, attaches a new revision,
and checks that "Needs attention" comes first and holds that one resource
with its `replace_paths`.

This slice needs the change order store and its plan jobs. If they are not
on `main` before this slice, a smaller first slice is the reader and the
view of one stored plan of one job (`GET .../jobs/{job_id}/plan/view`),
with the same e2e step on one workspace.

## Open questions

1. Is this its own theme, or part of the plans theme? The author proposes
   its own theme, because it adds a new kind of data (noise rules) with its
   own action and audit, and because the plans theme is mostly decided.
2. Is a similar change (B2) worth its cost in version 1, or do exact change
   groups (ADR 194) cover the common-tag case well enough?
3. Does a signature record the view basis (F2), or does the digest cover the
   rules (F1)?
4. Can a thread anchor on a similar change? If yes, its key encoding is a
   one-way door.
5. Who can create a noise rule: only a namespace administrator, or also a
   holder of `changeorder:approve`? Can a person approve a change order that
   a rule of their own folds?
6. What is the longest expiry of a rule: 30, 90 or 180 days? Can a person
   renew a rule, and how many times?
7. Does a noise rule also apply to the facts of a gate, for example to not
   count a folded update in `plan.change`? The author proposes no: facts
   count every change.
8. Is the part `plan-diagnostics` worth a second command in each OpenTofu
   plan step, or can the plan step run `tofu plan -json` once and keep both
   the stream and the plan file?
9. Does the server ship built-in rule sets, or only an example in the docs?
10. Is a collection view with no change order (S2) wanted later?
11. Does the Pulumi reader read the streaming engine events instead of the
    summary, to get `diffKind` with `*-replace` on each path? The summary
    gives `replaceReasons` only as keys.
12. What is the size limit of the Markdown rendering?

## Proposed glossary lines

**Noise rule**:
Data of a namespace, a collection or a unit that folds matching value paths
in the review view of a change order. It has an owner, a reason and an
expiry. It never folds a delete, a replace, a path that forces a replace, a
secret path or an error diagnostic. It never changes the plan, the digest or
a fact.
_Avoid_: Suppression, filter, ignore rule (that is of Sweep), exemption
(that is of a gate), exception (that is of a scanner finding)

**Similar change**:
Two or more in-place changes in a plan revision with the same tool, type,
value paths and kind of change on each path, and values that can differ. It
holds exact change groups and single changes.
_Avoid_: Fuzzy group, near duplicate

**View basis**:
The SHA-256 of the list of noise rules, with their versions, that rendered
the review view that a signer saw. A signature holds it.
_Avoid_: Render hash, view hash

## Outcome

Empty until the RFC is accepted or dropped.
