Roadmap: `docs/roadmap.md:779` (Every step is audited).

## Why

**Persona: site administrator.** A site administrator reviews what an agent did in an installation. The administrator needs each agent step in the audit log with the agent, the delegation and the reason, and a hash chain that verifies.

The user path this change must make work:

1. **Console.** Delegate to the agent from the console, then run one agent step through the API with a reason.
2. **API.** List the entries of the namespace and read the delegation and reason of each agent step alongside the agent block of audit-entry-has-no-agent.
3. **API.** Run the chain check for the namespace and confirm it verifies.
4. **Docs page.** Follow the audit log guide to repeat the list and check commands.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Read of an agent entry in a namespace the caller may not read fails. | 404 with error code audit.entry-not-found. List entries with `GET /api/audit/entries`. | `GET /api/audit/entries` |
| Read or chain verification during a database gap fails. | 503 with Retry-After 5 and error code audit.entry-read-unavailable. | `GET /api/audit/check` |
| Delegated call with a missing or invalid reason fails. | 400 with error code audit.reason-invalid that repeats nothing of the input and writes no entry. | `GET /api/audit/entries` |

Evidence for the claims above:

- The audit log port exists on main. Proof: pkg/audit
- The audit entries read route exists on main. Proof: ANY /api/audit/entries
- The audit chain check route exists on main. Proof: ANY /api/audit/check
- The audit export route is covered by e2e on main. Proof: e2e/audit_export_test.go
- The audit log guide exists on main. Proof: docs/guides/audit-log.md

## What Changes

- Each delegated agent step writes one audit entry that names the delegation and the reason alongside the agent block of audit-entry-has-no-agent.
- Each denied delegated call writes one denial entry that names the delegation where known and the refusal cause alongside the agent block of audit-entry-has-no-agent.
- Audit reads return the delegation and reason of each agent entry alongside the agent block of audit-entry-has-no-agent.
- Namespace chain verification covers delegation and reason fields and names the first bad sequence number.

Out of scope:

- Agent field block, covered by audit-entry-has-no-agent
- Delegation create, list and revoke
- Change order propose and confirm
- Model connection and run loop

Depends on:

- person-delegates-dusty
- audit-entry-has-no-agent
- `person-delegates-dusty` (order of the milestone review)
- `audit-entry-has-no-agent` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The reader sees delegation and reason in one entry without a second lookup.
- Chain failure names the first bad sequence number and the one check command.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Context writes are audited on main. (`pkg/dustpan/context_test.go:746 (TestContextWritesAreAudited)`)
- Audit chain families are tested on main. (`pkg/access/audit_chain_families_test.go:11 (TestAuditChainFamilies)`)
- User-agent truncation for audit entries is tested on main. (`pkg/audit/user_agent_test.go:14 (TestTruncateUserAgentKeepsRunes)`)
- The audit kind audit-read is defined on main. (`pkg/audit/audit.go:69 (audit-read)`)
- The error code audit.entry-not-found is defined on main. (`internal/errcode/catalog_audit.go:8 (audit.entry-not-found)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 120: Dusty, the built-in agent, runs on durable operations** (not owner-tagged).

- Decisions that apply: 5 (current).
- Changed by: ADR 128 changes decision 22; ADR 129 changes decisions 3, 7 and 20; ADR 129 extends; ADR 210 changes; ADR 297 changes decisions 13 and 14; ADR 311 extends; ADR 314 extends; ADR 354 changes decisions 13, 14, 15 and 16. Chain to the end: ADR 128, ADR 129, ADR 210, ADR 297, ADR 311, ADR 314, ADR 354, ADR 190, ADR 222, ADR 227, ADR 286, ADR 247, ADR 301, ADR 333, ADR 357, ADR 225, ADR 299, ADR 300, ADR 344, ADR 365, ADR 335, ADR 351, ADR 320, ADR 339, ADR 328, ADR 359, ADR 332.
- Current rule: this ADR, with ADR 128, 129, 210, 297, 311, 314, 354.
- Quote: "The audit log must be on. Every tool call writes an audit entry (ADR 126)."
- Effect: The change keeps decision 5 that every tool call writes an audit entry and that the step is refused when the append fails. It does not change the refusal of the connect route when the audit log is off.
- Conflict: none.

**ADR 354: Agent principals, delegation and the kind agent** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The person who delegated may confirm (owner, 2026-11-07)."
- Effect: The change follows the current rule that an agent acts for the person who delegated under a stored delegation. It writes the delegation on each entry and does not change who may confirm.
- Conflict: none.

**ADR 298: PostgreSQL failover readiness** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 337 changes decision 9. Chain to the end: ADR 337.
- Current rule: this ADR, with ADR 337.
- Quote: "Requests fail fast with 503. During the gap, each request that needs the database gets 503 with `Retry-After: 5`, in the shape of its API (W3, ADR 280 decision 4)."
- Effect: Reads and chain checks that need the database follow the fail-fast rule during a database gap.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does a delegated agent step run when the audit log is off? No. ADR 120 decision 5 keeps the refusal of the connect route when the audit log is off, so a delegated run does not start without an audit log.
- Does a reason with hostile text or secret text enter the audit entry? No. A reason outside the allowlist is refused with audit.reason-invalid and no entry is written. Transcript text never enters an entry.
- What happens when the audit append fails or two agent steps append at the same time? When the step append fails the step is refused with no effect. When the denial append fails the call is still refused, with 503 audit.entry-read-unavailable. Sequence numbers come from the database in the same transaction, so this works across instances.
- How do entries written before this change verify after the hash input changes? Each entry stores a hash-input version. Old entries verify with version 1 input. New entries verify with version 2 input that adds the stored delegation and reason fields alongside the agent block of audit-entry-has-no-agent. The version is hashed, so stripping the delegation or reason fields fails verification. No numbered migration and no shim are used.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
