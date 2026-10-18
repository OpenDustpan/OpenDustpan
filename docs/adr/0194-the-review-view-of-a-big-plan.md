# 194. The review view of a big plan

Status: accepted · 2026-10-18 · part of ADR 190

## Context

A change of one shared local can change hundreds of resources in many
units. A reviewer who reads each resource change one by one misses the one
delete in a list of 300 tag changes. The owner decided how the review shows
a big plan (owner):

- The cause diff comes first: what the author changed, for example a
  shared local. Then comes the effect.
- Replacements and deletes are never in a group. The review shows them as
  "needs attention".
- Identical in-place changes are in a group.
- A thread can have a group as its anchor.
- The review shows the blast radius and the policy results.

Facts:

- ADR 13 keeps the tool models apart. The readable plan of a stored plan
  is the JSON plan of the tool: `tofu show -json` or `pulumi preview
  --json` (ADR 102 decision 2). Only the tool package knows its format.
- ADR 41 masks the values that the tool marks as sensitive. ADR 150
  decision 6 compares secret values inside the server by digest, and never
  answers the digest.
- ADR 162 gives the blast radius of a unit: the units that read its
  outputs, in N hops downstream.
- ADR 114 gives the decisions of the policy engines. RFC 13 says that
  resource policy runs where the plan is, in the process of the tool.

## Decision

1. **Two sections, in this order.** The review view of a revision
   (`changeorder.View`) has the cause first, then the effect (owner). The
   console shows them in this order, and the API answers them in this
   order:
   1. The cause diff (decision 2).
   2. The summary: for each unit, the count of each change kind.
   3. Needs attention (decision 4).
   4. The groups of identical in-place changes (decision 5).
   5. The other changes, for each unit.
   6. The blast radius (decision 7).
   7. The policy results (decision 8).

   The threads show next to their anchors in each section.
2. **The cause diff.** The cause diff is the change of the code that the
   author made: the file diff from the base commit to the head commit of
   each code location of the revision. The base commit is the commit of
   the last state version of the unit that a job wrote, when the job
   record names one. Else the author gives it at the attach, and the
   attach checks that it is an ancestor of the head commit (ADR 191
   decision 2). The view and the API label the source of each base:
   `baseSource` is `job` or `author`, and the console shows "Base given by
   the author" for `author`. A reviewer then knows that the author chose
   the start of the diff. The port
   `changeorder.CauseDiffs` reads it from the forge (ADR 196 decision 3).
   The server does not store the cause diff. The forge is its home, and
   the server reads it again at each read, with a cache in memory of at
   most 5 minutes. A diff above 1 MiB, or above 300 files, answers the
   first part with the mark `truncated`, and a link to the compare page of
   the forge. A unit with no known base commit answers the mark
   `no-base`.
3. **The effect, for each unit, from its tool.** Each tool package
   implements the port `changeorder.PlanReader` for its readable plan
   (ADR 196 decision 4). It reads the part `plan-display` and answers a
   tool-neutral summary (`changeorder.UnitSummary`): one
   `changeorder.ResourceChange` for each resource that the plan changes.
   A resource change holds the key of the resource (ADR 193 decision 3),
   the type, the change kind, the value paths that change, and the masked
   values before and after. The change kinds are `create`, `update`,
   `replace`, `delete` and `read`. A resource with no change is not in the
   summary. The core never parses the plan of a tool.
4. **Needs attention.** Each `replace` and each `delete` is in the section
   "Needs attention", one by one, and never in a change group (owner). A
   `destroy` operation of a unit puts each of its resources there. The
   section comes before every change group, also when it holds one
   resource. The section also holds:
   - each unit whose summary is `unreadable` (decision 6), as a whole, with
     the error and the link to its readable plan,
   - a warning for each pair of units of the change order where one unit
     reads the outputs of the other (decision 7). The apply jobs have no
     order, so the unit that reads can apply first.
5. **Change groups of identical in-place changes.** Two `update` changes are
   identical when they have the same tool, the same type, the same set of
   value paths that change, and the same values before and after on each
   path. A change group (`changeorder.ChangeGroup`) holds two or more
   identical changes, from one unit or from several units of the same tool.
   A change that touches a secret path is never in a change group: a secret
   path is a value path whose value before or after the tool marks as
   sensitive (ADR 41). The view lists such a change one by one, in the other
   changes of its unit. The key of a change group is a content key, not an
   ID: the store derives it from the change, and never assigns it. It has
   the prefix `cog-`, which is not the prefix of the groups of principals,
   and it is the hex of the first 16 bytes of the SHA-256 of the tool, the
   type, the sorted value paths and the values before and after. These
   values are never secret, so the key covers no secret value. The key is
   not a digest of secret content (ADR 150 decision 6), and it tells nothing
   about a secret value. The key is stable from one revision to the next for
   the same change, and a thread can follow a change group (ADR 193 decision
   7). A `create`, a `replace`, a `delete` and a `read` are never in a
   change group. The console shows a change group as one row with its count,
   and opens it to list each resource.
