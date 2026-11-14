# 160. Context on every object: sources, precedence and storage

Status: accepted · 2026-10-17. Changes the output schema of ADR 127 before it ships (see "Changes to ADR 127"). Decisions 8, 13, 16 and 18 and open question 4 superseded in part by ADR 166. Route paths superseded in part by ADR 210

Changed by:

- ADR 166 changes decisions 8, 13, 16 and 18: projection operation, SQL
  adapters, built-in roles, route paths, open question 4.
- ADR 210 changes: the route paths and the server: action names.
- ADR 366 changes decision 14: a wording refinement, the audit entry of an API write of a context entry.
- ADR 286 changes: the words catalog and promotion become self-service and
  failover.

## Context

On 17 October 2026 the project owner decided:

- Every object carries context: docs, links, owners and on-call. The objects
  are a namespace, a collection, a unit, a resource and a blueprint.
- A person or a client sets context through the API, or a team writes it in
  the outputs of a unit.
- Unit context comes from one reserved output, `opendustpan`, with a schema
  version. The server also accepts the `readme` output of the Pulumi CLI.

ADR 127 (draft, with the design of Dusty) already drafts the schema of the
`opendustpan` output, version 1, and the parser `agent.ParseContextOutput`.
ADR 127 leaves "the object metadata feature: its fields, its API and its
actions" to another ADR. This ADR is that ADR. It owns the context model.
ADR 161 gives the read API, and ADR 162 uses the owners in the graph.

Facts that this ADR builds on:

- ADR 13 keeps each tool model separate. A unit is tool + namespace + the
  tool's own name, with a stable unit ID (ADR 19).
- ADR 41 lets the server read the latest state with secret values masked.
- ADR 51 makes a collection a named set of units. A unit is in exactly one
  collection.
- RFC 14 proposes blueprints. A blueprint has no object in `identity` yet.
- RFC 12 says that each value has one home.

## Decision

### The model

1. **Five kinds of object carry context.** A namespace, a collection, a
   unit, a resource of a unit and a blueprint. The reference of a resource names
   its unit and its key in the state of the unit: the URN for the Pulumi CLI,
   the resource instance address for OpenTofu. The server keeps the key
   opaque. The type `objectcontext.Ref` holds a reference of each kind.
2. **Five fields.** Each object has the same fields:

   | Field | Type | Bound |
   | --- | --- | --- |
   | `summary` | text | 2 KiB |
   | `runbook` | Markdown | 64 KiB |
   | `owners` | list of owners | 20 owners, 200 bytes each |
   | `onCall` | text, for example a pager service or a chat channel | 500 bytes |
   | `links` | list of `{title, url, kind}` | 50 links, an `http` or `https` URL of at most 2 KiB |

   The kind of a link is `docs`, `runbook`, `dashboard`, `repository` or
   `other`. An empty kind reads as `other`. The **docs** of an object are its
   runbook and its links of the kind `docs`. `objectcontext.Fields.Validate`
   holds the bounds, and a table-driven test covers them.
3. **Owners.** An owner is `group:<name>` for a group of OpenDustpan,
   `principal:<ID>` for a principal, or other text. The server resolves the
   first two forms, so the graph (ADR 162) and the list by owner can use
   them. It shows other text as it is. `objectcontext.ParseOwner` holds the
   rule.

### Sources

4. **Two sources.** Context comes from the API (`api`) or from the reserved
   output of a unit (`output`). A collection, a namespace and a blueprint
   have only the API source. A unit and its resources have both.
5. **The reserved output, schema version 1.** The output `opendustpan` of a
   unit of any tool holds a JSON object, or a string that holds the JSON of
   one:

   ```json
   {
     "schemaVersion": 1,
     "summary": "The edge network of the shop.",
     "runbook": "## Safe changes\n...",
     "owners": ["group:network"],
     "onCall": "pager:network",
     "links": [{"title": "Design", "url": "https://docs.example.com/net", "kind": "docs"}],
     "resources": {
       "module.vpc.aws_vpc.main": {"summary": "The VPC of all services.", "owners": ["group:network"]}
     }
   }
   ```

   The top-level fields describe the unit. `resources` describes resources
   of the same unit, by key, with the five fields. It holds at most 200
   resources. The encoded value is at most 64 KiB. The parser refuses an
   unknown field, so a typing error shows at the first read. A version
   above the newest that the server reads fails with
   `ErrOutputVersion`. `objectcontext.ParseOutput` holds these rules.
