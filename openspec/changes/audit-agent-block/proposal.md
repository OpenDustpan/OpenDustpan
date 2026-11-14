---
version: 2
---

## Why

**Persona: site administrator.** A site administrator reads the audit log and runs the chain check. Later agent changes need to record the agent run, the session, the tool, the argument digest, the break-glass flag and the secret stage in each entry. Today the audit entry has no such fields, the job start writes the agent as a change key, and no store column or API field exists. Without the block, the administrator cannot search by run and the chain cannot cover the agent data.

The user path this change must make work:

1. **Server start.** The server starts on an existing installation. The baseline columns exist. The chain check still passes for every stored entry.
2. **API.** The administrator calls `GET /api/audit/entries` and reads the agent block of an entry. An entry with no agent data has no block.
3. **API.** The administrator calls `GET /api/audit/check` for a scope. The check covers entries with and without the block.
4. **E2e.** An e2e step starts a job with an agent actor and reads its entry through `GET /api/audit/entries` with an access token.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The chain check finds a hash that does not match an entry | The check returns the first entry that fails, as it does today. No new error code. | `GET /api/audit/check` |
| An entry with an agent field that is not valid is refused at the store | The write fails and nothing is stored. The server returns its existing audit failure. | `GET /api/audit/entries` |

Evidence for the claims above:

- The audit Entry has no agent fields Proof: pkg/audit/audit.go:232 Entry, as the roadmap step states
- ComputeHash covers no agent field Proof: pkg/audit/audit.go:408 (func ComputeHash); test TestComputeHashIsStableAndCoversTheFields at pkg/audit/audit_test.go:99
- The job start writes the agent as a change key and Actor has only Agent string Proof: pkg/job/service_start.go:623 and pkg/job/job.go:381, as the roadmap step states
- The API converts an entry in one function Proof: internal/api/audit/audit.go:173 (func toAuditEntry)

## What Changes

- pkg/audit Entry gets the six fields agentRun, agentSession, tool, argsDigest, breakGlass and secretStage, and the channels agent, mcp and mcp-proxy.
- ComputeHash writes the agent/1 block only when a field is set, in the fixed order of ADR 126 decision 10.
- The baseline of each SQL dialect gets the six columns, edited in place in name order.
- The API entry gets an agent block and toAuditEntry fills it.
- pkg/job replaces Actor.Agent string with job.AgentRef, and the job entries write the block instead of a change key. For a job entry, tool, argsDigest and secretStage are empty.

Out of scope:

- The agent-tool audit kind and the tool call entries.
- The audit list filters channel, agentRun and tool, and the CEF mapping.
- The delegation in the hash block.
- The agent runs, the MCP endpoint and the model connection.

## Usability

Where the path still costs the person time or surprises them:

- The administrator finds the agent data of a run in one entry field, with no search in the change keys.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- ComputeHash exists and covers the current fields only (`pkg/audit/audit.go:408 (func ComputeHash)`)
- The API converts a stored entry in one function (`internal/api/audit/audit.go:173 (func toAuditEntry)`)
- A test covers the hash stability (`pkg/audit/audit_test.go:99 (TestComputeHashIsStableAndCoversTheFields)`)
- The channel is normalized to api for unknown values (`pkg/audit/audit_test.go:81 (TestNormalizeChannelMapsUnknownValuesToAPI)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 126: Every agent tool call writes an audit entry** (not owner-tagged).

- Decisions that apply: 2 (current), 9 (current), 10 (current).
- Changed by: ADR 129 extends decisions 6 and 7. Chain to the end: ADR 129, ADR 210, ADR 247, ADR 286.
- Current rule: this ADR, with ADR 129.
- Quote: "`pkg/audit` gets the kind, the three channels and the six fields. Each SQL store gets the columns."
- Quote: "An entry with all six fields empty (and `breakGlass` false) writes no block."
- Quote: "the length-prefixed text `agent/1`, then the six fields in a fixed order (`agentRun`, `agentSession`, `tool`, `argsDigest`, `breakGlass` as `true` or `false`, `secretStage`), each length-prefixed, with an empty value when it is not set. Inside the block no field is optional."
- Effect: The change adds the six fields and channels, the store columns, and the hash block exactly as decision 10 says: the text agent/1, then agentRun, agentSession, tool, argsDigest, breakGlass as true or false, secretStage, each length-prefixed, no field optional inside the block.
- Conflict: none.

**ADR 128: Agents in the job model and the history model** (not owner-tagged).

- Decisions that apply: 1 (changed), 17 (changed).
- Changed by: ADR 190 changes decisions 11, 12 and 13; ADR 222 changes decision 7; ADR 227 changes decision 7; ADR 286 changes; ADR 354 changes decisions 1, 5 and 17. Chain to the end: ADR 190, ADR 222, ADR 227, ADR 286, ADR 354, ADR 225, ADR 299, ADR 300, ADR 344, ADR 365, ADR 335, ADR 351, ADR 301, ADR 320, ADR 339, ADR 328, ADR 359, ADR 368, ADR 332.
- Current rule: this ADR, with ADR 190, 222, 227, 286, 354.
- Quote: "`audit.Entry` gets no field `Agent`."
- Quote: "The job model writes the agent of each of its audit entries in the agent block of ADR 126 decision 10, never in a field of its own."
- Quote: "`tool`, `argsDigest` and `secretStage` are empty."
- Effect: The job model writes the agent in the block. audit.Entry gets no Agent field. agentRun is the run ID for the channel agent, agentSession is the MCP session ID or the run ID, breakGlass is true for a break-glass credential, and tool, argsDigest and secretStage are empty for a job entry.
- Conflict: none.

**ADR 354: Agent principals, delegation and the kind agent** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The person who delegated may confirm (owner, 2026-11-07)."
- Effect: ADR 354 is owner-tagged and changes ADR 128 decision 17 so that job.AgentRef holds the agent. This change does not touch its decisions. The block stays agent/1 and the delegation is not in it. No ADR places the delegation in the block. A later block version needs an owner decision.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does the hash block hold the delegation? No. ADR 126 decision 10 fixes six fields and ADR 354 does not change the block. A later agent/2 block needs an owner decision.
- Do old chains still pass? Yes. ADR 126 decision 10 says an entry with all six fields empty hashes the same. A chain-check test of a stored chain proves it.
- Is a numbered migration needed? No. The baseline of each dialect is edited in place, as the rules for the schema say.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
