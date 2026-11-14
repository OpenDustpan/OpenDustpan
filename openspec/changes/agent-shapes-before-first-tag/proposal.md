---
version: 4
---

## Why

**Persona: site administrator.** A site administrator manages principals and reviews audit history. Before the first tag the stored shapes for agents must stay fixed so a later delegation feature needs no migration, while the agent kind alone must grant nothing.

The user path this change must make work:

1. **API.** Create a principal with kind agent through ANY /api/server/principals with a session credential.
2. **API.** Read the principal back through ANY /api/server/principals/{} and confirm the kind.
3. **API.** Read an audit entry with the agent block through ANY /api/audit/entries/{} and confirm agent, delegation, and reason.
4. **Docs page.** Follow the docs page for agent shapes and run each command as written.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Unknown principal kind rejected with the NEW error code `principal.kind-invalid`. | The server rejects the unknown kind and keeps stored data unchanged. Check the kind value and retry. | `GET /api/server/principals/{subject}` |
| Malformed agent block rejected with the NEW error code `audit.agent-invalid`. | The server rejects the entry and keeps the hash chain unchanged. Read the entry shape in the docs and retry. | `GET /api/audit/entries/{id}` |
| Malformed agent key rejected with the NEW error code `jobtoken.agent-invalid`. | The server rejects mint and stores no token. Check the agent subject and retry. | `POST /api/server/service-tokens` |

Evidence for the claims above:

- Principal domain package exists on main. Proof: pkg/principal where pkg/principal
- Audit domain package exists on main with an audit-read kind and hash coverage test. Proof: pkg/audit where pkg/audit; audit kind audit-read where pkg/audit/audit.go:69; test TestComputeHashIsStableAndCoversTheFields where pkg/audit/audit_test.go:99
- Principal and audit routes exist on main. Proof: routes ANY /api/server/principals, ANY /api/server/principals/{}, ANY /api/audit/entries, ANY /api/audit/entries/{}
- Principal and audit error catalogs exist on main. Proof: error codes principal.invalid at internal/errcode/catalog_principal.go:11 and audit.reason-invalid at internal/errcode/catalog_audit.go:17
- Main proves no agent kind because the pack states identity kinds are person, service, and workload with no agent kind. Proof: RFC 121 excerpt states identity.Kind has person, service and workload, and no agent kind

## What Changes

- Stored principal kind with values person, service, and agent that alone changes no access.
- Stored audit agent block of agent subject, delegation ID, and reason covered by the chain hash.
- Stored job token agent key returned on verification with existing claims unchanged.

Out of scope:

- Agent delegation checks, ceilings, scopes, and held requests
- Dusty principal, grants, and run behavior
- Migration or upgrade of shipped data beyond additive baseline fields
- Console views for agents
- List or filter by kind and audit export format changes

## Usability

Where the path still costs the person time or surprises them:

- The site administrator must learn a new kind value that grants nothing yet, which surprises without the docs note.
- Reason length and ID rules add read time on the principal and audit pages.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Audit HTTP adapter converts stored entries to API output. (`internal/api/audit/audit.go:173 (func toAuditEntry)`)
- Audit hash computation has a stable coverage test. (`pkg/audit/audit_test.go:99 (func TestComputeHashIsStableAndCoversTheFields)`)
- Route declarations have a hook test that fails on unknown shapes. (`pkg/dustpan/route_declarations_hook_internal_test.go:17 (func TestRouteHookFailsOnAnUnknownShape)`)
- Job token test helper builds an audit entry for token actions. (`pkg/jobtoken/jobtokentest/jobtokentest.go:204 (func auditEntry)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Does the PM adopt the provisional stored values person, service, and agent for principal kind, the audit agent block of agent, delegation, and reason, and the job token agent key before the first tag? Provisional decision (owner review 14 Nov): yes, additive shapes only with no access change.
   - Options: Fix kind values as person, service, agent as provisional PM decision before the first tag; Leave kind unfixed until a later RFC.
   - Provisional decision (owner review 14 Nov): Fix kind values as person, service, agent as provisional PM decision before the first tag
   - Reason: The pack lists no ADR that decides these shapes and the roadmap calls them one-way doors before the first tag. Benchmark: AWS practice for IAM principal kinds keeps a fixed allowlist of kinds and rejects unknown kinds at creation.
   - Persona impact: No persona loses access; the agent kind alone grants nothing until a later change. Escape hatch: Unknown kinds stay rejected; existing person and service rows keep current access with no change.
2. Which accepted ADRs decide the audit hash chain, the principal model, and the job token claims that this additive shape touches?
   - Options: Name the accepted ADRs that decide the audit hash chain, the principal model, and the job token claims and quote their decision lines; Confirm no accepted ADR decides them and proceed as provisional PM decision.
   - Provisional decision (owner review 14 Nov): Name the accepted ADRs that decide the audit hash chain, the principal model, and the job token claims and quote their decision lines
   - Reason: The context pack lists zero ADRs, so this draft cannot quote an ADR decision line without inventing a path or quote. Benchmark: ISO 27001 practice keeps a record of each decision that controls log integrity and identity records before the change ships.
   - Persona impact: No persona loses access; the question only names the ADR basis for the audit, principal, and token shapes. Escape hatch: This change ships only additive stored fields with no access change, so current grants and current hash checks keep working.

Questions considered and answered:

- Does an accepted ADR already fix the agent shapes? The pack lists zero ADRs. The pack lists RFC 121 as accepted but an RFC is an open question, not a decision. This change fixes only additive stored shapes with no access change, so it claims no ADR conflict and the provisional values stay a PM decision in open questions.
- What is the boundary of this change versus RFC 121 behavior? The milestone roadmap names three stored shapes as one-way doors before the first tag: principal kind, audit agent block in the hash chain, and job token agent key. This change fixes only those stored fields with no delegation checks, no Dusty behavior, and no console views.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