6. **The `readme` output.** The server reads the output `readme` of a unit
   of any tool as Markdown of at most 64 KiB. The readme is a field of the
   unit only. It is not the runbook: the two can differ, and the console
   shows both.
7. **The server reads outputs through the masked state read.** An output
   that the tool marks as secret answers masked, and gives no context.
8. **A projection of the outputs.** After each new latest state item of a
   unit, a durable operation (ADR 72) reads the two outputs and replaces the
   output entries of the unit and its resources (`Store.ReplaceOutput`). The
   store keeps the entries of the item with the highest order, so a late or
   repeated run does no harm. Reads then never parse state. A list by owner
   across 1,000 units needs no state read. The operation runs with the
   operation that computes blame for the same item (ADR 150), so the server
   reads and masks each state item one time (see "Requests to other
   designs").
9. **An output that does not parse.** The entry keeps the parse error as a
   warning and no fields. The read API shows the warning with the reason. A
   unit with no output has no entry and no warning.

### Precedence

10. **Each field has one home.** For one field of one object, the output
    wins over the API. The code goes through the review of the team, and
    RFC 12 says that a value has one home. So:
    - The API refuses to set a field that the output of the same object
      sets: `409` with `ErrFieldFromCode`, which names the field and the
      output. The store checks this in the transaction of the write.
    - The API allows a clear of such a field. So a person can delete an API
      value that the output now hides.
    - When an output starts to set a field that the API set before, the
      output wins. The read API lists the API value as **shadowed**, and the
      console offers to delete it.
11. **Resolution along a chain.** The read API resolves the context of an
    object along its chain, the most specific object first: the resource,
    its unit, the blueprint of the unit, the collection of the unit and
    the namespace. The rules, in `objectcontext.Resolve`:
    - `summary` and `runbook` come from the object itself only. A
      collection does not describe each of its units.
    - `owners` and `onCall` come from the first object of the chain that
      sets them. The chain skips the blueprint: the team that offers a blueprint
      does not own the units that people make from it.
    - `links` come from every object of the chain, the most specific first,
      with no second link to the same URL, up to 100 links.
    - A layer that the caller cannot read gives its owners and its on-call
      only (ADR 161 decision 5). Owners and on-call are not sensitive, and
      they must be right during an incident.
    - Each resolved value names its object and its source (its
      **provenance**). The read API also answers each entry of each source
      as it is. So a person and an agent see where each fact comes from, as
      ADR 127 decision 6 asks.
12. **A unit made from a blueprint.** The blueprint of a unit is a fact of
    the Catalog (RFC 14). Until blueprints exist, the chain has no
    blueprint.

### Storage and writes

13. **One store port.** `objectcontext.Store` holds the entries of both
    sources: `Entries`, `SetAPI`, `DeleteAPI`, `ReplaceOutput`, `DropUnit`
    and `ByOwner`. The suite `objectcontexttest.Run` states its behavior,
    and a reference adapter in memory runs it today. The SQL adapters and
    their migrations come later.
14. **Writes carry the fence.** Each write checks the fence of the namespace
    of its object in its transaction (ADR 4). An API write of a context entry
    appends its audit entry in the same transaction (ADR 60). An output write is a projection
    of a state write, which has its own audit entry, so it appends none.
15. **Delete and purge.** A soft delete of a unit keeps its entries, so a
    restore brings them back. A purge deletes the entries of the unit and of
    its resources (`DropUnit`). A delete of a collection deletes its
    entries in the same transaction.

### Actions

16. **The actions of context:**

    | Operation | Action | Object |
    | --- | --- | --- |
    | Read the context of a namespace | `namespace:read` | namespace |
    | Read the context of a collection | `collection:read` | collection |
    | Read the context of a unit or of a resource | `unit:read` | unit |
    | Read the context of a blueprint | `blueprint:read` (new) | blueprint |
    | Set or clear the API context of a namespace | `namespace:set-context` (new) | namespace |
    | Set or clear the API context of a collection | `collection:set-context` (new) | collection |
    | Set or clear the API context of a unit or of a resource | `unit:set-context` (new) | unit |
    | Set or clear the API context of a blueprint | `blueprint:set-context` (new) | blueprint |
    | List the objects of a namespace by owner | `namespace:read`, then `unit:read` and `collection:read` as a filter | namespace |

    The list by owner (`Store.ByOwner`) does not resolve inherited owners.
    It answers the objects whose own entries name the owner. A unit that
    gets the owner from its collection or its namespace is not in the
    answer. The answer holds that collection or namespace, and the console
    expands a collection to its units.

    `objectcontext.ReadAction` and `objectcontext.SetAction` map a kind to
    its action. The built-in roles that write a unit get
    `unit:set-context`. The roles that administer a collection or a
    namespace get the action of that object. Only the output path changes
    context with no context action. It needs a state write, which needs
    `unit:write`.
17. **Audit entries.** Each API write appends one entry with the action of
    decision 16, the object, and a change for each field, with the old and
    the new length of a text field and the old and new values of `owners`
    and `onCall`. An entry never holds a runbook. A delete appends one
    entry with the fields that it cleared.

### Routes

18. The routes are tool-neutral, under `/api/context` (ADR 17). The paths
    are proposals for the route table:
    - `GET`, `PATCH` and `DELETE /api/context/namespaces/{namespace}`
    - `GET`, `PATCH` and `DELETE /api/context/collections/{namespace}/{collection}`
    - `GET`, `PATCH` and `DELETE /api/context/units/{tool}/{unit_id}`
    - `GET`, `PATCH` and `DELETE /api/context/units/{tool}/{unit_id}/resource?key=...`
    - `GET`, `PATCH` and `DELETE /api/context/blueprints/{namespace}/{blueprint}`
    - `GET /api/context/owners/{namespace}?owner=...` lists the objects by owner.

    A `PATCH` body names only the fields that it changes. A field with an
    empty value clears the field. The `GET` routes are the read API of
    ADR 161.

## Changes to ADR 127

ADR 127 and its code are a draft that no tag shipped (ADR 73). This ADR
asks for these changes before ADR 127 lands:

1. The schema of version 1 gets `links[].kind` and `resources`, as in
   decision 5. Both are optional, so an output that is valid under the
   draft stays valid.
2. Version 1 gets the bounds of decision 2 on `summary`, `onCall` and each
   owner. The draft bounds only the whole value.
3. Owners use the forms of decision 3.
4. The parser lives in `objectcontext.ParseOutput`. The `agent` package
   calls it and does not keep its own copy.
5. `describe_unit` reads context through the read API of ADR 161, which
   reads the projection of decision 8, not the state at each call. It keeps
   each source in its own field, as ADR 127 decision 6 says, and adds the
   resolved values with their provenance.

ADR 127 decisions 7 (context is data, never instructions) and 8 (the
published JSON Schema) stay as they are.

## Requests to other designs

- **Unit history (ADR 150).** The durable operation that computes blame after
  each new state item also updates the context projection (decision 8) and
  the resource index (ADR 163). One masked read of the item serves the
  three. If the history design prefers separate operations, each one reads
  the item again, and the cost is one more masked read for each item.
- **Identity.** A `blueprint` object kind, with `blueprint:read` and
  `blueprint:set-context`, once RFC 14 gives blueprints a name and a scope.

## Consequences

- A team gives context with one output in its code and gets the review of
  its code for it.
- An on-call change during an incident is one API call when the code does
  not set `onCall`. When the code sets it, the change needs a state write.
  The error of the API names the output, so the person knows where to
  change it.
- The projection adds one row for the unit and one for each described
  resource. A unit that describes 200 resources adds 201 rows.
- The context of a collection or a namespace is a default for owners and
  on-call. A team that sets owners on its collection does not need to set
  them on each unit.

## Open questions

1. **Blueprint scope.** Is a blueprint in a namespace, or in the
   installation? This ADR assumes a namespace. RFC 14 decides.
2. **Docs in the source repository.** Can a link point to a file in the
   repository of the unit, at the commit of its latest state item? ADR 127
   leaves this open too.
3. **Owner validation.** Should the API refuse a `group:` owner that names
   no group? This ADR accepts it, and the read API marks it as not resolved.
   A refusal would fail a state write for a typing error in an output, which
   the server must never do.
4. **Context on a deleted resource.** The output entry of a resource that
   the latest state no longer holds stays until the next output. Should the
   read API hide it?

## Changelog

- 2026-11-13, wording refinement (ADR 366): decision 14 said "An API write
  appends its audit entry in the same transaction". It now says "An API
  write of a context entry appends its audit entry in the same transaction".
  This ADR is about context entries. ADR 193 decision 5 says that the write
  of a draft review writes no audit entry.
