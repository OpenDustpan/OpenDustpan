---
version: 4
---

## Why

**Persona: developer.** A Developer needs to read what a stack or workspace holds without seeing secret values, and to reveal one value when needed, with a clear audit record.

The user path this change must make work:

1. **API.** Developer lists versions of a stack or workspace from the docs page, then opens masked state in the API.
2. **API.** Developer reveals one value path when plain text is needed and finds the reveal in the audit log.
3. **Docs page.** Developer follows the docs page to fix a 403 with the one access command.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| masked read without unit:read grant | 403 with no secret bytes, body names the missing action and the one docs command. | `curl -H "Authorization: Bearer $TOKEN" /api/stacks/{org}/{project}/{stack}/state/{version}/resources` |
| reveal without unit:read-secrets grant | 403 with no secret bytes, body names unit:read-secrets and the one docs command. | `curl -X POST -H "Authorization: Bearer $TOKEN" /api/stacks/{org}/{project}/{stack}/state/{version}/reveal` |
| bad version or bad value path | 400 with the NEW error code `state.bad-path`, body repeats no secret value. | `curl -H "Authorization: Bearer $TOKEN" /api/stacks/{org}/{project}/{stack}/state/{version}/outputs` |

Evidence for the claims above:

- Main already serves Pulumi masked state reads for resources, one resource, outputs, diff and reveal. Proof: ANY /api/stacks/{}/{}/{}/state/{}/reveal, ANY /api/stacks/{}/{}/{}/state/{}/resources, ANY /api/stacks/{}/{}/{}/state/{}/resources/{}, ANY /api/stacks/{}/{}/{}/state/{}/outputs, ANY /api/stacks/{}/{}/{}/state/diff in route table or API description

## What Changes

- Keep existing Pulumi masked state and reveal routes unchanged
- Add the NEW tool-neutral routes `GET /api/dustpan/v1/units/{ref}/state` and `POST /api/dustpan/v1/units/{ref}/state/reveal` with unit:read and unit:read-secrets
- Add single-path Reveal with one reveal entry per plain-text answer with channel and principalKind
- Add the NEW error codes `unit.forbidden` and `state.bad-path` with no secret bytes in error text
- Declare 200 markers per page, opaque cursor, 256 KB chunks, 5 MB version bound and 30 second timeout

Out of scope:

- OpenTofu json-state rendering changes
- history redaction and purge writes
- agent read_state tool changes; the two-entry rule stays unchanged
- console views beyond one docs page

## Usability

Where the path still costs the person time or surprises them:

- Masked output stays machine-readable with stable value paths.
- Reveal needs one extra explicit step, which costs time but keeps secrets safe.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- main accepts a Pulumi git head check in the Pulumi API (`internal/api/pulumi/git_head_test.go:13 (func TestAcceptedGitHead)`)
- main maps state damage to content errors (`pkg/dustpan/state_damage_internal_test.go:24 (func TestStateDamageMapsOnlyContentErrors)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 41: The server reads state and masks secrets** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current), 3 (current), 4 (current), 6 (current), 14 (current).
- Changed by: ADR 60 changes decisions 5 and 13; ADR 103 changes decision 13; ADR 146 extends decision 13; ADR 151 changes decision 10; ADR 238 changes decision 12; ADR 240 changes decisions 9 and 13; ADR 240 extends decision 5; ADR 352 extends decision 10. Chain to the end: ADR 60, ADR 103, ADR 146, ADR 151, ADR 238, ADR 240, ADR 352, ADR 63, ADR 64, ADR 65, ADR 68, ADR 72, ADR 126, ADR 210, ADR 334, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 149, ADR 262, ADR 297, ADR 154, ADR 217, ADR 220, ADR 315, ADR 356, ADR 354, ADR 124, ADR 216.
- Current rule: this ADR, with ADR 60, 103, 146, 151, 238, 240, 352.
- Quote: "The server may read state."
- Quote: "A read masks secret values."
- Quote: "Value path. A value path is a JSON Pointer (RFC 6901) into the state document as the tool stores it."
- Quote: "Reveal is a separate call."
- Effect: This change keeps the masked read, value path, single-path reveal and unit:read and unit:read-secrets rules, and lands each route with its e2e step and declared actions.
- Conflict: none.

**ADR 151: Redaction, and an item key for each history item** (not owner-tagged).

- Decisions that apply: 4 (current), 11 (current).
- Changed by: ADR 154 changes decisions 3, 8, 15, 28 and 29; ADR 210 changes; ADR 217 changes decision 2; ADR 220 changes decision 2. Chain to the end: ADR 154, ADR 210, ADR 217, ADR 220, ADR 238, ADR 247, ADR 286, ADR 273, ADR 363, ADR 315, ADR 356.
- Current rule: this ADR, with ADR 154, 210, 217, 220.
- Quote: "A shredded record answers `secret.ErrShredded` before any blob read."
- Quote: "What a redaction is. A redaction destroys the content of one old history item and keeps its metadata."
- Effect: A reveal of a shredded item answers shredded before any blob read; a reveal of redacted content is refused.
- Conflict: none.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 247 extends decision 3; ADR 286 changes. Chain to the end: ADR 247, ADR 286.
- Current rule: this ADR, with ADR 247, 286.
- Quote: "Paths that stay outside `/api/dustpan/v1`. A protocol endpoint that an external system stores (a CLI, a runner, an IdP, a cloud trust policy) is not part of `/api/dustpan/v1` and has no API version."
- Effect: The NEW tool-neutral masked read and reveal live under /api/dustpan/v1; the stored CLI and runner paths do not move.
- Conflict: none.

**ADR 240: A state download by a person is a reveal, and a principal has a kind** (not owner-tagged).

- Decisions that apply: 2 (current), 5 (current), 8 (current).
- Changed by: ADR 297 extends; ADR 354 changes decisions 1, 3 and 4. Chain to the end: ADR 297, ADR 354, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 297, 354.
- Quote: "The entry records the channel as the client declares it (ADR 41 decision 13, ADR 60 decision 5)."
- Quote: "An audit entry gets the field `principalKind`."
- Quote: "Fail closed stays. The server writes the entry before the first byte of the download."
- Effect: Each reveal writes its entry of kind reveal with channel and principalKind before the first byte and fails closed.
- Conflict: none.

**ADR 126: Every agent tool call writes an audit entry** (not owner-tagged).

- Decisions that apply: 5 (current).
- Changed by: ADR 129 extends decisions 6 and 7. Chain to the end: ADR 129, ADR 210, ADR 247, ADR 286.
- Current rule: this ADR, with ADR 129.
- Quote: "A `read_state` call with secret values writes its `agent-tool` entry and the `reveal` entry of ADR 41, with the count of values (ADR 124 decision 11)."
- Effect: An agent reveal keeps the two-entry rule; this change does not alter it.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does the server read state contents and mask secret values? Yes. ADR 41 decisions 1, 2, 3, 4 and 6 answer it.
- How do the per-tool masking rules and the shredded-item rule apply? ADR 41 decisions 7 and 8 answer the masking rules. ADR 151 decision 4 answers shredded reads.
- Where do the NEW routes live and what lands first? ADR 41 decision 14 answers it: e2e step first, actions in the route table. ADR 210 decision 2 answers the prefix rule.
- What audit entry does a reveal write and when? ADR 41 decision 5 with ADR 240 decisions 2, 5 and 8: one reveal entry before the first byte, fail closed.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
