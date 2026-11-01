# 161. One tool-neutral context read API

Status: accepted · 2026-10-17. Decisions 1 and 3 superseded in part by ADR 166. Route paths superseded in part by ADR 210. Open question 2 answered by ADR 263

Changed by:

- ADR 166 changes decisions 1 and 3: the route paths and the 404 rule.
- ADR 210 changes: the route paths and the server: action names.
- ADR 263 extends: open question 2, drift gets its own context section.

## Context

On 17 October 2026 the project owner asked for a rival to a "context API":
one tool-neutral read API that gives people, agents (Dusty and MCP clients)
and other tools the full context of a unit or a resource in one call. The
answer holds ownership, on-call, docs, dependencies, recent changes and
health. Every field names its authorization action.

Today a client needs many calls for this: the inventory entry, the tool's
own stack or workspace routes, the history routes, the dependents route of
ADR 153 and the audit log. Each tool keeps its own words and its own routes
(ADR 12, ADR 13). An agent that reads a unit pays for each call in tokens.

ADR 160 gives the context model. ADR 162 gives the graph. ADR 150 gives the
history items and their actors. ADR 100 gives jobs. ADR 165 gives the
unmanaged-resource findings. ADR 122 lists `describe_unit` as a tool of
Dusty and of the MCP server.

## Decision

1. **One read, many sections.** `GET /api/context/units/{tool}/{unit_id}`
   answers one document for a unit. `GET
   /api/context/units/{tool}/{unit_id}/resource?key=...` answers one for a
   resource. The document has **sections**. A client names the sections it
   needs with `include=`. With no `include`, the call answers the default
   sections. The routes of ADR 160 for a namespace, a collection and a
   blueprint answer the section `context` only.
2. **The sections.** `objectcontext.SectionCatalog` is the catalog. Each section
   names its action and the object of that action:

   | Section | Answers | Action | Object | Default | Kinds |
   | --- | --- | --- | --- | --- | --- |
   | `identity` | For a unit: the reference, the tool, the tool's own name, the display name, the collection and the blueprint. For a resource: its type and key. | `unit:read` | the unit | yes | unit, resource |
   | `context` | The resolved context with provenance, and the entries of each source. Also the shadowed API values and the output warnings (ADR 160). Owners and on-call resolve over every layer (decision 5). | the read action of each object of the chain | each object of the chain | yes | all |
   | `readme` | the `readme` output | `unit:read` | the unit | yes | unit |
   | `outputs` | the outputs of the latest state item, secret values masked | `unit:read` | the unit | no | unit |
   | `dependencies` | The units one hop upstream and one hop downstream, with the kind, the evidence and the times of each edge. Also the count of readers that are only principals. | `unit:read` | the unit and each unit that it names | yes | unit, resource |
   | `changes` | The newest 10 history items and jobs, with their actors, and the resource changes of the newest state item. For a resource: its newest 10 changes from blame. | `unit:read` | the unit | yes | unit, resource |
   | `health` | The status, the lock, and the outcome and time of the newest job. Also the age of the latest state item and the open findings that name the unit (ADR 165). | `unit:read` | the unit | yes | unit, resource |
   | `audit` | the newest 20 audit entries whose object is the unit | `namespace:read-audit` | the namespace | no | unit |

   `objectcontext.Plan` gives the sections of a call and refuses a section
   that does not exist or does not apply to the kind. A test fails for a
   section with no action, no object or no kind. A test also fails for a
   section that names a secret action, because this API answers masked
   values only.
3. **The call needs `unit:read`.** Without it, the call answers `404`, as
   the other unit routes do. So the call does not tell that a hidden unit
   exists.
4. **A denied section does not fail the call.** Each section answers a
   status:
   - `ok`: the full answer.
   - `partial`: the answer left out objects that the caller cannot read, or
     a limit cut it. The section counts what it left out.
   - `denied`: no answer. The section names the action that the caller does
     not hold.
   - `unavailable`: no answer, because the server has no data. An example
     is a unit with no job, or a feature that is not on.

   So a client always knows why a part is missing. RFC 40 asks for this
   completeness on every answer.
5. **Every section names its action in the answer.** Each section of the
   answer holds `action` and `object` next to `status`. The `context`
   section holds the action and the status of each object of its chain.
   For example, a caller with no `collection:read` gets the context of the
   unit and of the namespace, and a `denied` mark on the collection layer.
   - **Owners and on-call resolve over every layer.** A denied layer still
     gives its owners and its on-call. It gives nothing else: no summary,
     runbook, links, readme, warnings, shadowed values or entries. Owners
     and on-call are not sensitive, and during an incident a person who
     reads a unit must get the right people to call, also when their role
     does not hold `collection:read`. Their provenance keeps the source and
     the kind of the layer only: the object of the provenance has no name.
     So a caller with no `collection:read` learns that the owners come from
     the collection of the unit, and does not learn the name of the
     collection.
     `objectcontext.Resolve` holds the rule (`Layer.Hidden`), and a test
     covers it. The section counts the denied layers in `hidden`.
6. **Each tool keeps its words.** The `identity` and `changes` sections of a
   Pulumi stack say "stack", "update" and "preview". Those of an OpenTofu
   workspace say "workspace", "state version" and "plan". The document
   reads each tool through its own port and never merges the models
   (ADR 13).
