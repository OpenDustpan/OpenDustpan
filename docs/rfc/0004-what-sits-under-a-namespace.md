---
rfc: 4
title: What sits under a namespace
status: draft
created: 2026-10-10
---

# RFC 4. What sits under a namespace

Status: draft. No proposal is final yet.

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

- The word. Not "project" (both tools use it). Candidates: group, space,
  folder, system, collection. "Space" is Spacelift's word for a similar,
  harder construct.
- Can a unit be in more than one group? One group is simple to reason about
  for access and maps to one TFE project. Labels can cover the rest (a unit
  can be in group `payments` with label `pci`).
- How do rules from two groups combine: union of allows, with any deny
  winning (RFC 19)?
- Does a blueprint (RFC 14) choose the group of the unit it makes, and can
  the person who makes the unit change it?
- Does the Kubernetes operator (RFC 31) map a Kubernetes namespace to a
  group?

## Outcome

None yet.
