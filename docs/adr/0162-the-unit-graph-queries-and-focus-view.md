# 162. The unit graph: queries, limits and the focus view

Status: accepted · 2026-10-17. Decisions 11 and 15 superseded in part by ADR 166. Route paths superseded in part by ADR 210

## Context

On 17 October 2026 the project owner decided:

- The graph joins units and resources by output reads (dependencies), by
  owners and by collections.
- The queries are the dependents of a unit, the blast radius within N hops
  and the path between two units.
- The console shows a focus view: one unit, one hop each way, and the other
  units folded into their collection. The whole graph is not readable at
  more than 140 units.
- The history design records the dependency edges from output reads. This
  design reads them and does not record them a second time.

ADR 153 (accepted, with the history design) defines the edges: the package
`dependency`, an edge from a source unit to a dependent, its kind
(`output-read` first), its evidence (`job`, `lock`, `principal`,
`declared`), its first and last time seen, and the dependents window of 90
days. It answers the direct dependents of a unit at
`GET /api/inventory/units/{tool}/{unit_id}/dependents`.

RFC 40 asks for graph queries with an anchor, traversal steps and a return
shape, with completeness on every answer. ADR 160 gives each unit its
owners and ADR 51 its collection.

## Decision

### The graph

1. **Nodes and edges.** A node is a unit, named by namespace, tool and
   stable unit ID (`graph.Unit`). A unit of one tool can depend on a unit of
   another tool. An edge is a dependency edge of ADR 153 that names a
   dependent unit. An edge that names only a principal is not a graph edge.
   The `dependencies` section of ADR 161 counts such readers.
2. **Owners and collections are facts of a node.** Each node carries its
   name, its collection, its effective owners (ADR 160) and whether it is
   deleted (`graph.Facts`). The console groups and colors nodes by them,
   and the focus view folds by collection. They are not edges that a walk
   follows: a walk over "same owner" would reach every unit of a large team
   in one hop.
3. **Resources.** The server cannot map an output to the resource that
   makes it (ADR 153 decision 16). So the graph links units, and a resource
   is a node only inside its unit. The `managed twice` finding of ADR 165
   links two units through one cloud resource. The console draws it on the
   graph as a marked link between the two units. A walk does not follow
   it, because it is not a dependency.
4. **No second record.** `graph.EdgeReader` reads edges. An adapter over the
   dependency store implements it. The graph package has no write port.
   Its suite, `graphtest.RunEdgeReader`, takes edges that the harness
   writes through the dependency port, so the suite also checks that the
   two ports agree.

### Queries

5. **Walk.** `graph.Walk` follows edges breadth first, hop by hop, from one
   unit, downstream (to the units that read its outputs) or upstream (to
   the units whose outputs it reads). The named queries are walks:
   - **Dependents**: one hop downstream. It answers the same units as the
     dependents route of ADR 153.
   - **Dependencies**: one hop upstream.
   - **Blast radius**: N hops downstream, from 1 to 5, 2 by default.
6. **Path.** `graph.Path` finds one shortest path between two units, up to
   6 hops. By default the path follows each edge from its source to its
   dependent, so it shows how the outputs of the first unit reach the
   second. With `undirected`, it follows edges both ways. When no path
   exists within the limits, the answer says so with its completeness.
7. **Focus view.** `graph.Focus` answers the view of the console:
   - the focus unit,
   - its neighbors one hop upstream and one hop downstream, up to 12 in each
     direction by default and 50 at most, the newest edges first,
   - the neighbors above that limit, and the units one hop beyond a shown
     neighbor, **folded** into their collections. A fold names the
     namespace, the collection, the count of units, and for each shown unit
     the count of edges to the fold.

   The view reads no further than two hops. A unit that is upstream and
   downstream shows in both lists.
8. **The window.** An edge counts when its last time seen is within the
   window: 90 days by default, the dependents window of ADR 153. A query can
   give a shorter window. So the graph and the delete check agree.
9. **Limits.** Each limit bounds the cost of one query on a namespace with
   thousands of units. A request above a limit fails with `400`.

   | Limit | Value |
   | --- | --- |
   | Hops of a walk | 1 to 5, default 2 |
   | Hops of a path | 1 to 6 |
   | Units in one answer | 1 to 500, default 200 |
   | Edges read in one hop | 5,000 |
   | Units in one call to a port | 500 |
   | Neighbors of the focus view | 1 to 50 in each direction, default 12 |

   When a limit cuts an answer, the answer says `truncated`.