6. **The analysis runs once for each unit plan.** When a unit plan becomes
   `ready`, the store starts the durable operation `changeorder.analyze`
   with the start key `<change order ID>/<revision>/<unit ID>`. The
   operation reads the part `plan-display`, calls the `PlanReader` of the
   tool, and writes the result in two parts:
   - **Metadata in SQL**, for each resource change: the unit ID, the key,
     the type, the change kind, the count of value paths that change and
     the group key. No value and no value path. This follows the rule of
     a resource change row of blame (ADR 150 decision 6).
   - **Content in the blob store**: the value paths and the masked values.
     The server seals it with the item key of the stored plan that it came
     from (ADR 151 decision 1). So a redaction or the end of the retention
     of the plan shreds the content of its summary too (ADR 198 decision
     2).

   The revision becomes `ready` only after each analysis ended. A
   `PlanReader` that fails, for example on a plan format that it does not
   know, marks the summary of that unit `unreadable` with the error. The
   revision is still `ready`. The checks fail closed:
   - The view puts the unit in "Needs attention" as a whole (decision 4),
     with the link to its readable plan. No change of that unit is in a
     change group.
   - The policy input of ADR 192 decision 4 marks the unit with
     `unreadable: true` and gives no counts of change kinds for it. A
     policy never reads a missing count as 0. The change kinds of an
     unreadable unit never count as safe.
   - When the unit plan has the part `policy-report`, that report counts as
     unreadable too (decision 8).
   - The unreadable summary blocks the approval of the revision, also with
     no policy, with `changeorder.ErrSummaryUnreadable` (ADR 192 decision
     4). An author attaches a new revision.
7. **The blast radius.** For each unit of the revision, the view shows its
   blast radius from the graph (ADR 162): the units that read its outputs,
   2 hops downstream by default. A unit that the reader cannot read counts
   as hidden. The view shows the owners of each unit (ADR 161). The view
   marks each unit of the blast radius that is also a unit of the change
   order, because the apply jobs have no order (ADR 190, "Consequences").
   Each such pair also gives a warning in "Needs attention" (decision 4).
8. **The policy results.** The view shows two kinds of results:
   - **The edit policy:** the decision of each engine at the decision
     point `changeorder.review` (ADR 192 decision 4), with its effect, its
     reasons and its policy version.
   - **The resource policy:** the report that a plan job gives when its
     steps run a resource policy. The job model gets a third, optional part
     of a stored plan, `policy-report`: a JSON document with one result for
     each rule, with the rule name, the enforcement (`advisory` or
     `mandatory`), the outcome (`pass` or `fail`), the key of the
     resource, if any, and a message. The tool package of the unit reads
     the format of its tool into this shape, through `PlanReader`. The
     server never runs a resource policy (RFC 13). A `mandatory` result
     with the outcome `fail` blocks the approval of the revision with
     `changeorder.ErrPolicyDenied`, as a `deny` of the edit policy does.
     A report that the `PlanReader` cannot read blocks the approval of the
     revision with `changeorder.ErrPolicyUnreadable`, which names the unit
     and the error. An unreadable report never counts as a pass. The
     author attaches a new revision.
9. **Who reads what.** The view needs `changeorder:read` on each unit of
   the change order. The masked values of a unit need `job:read-plans` on
   that unit, as the readable plan does (ADR 102 decision 8). A reader
   without it sees the metadata of the unit: keys, types, change kinds and
   counts. No route answers an unmasked value in the view. A person who
   needs one reads the readable plan of the unit with `unit:read-secrets`
   (ADR 102 decision 8). The cause diff needs `changeorder:read` only.

## Consequences

- A reviewer reads one cause and the list of resources that need
  attention before the long list of in-place changes.
- The tool packages carry the cost of each tool format. A new tool
  implements `PlanReader` and runs its conformance suite.
- The summary of a plan expires with the plan, and its metadata stays
  until the purge of the unit, as for stored plans (ADR 152 decision 7).
- The job model changes: a stored plan can have the part `policy-report`
  (ADR 102 decision 2 gets a third part kind). It is not `secret`, and it
  has the item key of its plan.

## Open questions

- Does the cause diff come from the forge only, or also from a source
  snapshot of the runner (ADR 135) when the forge is not reachable?
- Does a group span units of two namespaces? A change order has one
  namespace (ADR 190 decision 3), so version 1 never does.
- Does the review show a change of an output of a unit as its own kind,
  so a reviewer sees the effect on the units of the blast radius?
