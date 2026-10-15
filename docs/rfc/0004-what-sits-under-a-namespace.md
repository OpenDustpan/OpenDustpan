---
rfc: 4
title: What sits under a namespace
status: discussing
created: 2026-10-10
adr: 51, 61           # the collection part only
---

# RFC 4. What sits under a namespace

Status: discussing. The collection part is accepted as ADR 51 on
2026-10-13. How a unit from the CLI gets its collection is accepted as
ADR 61 on 2026-10-13. The rest stays open.

## Problem

The console design shows a namespace, then one list for each tool: Stacks for
the Pulumi CLI, Workspaces for OpenTofu, and an inventory on top. That layout
came from the Pulumi Cloud console, not from a decision.

- The navigation sorts units by tool. A tool is how a unit is deployed, not
  what it is to the team that owns it. Each new tool adds a section.
- Both tools have a project, but the console hides it as a prefix in a name
  (`acme/payments/api/staging`). The two projects also mean different things
  (ADR 13, `GLOSSARY.md`).
- The most common questions have no home: "what runs in production?" and
  "what does the payments team own?".

## Constraints

- The namespace is the unit of tenancy, home region and fencing (ADR 4). It
  stays the only hard container. Any level under it is soft: cheap to
  rename, and not replicated or fenced on its own.
- Tool models stay separate (ADR 13). Views across tools read the inventory.
- A shared level cannot be called "project", because both tools use that word
  with a different meaning.

## Options

1. **Tool first (the current design).** Under the namespace, each tool shows
   its own hierarchy. It adds no concept and follows ADR 13 exactly. It
   splits the namespace by tool and grows with each tool.
2. **A shared project.** One project level for every tool. It reads well, but
   it gives one word two meanings and merges what ADR 13 keeps apart.
3. **A new grouping of our own** (a "system" or "service") that holds units
   from any tool. It matches how teams think. It is a new domain concept with
   assignment, renames and access rules, and it pulls in teams and roles
   before RFC 2 settles them.
4. **No hierarchy, only facets.** The namespace is the only container. The
   inventory is the start page and filters by tool, by each tool's project,
   and by tags such as `env` and `team`. Pulumi stacks and TFE workspaces
   already carry tags, so the inventory can hold them without a new model.
   Each tool keeps its own hierarchy on its own pages and in its URLs.

## Leaning

Option 4 now, with option 3 open. It adds the least, fits the inventory of
ADR 13, and answers "what runs in production?" from the start. If access
rules later need a group, RFC 2 is the place to design option 3, and the
facets show which grouping people use. The cost: the tool sections in the
navigation go away or become secondary.

## Open questions

- What does the first screen of a namespace answer?
- Is environment a concept of its own, or only a tag?
- Do teams own units directly, or through a group?
- Can a view span namespaces? Two namespaces can have two home regions, so
  such a view touches data residency.
- Which tags does the inventory index: all of them, or a fixed set?

## Gaps from the competitor review

Added 2026-10-10 from the research doc "IaC and TACOS landscape". Not yet
worked into the proposal above.

- An owning team per unit, so that approvals and alerts go to the owner
  (Resourcely, Terramate).
- Attach profiles, policies and runner labels to units by label (Spacelift
  `autoattach:`), and bulk tagging (env zero).
- Folders as prefixed labels, so that one unit sits in many groups
  (Spacelift).

## Owner input, 2026-10-11

The project owner asks for a grouping of units that teams manage
themselves. The namespace is an operator construct. Teams still want to group
their stacks and workspaces in a way that matches their work, and to use
that group for three things:

- access rules (who may read, plan, apply or approve),
- attaching profiles (RFC 5),
- output sharing (RFC 15): "every unit in this group may read my outputs".

This moves the leaning from option 4 towards option 3. Facets stay, for
finding and filtering.

### A sketch of option 3: groups

- A **group** is a named set of units inside one namespace, from any tool.
  Team leads make and change groups. Operators do not have to.
- A group is soft (see Constraints): no home region, no fence, cheap to
  rename. A unit keeps its own name and URL in its tool. A group is never a
  prefix of a unit name.
- Groups do not nest (owner input, 2026-10-11). TFE projects are flat, and a
  group name follows the TFE project name rules from the start: letters,
  digits, inner spaces, `-` and `_`, 3 to 40 characters, unique in the
  namespace without regard to case. So every group maps to a TFE project with
  no translation.
- Access rules, profile attachments and output sharing can name a group as
  their object, beside a single unit or the whole namespace (ADR 17 actions
  stay the same: the group only widens the object).

### Membership: explicit, not by label

If a group used labels as its membership rule, anyone who can edit a unit's
labels could move the unit into a group with wider rights or into a group
that reads secret outputs. So a group that carries access rules, profiles or
output sharing has explicit members:

- Adding a unit to a group needs a right on the unit and a right on the
  group. Removing it needs the right on the group.
- Each change is audited and passes the policy check, like any write.
- Labels stay useful for views. A saved view (an inventory filter) can look
  like a group, but it never grants anything.

### Groups and the tools' projects

Both tools have a "project", but they are different things:

- An **OpenTofu project** (in the TFE protocol) is an organizational
  container of workspaces, with team access and variable sets. The `cloud`
  block can name it. That is almost exactly a group.
- A **Pulumi project** is a program. Its name is in `Pulumi.yaml`, it is part
  of every stack name (`<org>/<project>/<stack>`), and only the code can
  change it. One Pulumi project often holds `dev` and `prod` stacks, which
  need different access. So a Pulumi project cannot be the group.

