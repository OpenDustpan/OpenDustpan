---
version: 2
---

## Why

**Persona: agent.** An Agent program uses the REST API with an access token and tries to list stacks and workspaces, read history, and read state without seeing plain secret values.

The user path this change must make work:

1. **API.** Agent sends a REST request with an access token.
2. **API.** Server checks the grant for the requested action on the object.
3. **API.** Agent lists units, reads history, and reads masked state.
4. **API.** Agent asks for a plain secret value and gets a refusal.
5. **Docs page.** Agent reads the docs page for the agent interface.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Agent lists caller tokens with an invalid access token. | Server answers 401 with error code token.invalid and changes nothing. | `GET /api/server/caller/tokens` |
| Agent lists caller tokens with an expired access token. | Server answers 401 with error code token.ended and changes nothing. | `GET /api/server/caller/tokens` |

Evidence for the claims above:

- Main has an internal agent package and no agent REST route in the pack. Proof: internal/agent
- Main has caller token routes. Proof: ANY /api/server/caller/tokens
- Main has token error codes for invalid and ended tokens. Proof: internal/errcode/catalog_token.go:11
- Main tests read-only reach and namespace principal tokens. Proof: pkg/access/access_test.go:1787

## What Changes

- Record the coarse scope of the AI agent interface: read through the REST API with an access token.
- Keep secret masking and grant checks as ADR 41 requires.
- Reuse the bounds and routes of 39-reading-state-contents for agent state reads.
- Defer all behavior to RFC 41 once it is accepted.

Out of scope:

- Read-only tokens (RFC 20)
- An AI agent for infrastructure (RFC 73)
- Any agent write path

Depends on:

- 39-reading-state-contents

Builds on (not a change):

- read-only tokens (RFC 20)

## Usability

Where the path still costs the person time or surprises them:

- Agent path is not documented yet, so an agent cannot work from the docs page alone.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main defines the token.invalid error code. (`internal/errcode/catalog_token.go:11 (token.invalid)`)
- Main defines the token.ended error code. (`internal/errcode/catalog_token.go:9 (token.ended)`)
- Main tests that a custom role with read-only reach does not read outputs. (`pkg/access/access_test.go:1787 (TestCustomRoleWithUnitReadOnlyDoesNotReadOutputs)`)
- Main tests namespace principal tokens. (`pkg/access/access_test.go:1473 (TestNamespacePrincipalTokens)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 41: The server reads state and masks secrets** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current), 6 (current).
- Changed by: ADR 60 changes decisions 5 and 13; ADR 103 changes decision 13; ADR 146 extends decision 13; ADR 151 changes decision 10; ADR 238 changes decision 12; ADR 240 changes decisions 9 and 13; ADR 240 extends decision 5; ADR 352 extends decision 10. Chain to the end: ADR 60, ADR 103, ADR 146, ADR 151, ADR 238, ADR 240, ADR 352, ADR 63, ADR 64, ADR 65, ADR 68, ADR 72, ADR 126, ADR 210, ADR 334, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 149, ADR 262, ADR 297, ADR 154, ADR 217, ADR 220, ADR 315, ADR 356, ADR 354, ADR 124, ADR 216.
- Current rule: this ADR, with ADR 60, 103, 146, 151, 238, 240, 352.
- Quote: "The server may read state. The server may read the contents of every state that it stores, for each tool. Each read is an endpoint with a declared action on a unit (ADR 13, ADR 17)."
- Quote: "A read masks secret values."
- Quote: "`unit:read`: the unit, its history, and the masked state read."
- Quote: "`unit:read-secrets`: every response that holds a secret value in plain text."
- Effect: A later agent interface keeps these rules: each state read is an endpoint with a declared action, reads mask secret values, and reveal stays a separate action.
- Conflict: none.

**ADR 227: One fresh-authentication window** (not owner-tagged).

- Decisions that apply: 6 (extended).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends. Chain to the end: ADR 301, ADR 320, ADR 339, ADR 354, ADR 332.
- Current rule: this ADR, with ADR 301, 320, 339, 354.
- Quote: "An access token never gives a fresh authentication."
- Effect: An agent that acts with an access token never passes a fresh step, so a later agent interface cannot use tokens for reveal or for a weaker setting.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What can an agent do at first: which read calls, and does reveal apply?
   - Options: Read-only inventory, history and masked state only; Read-only plus gated reveal with audit.
   - Provisional decision (owner review 14 Nov): Read-only inventory, history and masked state only
   - Reason: It matches ADR 41 and keeps secrets closed while the RFC is open. Benchmark: AWS: read-only agent roles with explicit secret access and audit of each secret use.
   - Persona impact: Agent scripts read-only work with an access token. No person loses reach. Escape hatch: Agents get no plain secret values until the reveal rule is decided.
2. Does any agent read ship before read-only tokens (RFC 20) land?
   - Options: Wait for read-only tokens before any agent read ships; Ship agent reads on full access tokens with a warning.
   - Provisional decision (owner review 14 Nov): Wait for read-only tokens before any agent read ships
   - Reason: A read on a broad token can carry write or reveal reach, so the gate stays closed. Benchmark: AWS: least-privilege read-only roles issued before an agent integration ships.
   - Persona impact: Agent waits for a safe token kind. No person shares a broad token. Escape hatch: No agent read path ships until read-only tokens ship.
3. How is masked-only access enforced when a token is too broad?
   - Options: Refuse any agent request whose token holds write or unit:read-secrets reach; Allow and audit broad tokens until RFC 20 lands.
   - Provisional decision (owner review 14 Nov): Refuse any agent request whose token holds write or unit:read-secrets reach
   - Reason: Refuse at the boundary so a broad token never becomes an agent token by use. Benchmark: AWS: deny by default when the credential carries more reach than the task needs.
   - Persona impact: Agent gets a clear 403 naming the token kind it must use. Escape hatch: Refuse the request when the token kind or grant set is undecided.
4. What does each agent read record in the audit log?
   - Options: Audit each agent read as unit:read with the principal subject; Audit only reveal and skip masked reads.
   - Provisional decision (owner review 14 Nov): Audit each agent read as unit:read with the principal subject and principalKind, the channel agent and the object.
   - Reason: These are the same fields as the reveal entries of change 39, so a reviewer can filter agent reads. Benchmark: AWS CloudTrail: each data event records the actor, the action and the object.
   - Persona impact: Site administrators can filter agent reads. Escape hatch: None is needed.
5. Which page size, state size limit, and rate limit apply to agent lists and reads?
   - Options: Reuse the bounds and routes of 39-reading-state-contents for agent state reads; Define separate agent page and size limits.
   - Provisional decision (owner review 14 Nov): Reuse the bounds of state-read in change 39 (200 markers for each page, 256 KB chunks, a 5 MB version bound) and add a rate limit for each token, with an effective value that the API shows.
   - Reason: Two sets of bounds for the same route are not consistent, and each default must be visible. Benchmark: AWS: paginated APIs with documented throttling.
   - Persona impact: Agents see one set of bounds. Escape hatch: The Operator can lower the rate limit.

Questions considered and answered:

- Does the agent interface need its own read and reveal rules? No. ADR 41 decisions 1, 2 and 6 already require a declared action per read, masked secrets by default, and reveal as a separate action.
- Does main already ship an agent REST API? No. The pack names internal/agent and no agent route. The interface is not shipped.
- Can an agent scope be enforced before read-only tokens land? No. Read-only tokens (RFC 20) are not shipped, so the token kind stays an open gating question.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
