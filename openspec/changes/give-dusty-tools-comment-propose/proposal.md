Issue: #936, #937, #1147.

## Why

**Persona: developer.** A developer asks Dusty to explain a failed job and to draft the fix. Dusty can comment and propose. Only the developer confirms, so no agent change applies without a person.

The user path this change must make work:

1. **Console.** The developer asks Dusty to explain a failed job.
2. **API.** Dusty calls the NEW tool comment_job or comment_change_order. The developer reads the comment.
3. **Console.** Dusty calls the NEW tool propose_change_order. The developer confirms the draft.
4. **SDK.** An outside agent lists the same tools on the MCP endpoint with an access token.
5. **Docs page.** The developer reads the flow on the user docs page.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| An agent call is refused because the person has no agent:use on the namespace. | 403 with the NEW error code `agent.denied` and no write. | `GET /api/audit/entries` |
| The MCP endpoint is off and tools/list is called. | 404 with no tool list. | `GET /api/audit/entries` |
| A confirm is refused because the change order is not a draft. | 409 with the NEW error code `change.not-draft` and no state change. | `GET /api/audit/entries` |

Evidence for the claims above:

- Main has no comment_job, comment_change_order or propose_change_order tool. Proof: The keyword evidence rg -n -F --glob '!*_test.go' for each of the three names over pkg internal cmd gives 0 matches.
- Main has the read-only tools search_units, describe_unit, read_audit_log and explain_access. Proof: internal/agent/tools.go:28, internal/agent/tools.go:29, internal/agent/tools.go:34 and internal/agent/tools.go:35.
- Main has the audit kinds tool-operation and audit-read. Proof: pkg/audit/audit.go:44 and pkg/audit/audit.go:69.

## What Changes

- Three NEW agent tools comment_job, comment_change_order and propose_change_order in the one Toolset for the Dusty loop and the MCP endpoint.
- The propose tool calls the draft route owned by dusty-proposes-change-order.
- Refusal of each approve and reject verdict from an agent principal.
- A user docs page for Dusty comment and propose.

Out of scope:

- Streaming of Dusty answers, done in its own change for issue 937
- MCP endpoint read-only first slice, done in its own change for issue 1147
- The MCP stdio proxy command
- MCP resources and prompts
- Any approve tool for agents

Depends on:

