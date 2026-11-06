# 61. A unit from the CLI gets its collection from placement rules or the TFE project

Status: accepted · 2026-10-13

Changed by:

- ADR 329 extends decisions 1 and 3: placement rules also match OpenTofu unit names, and a unit that no rule matches is refused.

## Context

ADR 51 puts each unit in exactly one collection, with no default collection.
The server refuses a new unit when it cannot find its collection. ADR 51
leaves open how a CLI names the collection. Until a decision exists, the
server refuses every unit that a CLI makes.

The tool CLIs do not send a collection:

- `pulumi stack init` sends only `<namespace>/<Pulumi project>/<stack>`.
- The `cloud` block of OpenTofu can name a TFE project (`project`). The TFE
  protocol uses a TFE project to group workspaces.

[RFC 4](../rfc/0004-what-sits-under-a-namespace.md) sketches placement rules
for Pulumi stacks and a mapping from TFE projects to collections. On
2026-10-13 the project owner chose "Placement rules + TFE project
(Recommended)". In that option, rules in the namespace match "Pulumi
project/stack-name patterns". The `project` of the OpenTofu `cloud` block
"names the collection". On "No match", the server must "refuse with an error
naming the fix". RFC 4 quotes the full option.

## Decision

1. **Placement rules for Pulumi stacks.** A namespace has an ordered list of
   placement rules. Each rule matches a Pulumi project and a stack-name
   pattern, and names one collection. When the Pulumi CLI makes a stack, the
   server takes the collection of the first rule that matches.
2. **The TFE project names the collection for OpenTofu.** The
   TFE-compatible API shows each collection as a TFE project. When OpenTofu
   makes a workspace, the `project` of its `cloud` block names the
   collection.
3. **No match, no unit.** The server refuses to make the unit in these
   cases:
   - no placement rule matches a new Pulumi stack,
   - the `cloud` block names no `project`,
   - the named collection does not exist.

   The server does not make a collection on its own (ADR 51). The error
   names the namespace and the name of the unit. For a Pulumi stack it says
   to add a placement rule. For a workspace it says to set `project` in the
   `cloud` block. It also says that a person can make the collection, or
   make the unit through the API with a collection.
4. **The caller needs the right to make a unit in that collection.** After
   the server finds the collection, it checks that the caller has a grant
   that allows it to make a unit in that collection, on the collection or on
   an object that holds it (ADR 52). When the check fails, the error names
   the collection and the rule or the project that chose it.
5. **The API and blueprints name the collection directly.** A unit that the
   API, the console or a blueprint makes names its collection (ADR 51). Rules
   and TFE projects apply only to units that a CLI makes.
6. **Placement rules have an API.** Each placement rule operation has a REST
   API and an action on the namespace (ADR 17). Each change passes the
   policy check and writes an audit entry.

## Not decided here

[RFC 4](../rfc/0004-what-sits-under-a-namespace.md) keeps these open:

- Which rights a person needs to move a unit to another collection.
- The syntax of a stack-name pattern.
- What a change to a placement rule does to the units that exist.
- How the server gives a collection to the units that exist before ADR 51.
- (e2e) Whether a stack tag that the CLI sends at creation can name the
  collection.
- The names of the actions for placement rules and for unit creation
  (RFC 19).

## Consequences

- A collection name must be a valid TFE project name, so that the
  TFE-compatible API can show it. The name rules of RFC 4 apply: letters,
  digits, inner spaces, `-` and `_`, 3 to 40 characters, unique in the
  namespace without regard to case.
- The TFE-compatible API serves TFE project routes for collections. Check
  each route with `make e2e` before it ships.
- A team must make a collection, and a placement rule for each Pulumi
  project, before its CLI makes the first unit. The docs show the steps.
- Placement rules need numbered migrations in each SQL dialect (CLAUDE.md).
