Issue: #1317.

## Why

**Persona: site administrator.** Site administrator reviews who did what in a namespace and must see when an agent acted for a person without breaking existing audit hash chains.

The user path this change must make work:

1. **API.** List audit entries for the namespace and open the job start entry.
2. **Docs page.** Read the agent block of the entry to see which agent acted for the subject.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Read of an audit entry id that does not exist in the namespace scope. | Server answers 404 with error code audit.entry-not-found; list the namespace entries again and open an id from the list. | `GET /api/audit/entries` |
| Entry read is unavailable while the chain is read. | Server answers with error code audit.entry-read-unavailable; run the chain check again. | `GET /api/audit/check` |

Evidence for the claims above:

- Issue 1317 says the job service records the agent as a change key of the start entry until the field exists. Proof: Follow-up of #1165. ADR 103 decision 10 adds the field Agent to the audit entry. The job service records the agent as a change key of the start entry until the field exists.
- Package pkg/audit exists on main. Proof: pkg/audit
- Error code audit.entry-not-found exists on main. Proof: internal/errcode/catalog_audit.go:8
- Error code audit.entry-read-unavailable exists on main. Proof: internal/errcode/catalog_audit.go:11
- Test TestComputeHashIsStableAndCoversTheFields locks hash coverage on main. Proof: pkg/audit/audit_test.go:99

## What Changes

- Keep audit.Entry with no Agent field.
- Record the agent of each job audit entry in the agent block.
- Remove the temporary agent change key from new job entries and dual-read old entries with the block winning.

Out of scope:

- Agent principals and delegations of RFC 121
- Dusty runs and tools
- MCP endpoint and stdio proxy
- Definition of the agent/1 block format beyond ADR 126 decision 10 and ADR 128 decision 17

## Usability

Where the path still costs the person time or surprises them:

- The reader looks in the agent block, not in a top-level field, to see which agent acted; the docs page shows the block shape with one example.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Package pkg/audit holds the audit entry model on main. (`pkg/audit/audit.go:69 (func ComputeHash)`)
- Audit kind audit-read exists on main. (`pkg/audit/audit.go:69 (func ComputeHash)`)
- Error code audit.entry-not-found exists on main. (`internal/errcode/catalog_audit.go:8 (func catalogAudit)`)
- Test TestComputeHashIsStableAndCoversTheFields covers the entry hash on main. (`pkg/audit/audit_test.go:99 (func TestComputeHashIsStableAndCoversTheFields)`)
- Test TestChainWalkStart covers chain reads on main. (`pkg/audit/chain_test.go:258 (func TestChainWalkStart)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 103: Job gates, actions and audit entries** (not owner-tagged).

- Decisions that apply: 7 (changed), 10 (changed).
- Changed by: ADR 110 changes decisions 1 and 8; ADR 126 changes decision 10; ADR 128 changes decisions 2, 7, 9, 10 and 12; ADR 128 extends; ADR 138 extends decisions 8 and 9; ADR 141 changes decision 9; ADR 141 extends decision 4; ADR 142 extends decision 6; ADR 146 extends decision 2; ADR 148 extends decision 2; ADR 190 changes decisions 1 and 2; ADR 212 extends decision 2; ADR 213 extends decisions 1 and 8; ADR 222 changes decision 2; ADR 222 supersedes decision 1; ADR 224 supersedes decision 1; ADR 225 changes decision 2; ADR 227 changes decision 2; ADR 299 supersedes decision 3. Chain to the end: ADR 110, ADR 126, ADR 128, ADR 138, ADR 141, ADR 142, ADR 146, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 210, ADR 218, ADR 286, ADR 305, ADR 308, ADR 129, ADR 354, ADR 149, ADR 297, ADR 262, ADR 300, ADR 344, ADR 365, ADR 232, ADR 235, ADR 301, ADR 241, ADR 244, ADR 265, ADR 267, ADR 271, ADR 273, ADR 309, ADR 353.
- Current rule: this ADR, with ADR 110, 126, 128, 138, 141, 142, 146, 148, 190, 212, 213, 222, 224, 225, 227, 299.
- Quote: "Agents never approve, give a confirmation or break a freeze. An agent such as Dusty acts as the user who asked it (ADR 100 decision 17)."
- Quote: "The audit entry records the agent. `audit.Entry` gets a field `Agent`: the agent that acted for the subject, or empty. The entry hash covers it."
- Effect: Decision 10 first added an Agent field. ADR 126 changed decision 10 and ADR 128 decision 17 now rules: no Agent field, the agent travels in the agent block. This change follows the current rule.
- Conflict: none.

**ADR 126: Every agent tool call writes an audit entry** (not owner-tagged).

- Decisions that apply: 3 (current).
- Changed by: ADR 129 extends decisions 6 and 7. Chain to the end: ADR 129, ADR 210, ADR 247, ADR 286.
- Current rule: this ADR, with ADR 129.
- Quote: "No content in an entry. An entry never holds the prompt, an answer of the model, a tool result or the arguments."
- Effect: The agent block holds only the agent reference and the digest, never content.
- Conflict: none.

**ADR 128: Agents in the job model and the history model** (not owner-tagged).

- Decisions that apply: 17 (changed).
- Changed by: ADR 190 changes decisions 11, 12 and 13; ADR 222 changes decision 7; ADR 227 changes decision 7; ADR 286 changes; ADR 354 changes decisions 1, 5 and 17. Chain to the end: ADR 190, ADR 222, ADR 227, ADR 286, ADR 354, ADR 225, ADR 299, ADR 300, ADR 344, ADR 365, ADR 335, ADR 351, ADR 301, ADR 320, ADR 339, ADR 328, ADR 359, ADR 332.
- Current rule: this ADR, with ADR 190, 222, 227, 286, 354.
- Quote: "The job model writes the agent of each of its audit entries in the agent block of ADR 126 decision 10, never in a field of its own. `audit.Entry` gets no field `Agent`"
- Effect: Job entries write the agent in the block and audit.Entry keeps no Agent field. This supersedes ADR 103 decision 10.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does this change add an Agent field to audit.Entry? No. ADR 128 decision 17 says audit.Entry gets no field Agent. The agent goes in the agent block named by ADR 126 decision 10.
- What happens to the temporary agent change key in old job start entries? New entries stop writing it. Readers accept old entries; where both shapes exist the block wins. No stored row is rewritten.
- Does this change define how an agent ref is validated? No. Agent identity and delegations belong to RFC 121 and later ADRs. This change passes the ref the caller supplies and fails closed on entry validation failure.
- How are agent approval and break-glass attempts handled? They stay refused per ADR 103 decision 7. This change adds no approval path for agents.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
