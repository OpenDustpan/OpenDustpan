Roadmap: `docs/roadmap.md:779` (Every step is audited).

## Why

**Persona: site administrator.** A site administrator reviews what an agent did in an installation. The administrator needs each delegated agent step in the audit log with the agent, the delegation and the reason, and a hash chain that verifies.

The user path this change must make work:

1. **Console.** Delegate to the agent from the console, then run one agent step through the API.
2. **API.** List the entries and read the agent block (with the delegation ID) and the reason of each delegated step.
3. **API.** Run the chain check and confirm that it verifies.
4. **Docs page.** Follow the audit log guide to repeat the list and check commands.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Read of an entry in a namespace the caller may not read | 404 with the existing error code `audit.entry-not-found`. | `GET /api/audit/entries` |
| Read or chain check during a database gap | 503 with `Retry-After: 5` and the existing error code `audit.entry-read-unavailable`. | `GET /api/audit/check` |
| A delegated call with an invalid reason | 400 with the existing error code `audit.reason-invalid`. The answer repeats nothing of the input. | `GET /api/audit/entries` |

Evidence for the claims above:

- The audit log port exists on main. Proof: `pkg/audit/audit.go`.
- The entries route and the check route exist. Proof: `internal/api/audit/routes.go:49` and `:52`.
- The three error codes exist. Proof: `internal/errcode/catalog_audit.go:8` (`audit.entry-not-found`), `:11` (`audit.entry-read-unavailable`) and `:17` (`audit.reason-invalid`). The earlier draft of this change called `audit.reason-invalid` NEW. It is not.
- The entry has a `Reason` field and a check for it. Proof: `pkg/audit/audit.go:153` (`MaxReasonLen` is 500), `:182` (`ErrInvalidReason`) and `:195` (`ValidateReason`).
- The agent block exists on main with six of its seven fields. Proof: `pkg/audit/audit.go:256` (`NewOrigin`). The field `delegationID` of ADR 380 depends on a delegation record that main does not have (`rg -n 'delegationID' -g '*.go' .` finds no production use).
- The audit export is covered by e2e and the guide exists. Proof: `e2e/audit_export_test.go` and `docs/guides/audit-log.md`.

## What Changes

- Each step that an agent takes under a delegation writes one audit entry. The entry carries the agent block of ADR 380 with `delegationID` set, and the existing `Reason` field of the entry (ADR 380 decision 1: the agent block holds seven fields, and the reason of a delegated step stays the existing field of the entry).
- Each refused delegated call writes one denial entry that names the delegation where it is known and the cause.
- Audit reads return the block and the reason. The chain check covers them. The hash input has no version and no path for old entries (ADR 380 decision 3).
- An entry of an action with no namespace goes to the installation scope chain, as an entry of a person does on main.

Out of scope:

- The seven-field block itself. `audit-entry-has-no-agent` owns the type and the hash input of the other six fields.
- Delegation create, renew, list and revoke, and their audit entries. They are in `person-delegates-dusty`.
- The refusal of delegated work after an end. It is in `revoke-stops-work`.
- Change order drafting and confirmation.
- Model connect and the Dusty run loop.

Depends on:

- `person-delegates-dusty` (the delegation ID and `Resolve`).
- `audit-entry-has-no-agent` (still an active change; it makes the agent block).
- A change that makes a tool call carry a delegation and a reason (RFC 121 slices 3 and 6). No such change is in `openspec/changes/` on 20 November 2026. It is a dependency to be written.
- `audit-agent-block` is archived and landed. It is no longer a dependency.

## Usability

Where the path still costs the person time or surprises them:

- The reader sees the delegation and the reason in one entry with no second lookup.
- A chain failure names the first bad sequence number and the one check command.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Context writes are audited on main. (`pkg/dustpan/context_test.go:747 (TestContextWritesAreAudited)`)
- Audit chain families are tested on main. (`pkg/access/audit_chain_families_test.go:11 (TestAuditChainFamilies)`)
- User-agent truncation for audit entries is tested on main. (`pkg/audit/user_agent_test.go:14 (TestTruncateUserAgentKeepsRunes)`)
- The audit kind `audit-read` is defined on main. (`pkg/audit/audit.go:69 (audit-read)`)
- The error code `audit.entry-not-found` is defined on main. (`internal/errcode/catalog_audit.go:8 (audit.entry-not-found)`)
- `NewOrigin` refuses a block that has only `delegationID` set, unless `breakGlass` is set. (`pkg/audit/audit.go:256 (NewOrigin)`) See open question 2.

## ADR check

Each quote is copied from the file in `docs/adr/`. The owner tags, the "Changed by" chain and the "Current rule" come from `docs/adr/INDEX.md`.

