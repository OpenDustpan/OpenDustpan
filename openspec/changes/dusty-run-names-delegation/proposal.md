---
version: 1
---

Roadmap: `docs/roadmap.md:769` (A person delegates to Dusty).
Issue: No issue: a new change written from RFC 121 slices 3 and 6 and ADR 120 decision 8, as the plan refresh of 25 November 2026 asks. The pull request names the issue of the milestone when the lead assigns it.

## Why

**Persona: developer.** A developer asks Dusty a question in the console. Dusty is the agent of that developer, so the run must say under which delegation it acts, and it must stop when that delegation ends. Today a run records the person and the credential only. A run that names no delegation cannot show "everything Dusty did for this person under this note".

The user path this change must make work:

1. **API.** In a console session, start one Dusty run and name the delegation to Dusty (the ID from the list of own delegations). The start answer shows the run ID, the person, the delegation ID and the state.
2. **API.** Read the run. The answer shows the delegation ID, the credential kind `agent-run` and the stop reason when the run ended.
3. **API.** Revoke the delegation while the run is active. The run ends at its next step or tool call with the stop reason `delegation-ended`.
4. **API.** List the audit entries of the run. Each tool call entry names the delegation ID next to the person.
5. **Docs page.** The developer reads how to start a run under a delegation.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Start with no delegation ID | 422 with the NEW error code `agent.delegation-required`. No run is written. | `GET /api/dustpan/v1/delegations` |
| Start with a delegation that is unknown, ended, of another person, for another agent, or whose mode and scope do not cover the namespace | 422 with the NEW error code `agent.delegation-invalid`. The text names the cause. No run is written. | `GET /api/dustpan/v1/delegations` |
| Start with a delegated token | 403 with the NEW error code `agent.delegated-token-refused`. No run is written. | Sign in to the console, then repeat the call. |
| A tool call is outside the expanded scope of the delegation | The call fails with the existing agent denial. The entry says `delegation-scope`. No tool runs. | `GET /api/dustpan/v1/delegations/{id}` |

Evidence for the claims above:

- A run holds no delegation. Proof: `internal/agent/agent.go:178` (`type Run struct`) has the fields `Subject` and `CredentialID` and no delegation field. `rg -n 'Delegation' internal/agent -g '!*_test.go'` prints nothing.
- The start of a run takes no delegation. Proof: `internal/agent/store.go:18` (`type NewRun struct`) has no delegation field.
- The audit block can carry a delegation ID, and no caller of the agent sets it. Proof: `pkg/audit/audit.go:243` (`DelegationID`); `rg -n 'DelegationID' internal/agent -g '!*_test.go'` prints nothing.
- The loop resolves the credential at each step and before each tool call, and ends the run with `credential-ended`. Proof: `internal/agent/loop.go:64` (`type Credentials interface`) and `internal/agent/agent.go:99` (`StopCredentialEnded`).
- The loop is a durable kind with four steps. Proof: `internal/agent/loop.go:18` (`KindTurn`) and `internal/agent/loop.go:124` (`func TurnKind`).
- Main has no SQL adapter of `agent.RunStore`, no route that starts a run and no code that registers `TurnKind` in the server. Proof: `rg -n 'agent\.RunStore|agent\.TurnKind|agent\.NewRun' -g '*.go' cmd internal pkg` prints only `internal/agent/` and `internal/agent/agenttest/`.
- No change in `openspec/changes/` names the run store adapter, the start route or the delegation of a run. Proof: `rg -l 'RunStore|agent\.NewRun|NewRun' openspec/changes -g '!archive/**'` prints nothing for a proposal or a task list.
- No delegation record or route exists on main. Proof: see the evidence of `person-delegates-dusty`.

## What Changes

This change is the Dusty run job: slice 6 of RFC 121 (a run that names a delegation) and the part of slice 3 that a Dusty tool call needs (the scope and the mode of the delegation, checked with the grants of the person).

- A run records the delegation ID. The start of a run takes it, and a start with no valid delegation fails (RFC 121 decision 9).
- A start from a delegated token fails (RFC 121 decision 9).
- The loop resolves the delegation with the credential at the start of each step and before each tool call. A delegation that ended stops the run with the NEW stop reason `delegation-ended` (RFC 121 decision 16).
- A tool call needs both parts: the grants of the person, and the expanded scope and mode of the delegation. The call is allowed only when both allow it.
- The audit entry of each tool call carries the delegation ID in the agent block and the credential kind `agent-run`.
- The SQL adapter of `agent.RunStore` for each dialect, the routes to start, read, list and cancel a run, and the wiring of `TurnKind` in the server (ADR 120 decision 8 and the route table of ADR 120). These do not exist on main, and a delegated run needs them.
- A user docs page for a run under a delegation.