7. **Limits.** One call reads at most:
   - 10 history items, 10 jobs, 20 audit entries and 200 resource changes,
   - 50 neighbors in each direction for `dependencies`, by the newest edges,
   - the 64 KiB runbook and the 64 KiB readme,
   - 1 MiB of masked outputs. A larger answer cuts the outputs and marks the
     section `partial`.

   A call with every section answers at most about 1.5 MiB.
8. **One service for every client.** The console, the CLI, Dusty, the MCP
   server and any other tool call the same service. `describe_unit` of
   ADR 122 answers this document with the default sections. Its tool result
   cut of ADR 120 still applies.
9. **No new write.** The read API writes nothing and appends no audit entry
   for an allowed read. A denied call appends the entry of a denied request,
   as the other routes do.
10. **Context is data.** The document marks each text that people wrote:
    the summary, the runbook, the readme and the link titles. An agent reads
    them as data, never as instructions (ADR 127 decision 7).
11. **Versioned shape.** The document has `schemaVersion: 1`. Before 1.0 the
    shape can change (ADR 73). Each change raises the version, so a client
    can see it.
12. **The types and the ports.** `pkg/objectcontext` holds them:
    - **Response types.** `Document` has `schemaVersion`, `object` and
      `sections`. Each section is an `Answer`, the envelope: `action`,
      `object`, `status`, `hidden` (the count of objects left out), `data`,
      and for `context` only, `layers` (`LayerStatus`: the kind, the action
      and the status of each object of the chain). `data` is one type for
      each section: `IdentityData`, `ContextData`, `ReadmeData`,
      `OutputsData`, `DependenciesData`, `ChangesData`, `HealthData` and
      `AuditData`. A `denied` or `unavailable` section has no `data`.
    - **The service port.** `Reader.Read(ctx, Request)` answers a
      `Document`. `NewReader(Sources)` builds the one implementation, so
      every client gets the same answer.
    - **The source ports.** `Sources` holds one port for each source of a
      section: `InventoryReader` (the chain, the identity, the masked
      outputs and the health), `ContextReader` (the stored entries, which
      `Store` implements), `DependencyReader` (the edges one hop each way),
      `HistoryReader` (the history items and the resource changes),
      `JobReader` (the jobs), `FindingsReader` (the open findings by kind)
      and `AuditReader` (the audit entries). A nil feature port makes its
      sections `unavailable`, and a source answers `ErrUnavailable` when
      it has no data.
    - **The access port.** `Access` answers, for one caller, which layers of
      the chain it can read (`Layers`), whether it holds the action of a
      section (`Holds`), and which units of a list it can read
      (`Readable`), in one query each.
    - A table-driven test runs `Read` over fakes of each port: every section
      with every action, a denied collection layer, no `unit:read`, a denied
      audit section, features that are not on, a hidden neighbor, cut
      outputs, a broken source and an object kind with one section.

## Example

A short answer, for a caller that holds `unit:read` and `namespace:read`
and does not hold `collection:read`. The owners still come from the
collection (decision 5):

```json
{
  "schemaVersion": 1,
  "object": {"kind": "unit", "tool": "opentofu", "namespace": "acme", "unitID": "ws-9f2"},
  "sections": {
    "identity": {"action": "unit:read", "object": "unit", "status": "ok", "data": {"name": "network-prod", "collection": "network"}},
    "context": {
      "action": "unit:read", "object": "each-layer", "status": "partial", "hidden": 1,
      "layers": [
        {"object": "unit", "action": "unit:read", "status": "ok"},
        {"object": "collection", "action": "collection:read", "status": "denied"},
        {"object": "namespace", "action": "namespace:read", "status": "ok"}
      ],
      "data": {
        "owners": {"value": ["group:network"], "from": {"object": "collection", "source": "api"}},
        "onCall": {"value": "pager:ops", "from": {"object": "namespace", "source": "api"}}
      }
    },
    "dependencies": {"action": "unit:read", "object": "each-named-unit", "status": "partial", "hidden": 2, "data": {"downstream": [], "upstream": []}},
    "health": {"action": "unit:read", "object": "unit", "status": "ok", "data": {"status": "applied", "openFindings": {"managed-twice": 1}}}
  }
}
```

## Consequences

- A client gets the context of a unit in one call. An agent saves the
  tokens of five or six calls.
- The document reads ports of several features: the inventory, the context
  store, the dependency edges, the history, the jobs and the findings.
  Each port has its own conformance suite. A section whose feature is not
  there answers `unavailable` until it lands.
- Because a denied section does not fail the call, a role with only
  `unit:read` gets a useful answer. An administrator adds actions to give
  more.
- The routes need actions in the route table (ADR 17). A route of this API
  declares `unit:read` on the unit as its action. The service checks the
  action of each section in the code of the section.

## Open questions

1. **Several units in one call.** A dashboard of 50 units would make 50
   calls. Should the API take a list of units, with a limit?
2. **Drift.** RFC 25 proposes drift detection. When it exists, does drift go
   into `health`, or into a section of its own?
3. **Cost.** RFC 45 proposes cost estimation. Is cost a section?
4. **Resource dependents.** ADR 153 decision 16 answers the dependents of a
   resource inside its unit. Does the `dependencies` section of a resource
   answer them, or only the unit edges?