- dusty-proposes-change-order
- every-step-audited
- person-confirms
- `dusty-proposes-change-order` (order of the milestone review)
- `person-confirms` (order of the milestone review)
- `every-step-audited` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A person can confuse a draft with a proposed order unless the state label says draft until confirmed.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The agent tool names for four read-only tools exist. (`internal/agent/tools.go:28 (ToolSearchUnits)`)
- The audit kind tool-operation exists. (`pkg/audit/audit.go:44 (tool-operation)`)
- The secret consent path is tested. (`internal/agent/gate_test.go:168 (TestConsentPath)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 122: Twelve task-shaped tools, each with its action and object** (not owner-tagged).

- Decisions that apply: 1 (changed), 14 (current).
- Changed by: ADR 128 changes decision 1; ADR 210 changes. Chain to the end: ADR 128, ADR 210, ADR 190, ADR 222, ADR 227, ADR 286, ADR 354, ADR 247, ADR 225, ADR 299, ADR 300, ADR 344, ADR 365, ADR 335, ADR 351, ADR 301, ADR 320, ADR 339, ADR 328, ADR 359, ADR 332.
- Current rule: this ADR, with ADR 128, 210.
- Quote: "A change that makes the catalog larger than 12 tools needs a new ADR."
- Effect: The three NEW tools join the one Toolset after the short ADR 122 amendment is accepted. No tool is renamed or deleted.
- Conflict: rfc_needed.

**ADR 123: An MCP endpoint and a stdio proxy offer the same tools** (not owner-tagged).

- Decisions that apply: 2 (current), 7 (current).
- Changed by: ADR 210 changes. Chain to the end: ADR 210, ADR 247, ADR 286.
- Current rule: this ADR, with ADR 210.
- Quote: "Tools only. The endpoint offers the `tools` capability: `tools/list` answers the catalog of ADR 122, and `tools/call` runs a tool through the one `Toolset` (ADR 122 decision 13). It offers no resources, no prompts and no sampling. Resources can come"
- Quote: "Off by default. An administrator turns it on."
- Effect: The MCP endpoint lists the same three tools through the one Toolset. It stays off by default.
- Conflict: none.

**ADR 126: Every agent tool call writes an audit entry** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 129 extends decisions 6 and 7. Chain to the end: ADR 129, ADR 210, ADR 247, ADR 286.
- Current rule: this ADR, with ADR 129.
- Quote: "A new kind, `agent-tool`. Each tool call of each channel writes one entry of the kind `agent-tool`, also for a read, a denied call and a failed call."
- Effect: Each call of the three tools writes one agent-tool entry with person as subject and dusty with delegation as agent.
- Conflict: none.

**ADR 128: Agents in the job model and the history model** (not owner-tagged).

- Decisions that apply: 10 (current), 16 (current).
- Changed by: ADR 190 changes decisions 11, 12 and 13; ADR 222 changes decision 7; ADR 227 changes decision 7; ADR 286 changes; ADR 354 changes decisions 1, 5 and 17. Chain to the end: ADR 190, ADR 222, ADR 227, ADR 286, ADR 354, ADR 225, ADR 299, ADR 300, ADR 344, ADR 365, ADR 335, ADR 351, ADR 301, ADR 320, ADR 339, ADR 328, ADR 359, ADR 332.
- Current rule: this ADR, with ADR 190, 222, 227, 286, 354.
- Quote: "An agent records no verdict."
- Quote: "Not in the tool catalog today. `agent.Changes` gets the method `Comment` for a later tool."
- Effect: No approve tool is added. Each approve and reject verdict from an agent principal is refused. The change uses the Comment port that ADR 128 holds for a later tool.
- Conflict: none.

**ADR 190: Change orders: an object above jobs** (owner-tagged: decision 14).

- Decisions that apply: 14 (changed), 16 (current).
- Changed by: ADR 222 changes decisions 6 and 7; ADR 225 changes decisions 6 and 7; ADR 299 changes decisions 4, 9, 10, 12 and 14; ADR 300 changes decision 10; ADR 344 changes decision 14; ADR 365 changes decision 10. Chain to the end: ADR 222, ADR 225, ADR 299, ADR 300, ADR 344, ADR 365, ADR 286, ADR 335, ADR 351, ADR 354, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 222, 225, 299, 300, 344, 365.
- Quote: "Agents. An agent reads a change order and leaves comments, when a later ADR gives it a tool. It leaves comments and blocking comments only (owner, ADR 128 decision 10). An agent records no verdict: it submits its blocking threads in a review of the verdict `comment` (ADR 192 decision 2). It can writ"
- Quote: "No job thread on a job of a change order."
- Effect: Comment tools leave comments and blocking comments only in a review of verdict comment. A comment on a job with a change-order origin is refused.
- Conflict: none.

**ADR 354: Agent principals, delegation and the kind agent** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The person who delegated (Alice) may confirm a change that her own agent proposed, by default."
- Effect: The draft names the person as subject and dusty with the delegation as agent. The person who delegated confirms the draft under the change-order rules.
- Conflict: none.

## Open questions

1. Is the ADR 122 amendment for the three tools accepted before this change is built? Decides: PM.
   - Options: Accept the short amendment first, then build; Defer the three tools.
   - Recommended: Accept the short amendment first, then build. Reason: ADR 122 decision 14 needs a new ADR for a 13th tool. The amendment is a prior step, outside this change.

Not spec-ready: the change conflicts with ADR 122: an RFC comes first.

Questions considered and answered:

- Can an agent approve? No. ADR 128 decision 10 refuses each verdict from an agent. No approve tool is added.
- Who confirms a draft? The person who delegated confirms through the draft and confirm-draft routes of dusty-proposes-change-order, by ADR 354 decision 1.
- Does the MCP endpoint gain resources or prompts? No. ADR 123 decision 2 offers tools only.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
