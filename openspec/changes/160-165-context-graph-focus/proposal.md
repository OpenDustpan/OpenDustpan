---
version: 1
---

## Why

**Persona: developer.** A developer keeps notes about stacks and workspaces and checks what depends on one unit before a change, but today context has no wired read and write path and the focus view has no route, so notes stay local and blast radius stays guesswork.

The user path this change must make work:

1. **Docs page.** Open docs page for context and focus, then call GET namespace context and GET unit context through the API.
2. **API.** Call GET unit context and PATCH unit context for one stack or workspace.
3. **Console.** Call NEW focus route on the page of one unit and open folds by collection.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Unit or namespace does not exist, or the caller cannot read the start unit. | 404 with code context.object-not-found. Run the same GET again with the namespace ID from the namespace list. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/context` |
| Patch names an unknown section or field. | 400 with code context.invalid. Run the PATCH again with a section from the section catalog. | `PATCH /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/context` |
| PATCH of the context of a soft-deleted unit. | 409 with code context.unit-deleted. Restore the unit before the PATCH, or read the marked GET. | `PATCH /api/dustpan/v1/namespaces/{namespace}/context` |

Evidence for the claims above:

- Context read and write routes exist in the route table for namespaces and units. Proof: Route ANY/DELETE/GET/PATCH /api/dustpan/v1/namespaces/{}/context and ANY/DELETE/GET/PATCH /api/dustpan/v1/namespaces/{}/units/{}/context where route table or API description.
- Context error codes exist for invalid input, missing values and missing objects. Proof: Error codes context.field-from-code, context.invalid, context.not-set, context.object-not-found, context.unit-deleted, context.unknown-section where internal/errcode/catalog_context.go.
- Domain ports for context reading and graph focus exist on main with tests. Proof: Type ContextReader where pkg/objectcontext/reader.go:257 (func IDOf); type FocusView where pkg/graph/focus.go:48; tests TestContextActionRoles where pkg/access/access_test.go:648 and TestFocus where pkg/graph/graph_test.go:331.

## What Changes

- Serve GET, PATCH and DELETE context routes for namespaces and units by stable namespace ID and stable unit ID.
- Add the NEW route GET /api/dustpan/v1/namespaces/{namespace}/units/{tool}/{unit_id}/focus with action unit:read on the start unit and each named unit.
- Add SQL adapters for objectcontext entries plus owner index and graph edge reads on both dialects.

Out of scope:

- Sweep targets and sweep lifecycle.
- Streaming sweep report intake and finding triage.
- Schedule worker and daily state check.
- Projection runs, resource index rebuild and restore behavior.

## Usability

Where the path still costs the person time or surprises them:

- Focus folds hide units above the neighbor bound; the person must open each collection fold to see hidden units.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Context reader port exists and resolves stable IDs. (`pkg/objectcontext/reader.go:257 (func IDOf)`)
- Focus view type exists in the graph domain. (`pkg/graph/focus.go:48 (func Focus)`)
- Context error catalog exists with context codes. (`internal/errcode/catalog_context.go:7 (func Code)`)
- Graph focus has a domain test. (`pkg/graph/graph_test.go:331 (func TestFocus)`)
- Context actions have role coverage. (`pkg/access/access_test.go:648 (func TestContextActionRoles)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 162: The unit graph: queries, limits and the focus view** (not owner-tagged).

- Decisions that apply: 1 (current), 4 (current), 5 (current), 7 (current), 9 (current), 10 (current), 12 (current), 14 (current).
- Changed by: ADR 166 changes decisions 11 and 15; ADR 210 changes; ADR 313 extends. Chain to the end: ADR 166, ADR 210, ADR 313, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 166, 210, 313.
- Quote: "Nodes and edges. A node is a unit, named by namespace, tool and stable unit ID (`graph.Unit`). A unit of one tool can depend on a unit of another tool. An edge is a dependency edge of ADR 153 that names a dependent unit. An edge that names only a principal is not a graph edge. The `dependencies` section of ADR"
- Quote: "No second record. `graph.EdgeReader` reads edges. An adapter over the dependency store implements it. The graph package has no write port. Its suite, `graphtest.RunEdgeReader`, takes edges that the harness writes through the dependency port, so the suite also checks that the two ports agree.  ### Queries"
- Quote: "Focus view. `graph.Focus` answers the view of the console: - the focus unit, - its neighbors one hop upstream and one hop downstream, up to 12 in each direction by default and 50 at most, the newest edges first, - the neighbors above that limit, and the units one hop beyond a shown neighbor, **folded** into the"
- Effect: This change wires the accepted focus view, walk limits, access filter, completeness metadata and unit:read actions to SQL adapters and tool-neutral routes.
- Conflict: none.

**ADR 166: Context and graph: gaps found before the implementation** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current), 10 (current), 13 (current), 14 (current), 15 (current), 16 (current), 17 (current), 18 (current).
- Changed by: ADR 210 changes; ADR 286 changes. Chain to the end: ADR 210, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 210, 286.
- Quote: "One table for the entries. Both dialects get the table `object_context`, one row for each entry:"
- Quote: "The stores key by the namespace ID. Every row of the context store, of the resource index, of the projection heads and of the dependency edges holds the stable ID of the namespace (ADR 163), never its name. The ports kee"
- Quote: "The routes of context. These replace the paths of ADR 160 decision 18 and ADR 161 decision 1:"
- Effect: This change adds the object_context tables keyed by namespace ID, the edge reader port and indexes, and serves the replacement context routes with the namespace-first 404 rule and soft-deleted handling.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- What does the focus view return and where is context stored? ADR 162 decisions 7 and 9 fix the shape: focus unit, one hop each way, 12 by default and 50 at most, folds, hops 1 to 5, units 1 to 500 default 200, edges read 5000 per hop. ADR 166 decisions 1, 2 and 10 fix storage by namespace ID with owner index.
- Does this change need a new authorization action? No. ADR 162 decision 14 adds no action and reuses unit:read on the start unit and each named unit; ADR 166 decision 19 puts context actions in the data family. No new action is specified.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
