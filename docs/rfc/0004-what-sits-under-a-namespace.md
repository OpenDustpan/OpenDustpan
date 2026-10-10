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

## Outcome

None yet.