### Access

10. **A walk never passes through a hidden unit.** Each unit that a query
    names needs `unit:read` for the caller. The `graph.Access` port answers
    the readable units of a batch, with the filter in the query (ADR 17),
    never with one check for each unit. A unit that the caller cannot read
    counts in `hidden`, and the walk does not follow its edges. So an
    answer never shows the structure behind a hidden unit.
11. **The start unit.** A query whose start unit the caller cannot read
    answers `404`, the same as for a unit that does not exist.
12. **Completeness.** Each answer holds `complete`, `truncated`, `hidden` and
    `since` (the start of the window). This is the completeness metadata of
    RFC 40.
13. **Across namespaces.** An edge can link units of two namespaces. A walk
    follows it when the caller can read both units. An edge is in the
    namespace of its source (ADR 153), and the walk reads it from there.
14. **Actions.** The graph adds no action:

    | Query | Action | Object |
    | --- | --- | --- |
    | Each query | `unit:read` | the start unit, and each unit that the answer names |

    A graph read appends no audit entry. A denied read appends the entry of
    a denied request.

### Routes

15. The routes extend the inventory API. The paths are proposals for the
    route table:
    - `GET /api/inventory/units/{tool}/{unit_id}/graph/downstream?hops=&maxNodes=&window=` (blast radius)
    - `GET /api/inventory/units/{tool}/{unit_id}/graph/upstream?hops=&maxNodes=&window=`
    - `GET /api/inventory/units/{tool}/{unit_id}/graph/focus?neighbors=&window=`
    - `GET /api/inventory/graph/path?from={tool}/{unit_id}&to={tool}/{unit_id}&maxHops=&undirected=`

    The MCP server of ADR 123 can offer the blast radius and the path as
    tools. ADR 122 keeps the catalog at 8 to 12 tools, so this needs a
    decision on that catalog (see "Open questions").

### What the console needs

16. The console needs:
    - the focus view on the page of each unit, with the folds as boxes with
      a count, a click on a fold that opens the units of that collection,
      and a click on a unit that moves the focus,
    - a blast radius view before a destroy or a delete, next to the delete
      check of ADR 153, with the owners of each unit, so a person knows whom
      to tell,
    - a path search between two units,
    - a clear mark on each answer that is `truncated` or has `hidden` units,
      with the count,
    - the edge evidence on each edge: an inferred edge (`lock`, or
      `ambiguous`) shows in another line style than an exact edge (`job`,
      `declared`).

## Consequences

- No new table. The graph reads the dependency edges and the facts of the
  inventory, the collections and the context.
- A walk of 5 hops reads at most 5 batches of edges and 5 batches of facts
  for each 500 units. On a namespace of 10,000 units, a walk that reaches
  500 units reads at most about 25,000 edges.
- Inferred edges (`lock`) can be wrong (ADR 153). The graph shows their
  evidence and does not hide them.
- A team that runs its tools on workstations with no lock gets
  principal-only edges, which are not graph edges. Jobs (ADR 100) give exact
  edges.

## Requests to other designs

- **Dependency store (ADR 153).** The graph reads edges for a batch of units
  in one call: `Edges(units, direction, since, limit)`. The draft
  `dependency.Store` reads one unit at a time with `Dependents` and
  `Sources`. A batch method in that port avoids up to 500 queries for each
  hop. Without it, the adapter calls the draft methods in a loop, and the
  limits stay the same.
- **Unit reference.** `graph.Unit` has the same shape as `history.Unit` of
  the draft. When the history design lands, the graph uses `history.Unit`,
  and this type goes.

## Open questions

1. **Agent tools.** Do the blast radius and the path become tools of Dusty
   and of the MCP server, or does `describe_unit` with its `dependencies`
   section cover enough? ADR 122 counts the tools.
2. **Hidden units in a blast radius.** A person who checks a delete wants
   the full count of units that a change reaches. This ADR stops at a hidden
   unit and counts it. Should an administrator be able to see a count of
   the units beyond a hidden unit, with no names?
3. **Declared edges.** When RFC 15 or RFC 24 adds declared edges, do they
   count with no window, because a declaration does not age?