**ADR 380: The delegation in the agent block of an audit entry** (owner-tagged).

- Decisions that apply: 1, 2 and 3 (current). Changed by: -. Chain to the end: none. Current rule: this ADR. It changes ADR 126 decision 10.
- Quote, decision 1: "The agent block of ADR 126 decision 10 holds seven fields in this fixed order: `agentRun`, `agentSession`, `tool`, `argsDigest`, `breakGlass` as `true` or `false`, `secretStage`, and `delegationID`."
- Quote, decision 1: "`delegationID` is the stable ID of the delegation of ADR 354 under which the agent acted (ADR 362), and it is empty when the step had no delegation."
- Quote, decision 3: "The block keeps the text `agent/1`."
- Effect: the delegation ID is the seventh field of the block. There is no block version and no compatibility path.
- Conflict: none.

**ADR 126: The audit entry of an agent step** (not owner-tagged).

- Decision that applies: 10 (changed by ADR 380). Changed by: ADR 129, 380. Chain to the end: ADR 129, 380. Current rule: this ADR, with ADR 129, 380.
- Effect: the block is the one that ADR 380 gives.
- Conflict: none.

**ADR 120: Dusty, the built-in agent, runs on durable operations** (not owner-tagged).

- Decision that applies: 5 (current). Changed by: ADR 128, 129, 210, 297, 311, 314, 354, 367. Chain to the end: ADR 128, 129, 210, 297, 311, 314, 354, 367, 190, 222, 227, 286, 247, 301, 333, 357, 225, 299, 300, 344, 365, 335, 351, 320, 339, 373, 385, 372, 328, 359, 368, 377, 332, 376. Current rule: this ADR, with ADR 128, 129, 210, 297, 311, 314, 354, 367.
- Quote: "The audit log must be on. Every tool call writes an audit entry (ADR 126)."
- Effect: a tool call whose entry cannot be written is refused.
- Conflict: none.

**ADR 354: Agent principals, delegation and the kind agent** (owner-tagged). RFC 121 decision 51 lists the entries of a delegation: "Each delegation create, renew, use, revoke, expiry and end ... writes an entry."

- Current rule: this ADR. Conflict: none.

**ADR 298: PostgreSQL failover readiness** (not owner-tagged). Decision 2 is current; changed by ADR 337 (decision 9).

- Quote: "Requests fail fast with 503."
- Effect: a read or a check that needs the database during a gap answers 503. Conflict: none.

## Open questions

Open questions: 4, parked

The questions below are not decided. No ADR line and no RFC line settles them. The owner decides each one.

1. **How does a delegated call give its reason, and what may the reason hold?** `audit.ValidateReason` exists and the limit is 500. The earlier draft of this change asked for a required body field `reason` on each tool call, and for ASCII 0x20 to 0x7E only. No ADR line or RFC line says either. Options:
   - (a) Use `ValidateReason` as it is, and take the reason from the call. Cost: each tool needs a place for it.
   - (b) Add the ASCII allowlist. Cost: a reason in a language other than English fails, which the person may not expect.
   - (c) The server writes the reason from the step (tool name and run) and the agent supplies none. Cost: the reason does not say why.
   - Decides: the owner.
2. **How does a delegated entry get a block with only `delegationID`?** `NewOrigin` (`pkg/audit/audit.go:256`) refuses that. A delegation create, a renew and an end entry have a delegation and no agent run. Options:
   - (a) Change `NewOrigin` so that `delegationID` alone is valid. Cost: a change to a function with an ADR 380 test.
   - (b) Lifecycle entries carry the delegation in the entry fields and no block. Cost: two places hold the delegation ID, and a reader joins them.
   - Decides: the owner. This question is the same as question 6 of `person-delegates-dusty`.
3. **What does the denial entry hold for an ID that names no delegation?** The earlier draft stored the text `unknown-delegation` plus 64 characters of the input. No line asks for it. Options: (a) store nothing of the input; (b) store a bounded, escaped part of it. Cost of (b): an attacker may write text into the log. Decides: the owner.
4. **What does the caller get when a denial entry cannot be written?** The earlier draft said 503 with `audit.entry-read-unavailable`. That code names a read. Options: (a) reuse it; (b) a NEW code for a failed write. Cost of (b): one more code in the catalog. Decides: the owner.

Questions considered and answered:

- Does a delegated step run when the audit log is off? No. ADR 120 decision 5.
- What if the append of a step fails? The step is refused with no effect (ADR 120 decision 5, with RFC 121 decision 51).
- How do old entries verify after the hash input changes? The earlier answer (hash-input versions 1 and 2) is withdrawn. ADR 380 decision 3 gives no block version and no compatibility path, because no tag shipped (rule: no shim for a shape that no tag shipped).
