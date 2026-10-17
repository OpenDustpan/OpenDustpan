# 126. Every agent tool call writes an audit entry

Status: accepted · 2026-10-17. The decisions of the project owner and the
project manager below are accepted. The other decisions take effect when the
project lead accepts the pull request of this ADR.

## Context

The project owner decided that every tool call of Dusty is in the audit log
(ADR 60), and that outside agents get the same audit log (ADR 123). ADR 60
decision 3 writes an entry for an action that changes something, reads a
secret value or reads the audit log. A plain read writes no entry. Most
agent tools only read.

ADR 72 decision 6 makes the hash encoding of an entry a fixed list of every
field, so a new field changes the hash of every entry once, before the first
release.

## Decision

1. **A new kind, `agent-tool`.** Each tool call of each channel writes one
   entry of the kind `agent-tool`, also for a read, a denied call and a
   failed call. The `Toolset` writes it through the `agent.ToolAuditor`
   port as an `agent.ToolCallRecord` (ADR 122 decision 13), so no tool can
   skip it. A call whose entry cannot be written fails with
   `ErrAuditFailed`, as ADR 70 decision 26 asks for a break-glass session.
   `agenttest.RunToolset` checks both rules.
2. **The fields of an `agent-tool` entry.**
   - **Who:** `subject`, `idpSubject` and `tokenID` of the person (ADR 60
     decision 5). The agent is never the subject.
   - **What:** `action` is the action of the tool, or the first start
     action of `propose_change`. `kind` is `agent-tool`.
   - **Object:** `object`, `objectName` and `objectID` of the object of the
     tool, or the namespace of a list.
   - **Channel:** three new values: `agent` for Dusty, `mcp` for the MCP
     endpoint, and `mcp-proxy` for the stdio proxy.
   - **Result:** `outcome`, and `status`: the HTTP status that the same
     result has in the API.
   - **New fields:** `agentRun` (the run, for the channel `agent`),
     `agentSession` (the MCP session, or the run), `tool` (the tool name),
     `argsDigest` (the SHA-256 of the arguments in canonical JSON, from
     `agent.ArgsDigest`), `breakGlass` (true when the credential belongs to
     a break-glass session) and `secretStage` (the closed stage of a secret
     read that the gate refused, or empty).
3. **No content in an entry.** An entry never holds the prompt, an answer
   of the model, a tool result or the arguments. The digest of the
   arguments lets an auditor match an entry to the transcript of the run
   while the transcript exists (ADR 120 decision 22). ADR 32 decision 7
   and ADR 60 decision 16 apply to every new field.
4. **A change writes two entries.** A `propose_change` or `cancel_change`
   writes its `agent-tool` entry. The job model writes its own entries of
   the kind `change` for the job (its start, each confirmation, each
   approval, its cancel), with the person as the subject and the same
   `agentRun`, `agentSession` and `breakGlass` (ADR 125 decision 13). So a
   search for the run finds all of them.
5. **A secret read writes two entries.** A `read_state` call with secret
   values writes its `agent-tool` entry and the `reveal` entry of ADR 41,
   with the count of values (ADR 124 decision 11).
6. **Run entries.** These actions of a run write an entry of the kind
   `change` in the namespace of the run:
   - the start (`agent:start-run`), with the model name and the limits in
     `changes`,
   - the cancel (`agent:start-run` or `namespace:manage-agent-runs`), by
     the person who cancels,
   - the end, with the subject of the run, the stop reason, the turns, the
     tool calls and the four token counts of the ledger in `changes`.
7. **Setting entries.** Each of these writes an entry of the kind `change`:
   - a model connect or disconnect (`@server`),
   - a change of the installation settings: the limits, `directChanges`,
     and `mcpEnabled`, which turns the MCP endpoint on or off (`@server`),
   - the open of a maintenance window by a start with the secret flag, the
     acceptance of a window with the instances that it covers, and the
     close of a window with no acceptance (`@server`),
   - each opt-in and each opt-out of a hosted account (`@server` of the
     account),
   - a namespace opt-in or opt-out (the namespace),
   - a person opt-in or opt-out (the scope `@server`, with the caller as
     the object),
   - the approval of a consent (the namespace of the unit).
8. **Queries.** The audit list and export (ADR 60 decisions 8 and 9) get the
   filters `channel`, `agentRun` and `tool`. A person reads the entries of
   the runs of a namespace with `namespace:read-audit`, as for every entry.
9. **The audit package changes.** `pkg/audit` gets the kind, the three
   channels and the six fields. The fields go into the fixed hash encoding
   of ADR 72 decision 6. Each SQL store gets the columns. The CEF mapping of
   ADR 60 decision 9 maps `tool` and `agentRun` to custom string keys with
   labels.

## Consequences

- An agent that reads a lot writes a lot of entries: up to 8 for each turn
  of a run with the default limits. The retention of RFC 22 applies to
  them as to every entry.
- `agent-tool` entries are the first entries for plain reads. An auditor
  can see what an agent read, which ADR 60 does not record for a person.
- A search by `argsDigest` works only while the transcript exists.

## Not decided here

- Whether a person can turn off the entries of plain reads by agents. ADR
  60 decision 2 allows no switch for one kind, so the answer today is no.