A possible mapping, like namespace and organization (each tool calls the
namespace an organization):

- The TFE-compatible API shows each group as a project, so `tofu` and the
  TFE protocol see their own word, and a workspace that names a project in
  its `cloud` block lands in that group.
- A Pulumi stack has a group as OpenDustpan metadata, beside its Pulumi
  project. A new stack could get the group named like its Pulumi project
  when that group exists, and a person can move it later.
- The console keeps each tool's words: "project" on OpenTofu pages, "Pulumi
  project" on Pulumi pages, and our own word for the group in shared views.

### How a Pulumi stack gets a group

The Pulumi CLI has no field for a group. `pulumi stack init` sends only
`<org>/<project>/<stack>`. So the server places the stack:

1. **Placement rules** in the namespace match the Pulumi project and the
   stack name, first match wins. For example: project `payments`, stack
   `prod*`, to group `payments-prod`; project `payments`, any stack, to group
   `payments`. Rules by stack name are what split `dev` from `prod` inside
   one Pulumi project.
2. The server then checks that the caller may create units in that group. The
   CLI gets an ordinary error if not, which names the group and the rule.
3. No rule matches: the namespace's default group, or a refusal, as the
   namespace chooses.
4. A stack made by a blueprint, the console or the API names its group
   directly.
5. A person with rights on both groups can move a stack later. The move shows
   what changes first: access, profiles, and which output reads stop.

After that, the CLI sees no difference. `pulumi up`, `pulumi stack ls` and
stack references work as before, and the group only decides who may do what,
which profiles attach and who may read outputs.

Open: can a stack tag (if the CLI sends one at creation) name the group, as a
request that the server still checks? Needs a check with `make e2e`.

### Open questions for groups

The owner answered every question of this list. See "Owner direction
(2026-10-12)".

### Open questions for collections

ADR 51 accepts the collection. These questions stay open after the owner
direction of 2026-10-13. The project lead decided the rights to move a unit,
the syntax of a stack-name pattern and the effect of a rule change (see
Outcome). Each "lead proposal" is a proposed default from the
project lead for the owner to decide. It is not a decision.

- How does the server give a collection to the units that exist before
  ADR 51? Lead proposal: an upgrade step makes no default collection. The
  server refuses writes to such a unit until an administrator moves it into
  a collection, and the error names the fix.
- (e2e) Can a stack tag that the CLI sends at creation name the collection,
  as a request that the server still checks?

## Owner direction (2026-10-12)

The project owner answered questions in this RFC on 2026-10-12. Words in
quotation marks are the words of the owner. A question that the owner sent to
research or to a spike stays in "Open questions", with a mark. This section
decides nothing. An ADR records a decision (ADR 15).

- Question "The word": "I guess collection sounds good."
- Question "Can a unit be in more than one group?": "No."
- Question "How do rules from two groups combine": "See above."
- Question "Does a blueprint (RFC 14) choose the group of the unit it makes":
  "It optionally can, and it can optionally prevent changing it."
- Question "Does the Kubernetes operator (RFC 31) map a Kubernetes namespace to a
  group": "No we should not make that assumption."

## Owner direction (2026-10-13)

The project owner answered a question of RFC 19 and of this RFC on
2026-10-13. Words in quotation marks are the words of the owner.

- Question "Can a role be granted on a label?": "Collections only". Grants go
  on a namespace, a collection or a unit. Labels are for views and filters,
  and they never grant access.

This answer replaces the owner's answer "Yes." of 2026-10-12 in RFC 19 to the
question "Can a role be granted on a label (RFC 4)". A label never grants
access. The section "Membership: explicit, not by label" above already said
that a saved view never grants anything.

Second round, through the project lead, on 2026-10-13:

- Question "Must each unit be in a collection, or can a unit be in no
  collection?": "Yes, and no default collection. Users create one." So each
  unit is in exactly one collection, and no default collection exists.
  ADR 51 derives from this that the server refuses a new unit when it
  cannot find its collection, with an error that names the fix.

Third round, through the project lead, on 2026-10-13:

- Questions "Does the owner accept the sketch for names and TFE projects?"
  and "Does the owner accept the placement rules for a new Pulumi stack?":
  the owner chose "Placement rules + TFE project (Recommended)". The text of
  that option was: "Namespace rules match Pulumi project/stack-name
  patterns; OpenTofu's `cloud` block `project` names the collection. No
  match: refuse with an error naming the fix." ADR 61 records the decision.
  The question on the rights to move a unit stays open.

## Outcome

The collection part is accepted as ADR 51 on 2026-10-13. A collection is a
named set of units in one namespace, from any tool. Collections do not nest.
Each unit is in exactly one collection, and no default collection exists. The
server refuses a new unit with no collection. Membership is explicit, and
labels never grant access. A grant, a profile attachment and an
output-sharing rule can name a collection as their object.

ADR 61 accepts on 2026-10-13 how a unit from the CLI gets its collection.
Placement rules in the namespace match the Pulumi project and a stack-name
pattern, and the first match wins. For OpenTofu, the `project` of the `cloud`
block names the collection, and the TFE-compatible API shows each collection
as a TFE project. When nothing matches, the server refuses the unit with an
error that names the fix. The caller needs the right to make a unit in the
collection.

On 2026-10-13 the project lead accepted three lead proposals of "Open
questions for collections", under the delegation of the owner. To move a unit,
a person needs rights on the unit, on the old collection and on the target
collection. A stack-name pattern of a placement rule is a glob with `*`. A
change to a placement rule does not change units that exist.
The rest of this RFC has no outcome yet.
