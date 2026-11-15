---
version: 2
---

## Why

**Persona: developer.** Developer manages stacks and workspaces across two tools and tries to ask one agent for status and safe next steps without learning two CLIs.

The user path this change must make work:

1. **Docs page.** Developer reads how to ask the agent about stacks and workspaces of a namespace
2. **API.** Agent calls the tool-neutral REST API with an access token
3. **Console.** Developer reviews the audit entry of each agent action

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Agent asks for an action outside its grants | 403 with error code `access.denied`. The answer names the denied action. | `ANY /api/server/caller/access` |
| Agent calls with an expired access token | 401 with error code `token.ended`. The answer names the expired token. | `POST /token` |

Evidence for the claims above:

- Main revokes access tokens in the local-user store. Proof: pkg/store/postgres/localusers.go:366 (func localUserTx.RevokeAccessTokens)
- Main defines the tool-operation audit kind. Proof: pkg/audit/audit.go:44

## What Changes

- Record the Later intent for one AI agent for infrastructure across both tools
- Record the dependency on the interface for AI agents and access tokens
- Defer all behavior, routes and audit shape to the RFC

Out of scope:

- Interface for AI agents itself
- Access token shapes
- Runner execution
- Kubernetes operator

Depends on:

- 41-interface-ai-agents

Builds on (not a change):

- read-only and access tokens (RFC 20)

## Usability

Where the path still costs the person time or surprises them:

- Agent path is not yet usable; docs will show one scripted read-only flow first.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main revokes access tokens in the local-user store. (`pkg/store/postgres/localusers.go:366 (func localUserTx.RevokeAccessTokens)`)
- Main revokes access tokens through the local-user store port. (`pkg/store/internal/localuserstore/localuserstore.go:129`)
- Main keeps namespace principal token checks in access tests. (`pkg/access/access_test.go:1473 (func TestNamespacePrincipalTokens)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 41: The server reads state and masks secrets** (not owner-tagged).

- Decisions that apply: 1 (current), 6 (current).
- Changed by: ADR 60 changes decisions 5 and 13; ADR 103 changes decision 13; ADR 146 extends decision 13; ADR 151 changes decision 10; ADR 238 changes decision 12; ADR 240 changes decisions 9 and 13; ADR 240 extends decision 5; ADR 352 extends decision 10. Chain to the end: ADR 60, ADR 103, ADR 146, ADR 151, ADR 238, ADR 240, ADR 352, ADR 63, ADR 64, ADR 65, ADR 68, ADR 72, ADR 126, ADR 210, ADR 334, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 149, ADR 262, ADR 297, ADR 154, ADR 217, ADR 220, ADR 315, ADR 356, ADR 354, ADR 124, ADR 216.
- Current rule: this ADR, with ADR 60, 103, 146, 151, 238, 240, 352.
- Quote: "The server may read state. The server may read the contents of every state that it stores, for each tool."
- Quote: "Actions. The actions follow the names in `pkg/identity`: - `unit:read`: the unit, its history, and the masked state read."
- Effect: A future agent reads state only through masked reads with unit:read and reveals secrets only through the separate reveal call.
- Conflict: none.

**ADR 227: One fresh-authentication window** (owner-tagged: decision 2).

- Decisions that apply: 2 (current), 6 (extended).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends. Chain to the end: ADR 301, ADR 320, ADR 339, ADR 354, ADR 332.
- Current rule: this ADR, with ADR 301, 320, 339, 354.
- Quote: "One window everywhere (owner). The installation has one setting, `freshAuthSeconds`, with the default 900 (15 minutes)."
- Quote: "Errors. A step with no fresh authentication answers `403` with the error code `mfa.fresh-step-required` and changes nothing (ADR 212 decision 3)."
- Effect: An access token never gives a fresh authentication, so agent reveal on its own token is excluded for now.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What is the first allowed scope of the infrastructure agent across both tools?
   - Options: Read-only question answering over masked state and history; Plan and preview proposals with human approval before apply; Full autonomous apply across stacks and workspaces.
   - Provisional decision (owner review 14 Nov): Read-only question answering over masked state and history
   - Reason: AWS practice grants read first and adds mutating actions after review. Benchmark: AWS least privilege: read first, add writes after review
   - Persona impact: Read-only first upsets no persona; writers keep the CLI path. Escape hatch: Scope the first agent to masked reads with no reveal until approvals exist.
   - Owner-tagged ADRs involved: ADR 227 (their words do not change).
2. How does an agent reveal a secret value when an access token never gives a fresh authentication?
   - Options: No agent reveal; masked reads only; Agent reveal with human approval and fresh authentication by a person.
   - Provisional decision (owner review 14 Nov): No agent reveal; masked reads only
   - Reason: PCI DSS practice needs a fresh human check before a secret leaves the server. Benchmark: PCI DSS: human check before secret disclosure
   - Persona impact: Masked-only upsets no persona; a person reveals through the console. Escape hatch: Forbid agent reveal until the RFC defines a human-approved path.
   - Owner-tagged ADRs involved: ADR 227 (their words do not change).
3. How does the agent handle a hostile prompt in state or history content, a leaked token and a token revoked mid-session?
   - Options: Fail closed: treat state and history as untrusted, deny every mutating call that content triggers, end the session on revoke; Allow read-only continuation after revocation.
   - Provisional decision (owner review 14 Nov): Fail closed: treat state and history as untrusted, deny every mutating call that content triggers, end the session on revoke
   - Reason: AWS practice denies on revoked credentials and isolates untrusted content. Benchmark: AWS: deny on revoked credentials
   - Persona impact: Fail closed upsets no persona; a person reissues the token and retries. Escape hatch: A person reissues the token and starts a new session.

Questions considered and answered:

- Can the infrastructure agent be specified before the agent interface lands? No. The roadmap row waits on the interface for AI agents and access tokens. This coarse change records intent only.
- How would the agent see secret values? ADR 41 decisions 1, 2, 4 and 6: masked reads by default, reveal as a separate call. ADR 227 decision 6: an access token never gives a fresh authentication, so agent reveal stays an open question.
- Is this change spec-ready? No. It holds open questions, so it is not built until the RFC is accepted and the questions are answered.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
