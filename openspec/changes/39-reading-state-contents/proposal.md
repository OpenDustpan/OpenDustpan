---
version: 6
---

## Why

**Persona: developer.** A Developer needs to read what a stack or workspace holds without seeing secret values.

The user path this change must make work:

1. **API.** Developer lists versions of a stack or workspace from the docs page, then opens masked state in the API.
2. **Docs page.** Developer follows the docs page to fix a 403 with the one access command.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| masked read without unit:read grant | 403 with no secret bytes, body names the missing action and the one docs command. | `curl -H "Authorization: Bearer $TOKEN" /api/stacks/{org}/{project}/{stack}/state/{version}/resources` |
| bad version or bad value path | 400 with the NEW error code `state.bad-path`, body repeats no secret value. | `curl -H "Authorization: Bearer $TOKEN" /api/stacks/{org}/{project}/{stack}/state/{version}/outputs` |

Evidence for the claims above:

- Main already serves Pulumi masked state reads for resources, one resource, outputs and diff. Proof: ANY /api/stacks/{}/{}/{}/state/{}/resources, ANY /api/stacks/{}/{}/{}/state/{}/resources/{}, ANY /api/stacks/{}/{}/{}/state/{}/outputs, ANY /api/stacks/{}/{}/{}/state/diff in route table or API description

## What Changes

- Keep existing Pulumi masked state routes unchanged
- Add the NEW tool-neutral route `GET /api/dustpan/v1/units/{ref}/state` with unit:read
- Add the NEW error codes `unit.forbidden` and `state.bad-path` with no secret bytes in error text
- Declare 200 markers per page, opaque cursor, 256 KB chunks, 5 MB version bound and 30 second timeout
- Single-path reveal moves to the change `39-reveal-state-value`, which depends on this change

Out of scope:

- single-path reveal and its audit entry (change `39-reveal-state-value`)
- OpenTofu json-state rendering changes
- history redaction and purge writes
- agent read_state tool changes; the two-entry rule stays unchanged
- console views beyond one docs page

## Usability

Where the path still costs the person time or surprises them:

- Masked output stays machine-readable with stable value paths.
- Masked output shows no plain secret; the next change adds the explicit reveal step.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- main accepts a Pulumi git head check in the Pulumi API ((read only) `internal/api/pulumi/git_head_test.go`, line 13 (func TestAcceptedGitHead))
- main maps state damage to content errors ((read only) `pkg/dustpan/state_damage_internal_test.go`, line 24 (func TestStateDamageMapsOnlyContentErrors))

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
- Effect: This change keeps the masked read, value path and unit:read rules, and lands the route with its e2e step and declared actions. The reveal rules land in `39-reveal-state-value`.
- Conflict: none.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 247 extends decision 3; ADR 286 changes. Chain to the end: ADR 247, ADR 286.
- Current rule: this ADR, with ADR 247, 286.
- Quote: "Paths that stay outside `/api/dustpan/v1`. A protocol endpoint that an external system stores (a CLI, a runner, an IdP, a cloud trust policy) is not part of `/api/dustpan/v1` and has no API version."
- Effect: The NEW tool-neutral masked read lives under /api/dustpan/v1; the stored CLI and runner paths do not move.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does the server read state contents and mask secret values? Yes. ADR 41 decisions 1, 2, 3, 4 and 6 answer it.
- How do the per-tool masking rules apply? ADR 41 decisions 7 and 8 answer them.
- Where do the NEW routes live and what lands first? ADR 41 decision 14 answers it: e2e step first, actions in the route table. ADR 210 decision 2 answers the prefix rule.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