Out of scope:

- The delegation record, its routes and its ends. They belong to `person-delegates-dusty`.
- Creating Dusty as an agent principal on model connect (RFC 121 decision 7) and the model connection. This change reads the agent ID that the delegation holds.
- The agent class of each catalog action and the `never` class (RFC 121 decision 20).
- The evaluation of both parts for `change.planned`, `change.apply`, `state.write` and the job token, and the work that carries a delegation (RFC 121 decision 31 and slice 3 for jobs).
- The `delegation.*` facts in gates and access limits (RFC 121 decision 44).
- Held requests, the mark of untrusted input and OAuth.
- The reason text of each step in the audit entry (`every-step-audited`).
- The stream of an answer (ADR 314) and the console pages.

Depends on:

- person-delegates-dusty
- A model connect change that creates Dusty as an agent principal (RFC 121 decision 7, slice 6). No such change is in `openspec/changes/` on 25 November 2026. It is a dependency to be written. This proposal does not name it and does not specify it.

Builds on (not a change):

- ADR 354 (accepted, owner-tagged) and RFC 121 decisions 9, 10, 16 and 44.

## Usability

Where the path still costs the person time or surprises them:

- A person who revokes a delegation sees an active run end at its next step, not at once. The read of the run shows the stop reason `delegation-ended` and the time.
- A start that fails names the one cause, so the person does not need to read the delegation to find it.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The run record holds the person and the credential ID. (`internal/agent/agent.go:178 (Run)`)
- The loop resolves the credential before each step and before each tool call. (`internal/agent/loop.go:64 (Credentials)`)
- A credential that ended stops the run with `credential-ended`. (`internal/agent/agent.go:99 (StopCredentialEnded)`)
- The audit block has a delegation ID field. (`pkg/audit/audit.go:243 (DelegationID)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`.

**ADR 354: Agent principals, delegation and the kind agent** (owner-tagged, all decisions).

- Decisions that apply: 6 (current), 2 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote, decision 6: "Decisions 1 to 52 of the "Proposal" of the RFC apply."
- Quote, header: "Changes ADR 120 decisions 13, 14, 15 and 16: Dusty becomes an agent principal, and a run names a delegation."
- Effect: RFC 121 decisions 9, 10 and 16 are the rules of this change.
- Conflict: none.

**ADR 120: Dusty, the built-in agent, runs on durable operations** (owner decisions inside, not tagged as a whole).

- Decisions that apply: 8 (current), 9 (current), 13 (changed by ADR 297 and ADR 354), 14 (changed by ADR 297 and ADR 354), 15 (changed by ADR 354), 16 (changed by ADR 354).
- Changed by: ADR 128, 129, 210, 297, 311, 314, 354, 367. Chain to the end: ADR 129, ADR 210, ADR 297, ADR 311, ADR 314, ADR 354, ADR 367.
- Current rule: this ADR, with ADR 128, 129, 210, 297, 311, 314, 354, 367.
- Quote, decision 8: "Each turn is one durable operation of the kind `agent.turn` (ADR 72 decision 1)."
- Quote, decision 13: "It never stores the credential, and never keeps a principal from one step to the next."
- Quote, decision 14: "the run ends with the stop reason `credential-ended`."
- Quote, decision 15: "Dusty is not a principal."
- Effect: Decision 15 no longer holds (ADR 354). The change keeps the chain of turn operations and the resolve at each step. It adds the delegation to that resolve and a stop reason next to `credential-ended`. It never stores a credential.
- Conflict: none after ADR 354.

**ADR 297: Console sessions are sealed cookies, not stored sessions** (owner direction, 2026-10-31).

- Decisions that apply: the change of ADR 120 decisions 13 and 14 (current).
- Changed by: ADR 301, ADR 333. Chain to the end: ADR 301, ADR 333.
- Current rule: this ADR.
- Quote: "a run that a console session starts resolves the sign-in that it records, not a stored session."
- Effect: The delegation ID is a stored reference, not a session. The run still resolves the sign-in. The change stores no credential.
- Conflict: none.

**ADR 314: Jobs are for user workloads, and Dusty streams its answers** (owner decisions of 2026-10-18).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: ADR 357. Chain to the end: ADR 357.
- Current rule: this ADR, with ADR 357.
- Quote, decision 1: "They are never for platform logic of ours (backups, HTTP webhooks and similar)."
- Effect: A Dusty run is a durable operation, not a job. The change adds no job. The stream is out of scope.
- Conflict: none.

## Open questions

Open questions: none
