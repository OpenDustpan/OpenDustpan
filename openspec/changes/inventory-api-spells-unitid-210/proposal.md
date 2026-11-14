---
version: 1
---

Issue: #1282, #1330, #1331, #1362.

## Problem

An agent that reads the inventory through the REST API gets unitID while ADR 210 requires camelCase unitId, so generated clients make two names for one concept and scripts break on rename.

## Evidence

- The API description reflector defines the LegacyFieldNames allowlist. Proof: internal/api/apidesc/body.go:252 (func LegacyFieldNames)
- The inventory handler serves the unit list. Proof: internal/api/inventory/inventory.go:432 (func Handler.listUnits)
- The jobs handler serves getJob. Proof: internal/api/jobs/serve.go:177 (func Handler.getJob)
- No LegacyFieldNames caller exists outside the apidesc package in non-test code. Proof: rg -n -F --glob '!*_test.go' 'apidesc.LegacyFieldNames' pkg internal cmd matches 0
- The context operations name the ContextObject schema. Proof: internal/api/objectcontext/operations.go:70
- The audit answer carries tokenID, sourceIP and requestID spellings today. Proof: internal/api/audit/audit.go:137

## Fix boundary

- Add unitId beside unitID in Unit, DeletedUnit and ContextObject answers, deprecated.
- Add versionId beside versionID in the closed getJob answer, deprecated.
- Add camelCase beside each old acronym spelling in audit, installation, runner and agent-result answers in the same pass, deprecated.
- Add sequenceId, operationId and url spellings beside old spellings in history bodies, job store bodies and URL fields, deprecated.
- Accept renamed input fields with the new spelling and the old spelling with equal values and refuse different values with the NEW 400 error code request.field-conflict.
- Name each field of each new route of this milestone with camelCase names unitId and versionId only.
- Remove old spellings and their LegacyFieldNames entries after the reader move and before stable.

Out of scope:

- Binary upload body of stageRegistryUpload (#1331).
- Full operation coverage of the Go client generator (#1362).
- Console release train that moves readers to the new names.

## Check

- Add failing test for dual unitId and unitID on listUnits and get-by-ID in internal/api/inventory/inventory.go:432. Check: NEW test TestDualUnitId in internal/api/inventory fails on main, passes after fix.
- Add failing test for dual unitId on DeletedUnit list in internal/api/inventory. Check: NEW test TestDualDeletedUnitId in internal/api/inventory fails on main, passes after fix.
- Add failing test for dual versionId and versionID on getJob closed answer and absence on open answer in internal/api/jobs/serve.go:177. Check: NEW test TestDualVersionId in internal/api/jobs fails on main, passes after fix.
- Add failing test for dual unitId and unitID on ContextObject in internal/api/objectcontext/operations.go:70. Check: NEW test TestDualContextUnitId in internal/api/objectcontext fails on main, passes after fix.
- Add failing test for dual tokenId, sourceIp, requestId, objectId and jobId in audit answers in internal/api/audit/audit.go:137. Check: NEW test TestDualAuditFields in internal/api/audit fails on main, passes after fix.
- Add failing test in the NEW file internal/api/inventory/conflict_test.go for 400 on a request with different values in both spellings of one renamed field. Check: NEW test TestRefuseConflictingSpellings in internal/api/inventory/conflict_test.go fails on main, passes after fix with NEW error code request.field-conflict.
- Wire dual fields in Unit, DeletedUnit and ContextObject answers in internal/api/inventory/inventory.go:432 and internal/api/objectcontext/operations.go:70. Check: GET /api/dustpan/v1/namespaces/{namespace}/units with access token returns unitId equal to unitID.
- Wire dual versionId in the closed getJob answer in internal/api/jobs/serve.go:177. Check: GET job route with access token on closed job returns versionId equal to versionID.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The API description reflector defines the LegacyFieldNames allowlist. (`internal/api/apidesc/body.go:252 (func LegacyFieldNames)`)
- The inventory handler serves the unit list. (`internal/api/inventory/inventory.go:432 (func Handler.listUnits)`)
- The jobs handler serves getJob. (`internal/api/jobs/serve.go:177 (func Handler.getJob)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide** (not owner-tagged).

- Decisions that apply: 8 (current), 9 (current), 10 (current).
- Changed by: ADR 247 extends decision 3; ADR 286 changes. Chain to the end: ADR 247, ADR 286.
- Current rule: this ADR, with ADR 247, 286.
- Quote: "**Field names** are camelCase."
- Quote: "Inside a major version, a change is additive only: a new route, a new optional request field, a new answer field, a new optional query parameter or a new error code (ADR 182)."
- Quote: "After 1.0, each answer of a deprecated route or of a route of a deprecated major version holds two headers:"
- Effect: Decision 10 requires camelCase so unitId replaces unitID. Decisions 8 and 9 govern versions after 1.0 only, so before 1.0 the server adds new names beside old names and removes old names before stable. The reader move gates removal.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- What are the new field names for the ...ID and ...URL fields? ADR 210 decision 10 answers it: field names are camelCase, so unitId, versionId, sequenceId, operationId, tokenId, sourceIp, requestId, objectId, jobId, principalId, backupId, grantId, clientId, keyId, consentId, and url spellings authorizationUrl, tokenUrl, userinfoUrl, serverUrl, consentUrl, confirmUrl, reviewUrl.
- Does the dual-write and removal break the additive rule or need deprecation headers? ADR 210 decisions 8 and 9 answer it. Both govern versions after 1.0. Before 1.0 the server adds new names beside old names, marks old names deprecated in the description without Deprecation headers, moves readers, then removes old names before stable.
- How do the old names pass the reflector during migration? The pack proves LegacyFieldNames exists at internal/api/apidesc/body.go:252 and proves no caller outside apidesc in non-test code. The change keeps old names on that list during dual-write and removes each name when its field is removed.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
