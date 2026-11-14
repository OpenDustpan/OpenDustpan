---
version: 2
---

Roadmap: `docs/roadmap.md:373` (Environment promotion).

## Why

**Persona: developer.** Developer moves a tested config from staging to production. Today the copy is manual, drifts, and skips review. The developer needs a visible difference and a gated promotion that writes history.

The user path this change must make work:

1. **API.** Read comparison of staging and production config through API and docs page
2. **API.** Promote selected config from staging to production through API
3. **CLI.** Run plan and apply on promoted unit through tool CLI
4. **API.** Read decision record through API when a gate refuses the promotion

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Promotion across tools | 422 promotion.incompatible-tools with message `Units use different tools` | `POST /api/dustpan/v1/namespaces/{namespace}/promotions` |
| Target unit changed during promotion | 409 unit.conflict with message `Target unit changed, read the comparison again` | `POST /api/dustpan/v1/namespaces/{namespace}/promotions` |
| Gate refuses the promotion plan | 201 with committed edit and decision record, message `Edit committed, plan refused, see decision record` | `GET /api/dustpan/v1/namespaces/{namespace}/promotions/{id}` |
| Promotion across blueprints | 422 promotion.incompatible-blueprint with message `Units come from different blueprints` | `POST /api/dustpan/v1/namespaces/{namespace}/promotions` |
| Promotion names identity field | 400 promotion.field-not-allowed with message `Field is outside effective config values` | `POST /api/dustpan/v1/namespaces/{namespace}/promotions` |
| Promotion with empty field list | 400 promotion.empty-fields with message `Promotion names no config field` | `POST /api/dustpan/v1/namespaces/{namespace}/promotions` |
| Promotion copies secret without flag | 400 promotion.secret-requires-explicit-field with message `Secret field needs explicit secret flag` | `POST /api/dustpan/v1/namespaces/{namespace}/promotions` |

Evidence for the claims above:

- A blueprint guide exists in the docs. Proof: docs/guides/publish-a-blueprint.md
- Write-back has one e2e file in the pack. Proof: e2e/unit_settings_test.go
- No comparison or promotion e2e file is listed in the pack. Proof: Blueprint e2e_count 0 and Write-back e2e list holds only e2e/unit_settings_test.go
- The pack names no ADR for this item. Proof: adrs list is empty and keywords list holds only Blueprint and Write-back

## What Changes

- New route GET /api/dustpan/v1/namespaces/{namespace}/units/compare for per-field effective config difference
- New route POST /api/dustpan/v1/namespaces/{namespace}/promotions for gated promotion copy as edit
- New route GET /api/dustpan/v1/namespaces/{namespace}/promotions/{id} for committed edit and decision record
- Comparison read of effective config values of two units from one blueprint with masked secrets
- Promotion copy of selected config fields as an edit with write-back commit and plan
- Milestone 5 gate evaluation on the promotion plan

Out of scope:

- Ephemeral units
- Artifact registry and curated upstreams
- Upgrade pull requests
- Job proxy
- Console catalog

Depends on:

- blueprints
- `blueprints` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- Caller must map field names between staging and production without help when blueprint defaults differ.
- Promotion keeps the committed edit when a gate refuses the plan, so caller must write a new edit to undo.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Self-service blueprint read test exists (`pkg/store/postgres/self_service_blueprints_internal_test.go:93 (TestSelfServiceBlueprintReadRefusesHostileRows)`)
- Unit missing error code exists (`internal/errcode/catalog_unit.go:21 (unit.not-found)`)
- Gate denial error code exists (`internal/errcode/catalog_gate.go:17 (gate.denied)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Does the owner confirm the provisional copy-as-edit with 201 on gate refusal before RFC 55 is accepted?
   - Options: Copy as a target edit with a write-back commit and a plan under milestone 5 gates; Copy as a direct settings write with no plan.
   - Provisional decision (owner review 14 Nov): Copy as a target edit with a write-back commit and a plan under milestone 5 gates
   - Reason: Copy as an edit keeps history, write-back, and gates; a direct write loses review and the record. Benchmark: AWS practice of change records with approval and rollback note
   - Persona impact: Developer keeps a visible difference and a gated history in the recommended option. Escape hatch: If the owner rejects copy-as-edit, the promotion routes stay unused and the settings edit path is unchanged.

Questions considered and answered:

- Does a comparison or promotion API already exist? No. The pack lists Blueprint e2e_count 0 and only e2e/unit_settings_test.go for write-back, so no comparison or promotion route exists and the behavior is new.
- Does promotion need new gates? No. The roadmap step says the gates of milestone 5 apply, so promotion reuses the same gate evaluation as other edits and adds no new gate model.
- Does comparison show effective values only or effective values plus source location? Effective values only. The glossary defines effective value as the value a job uses, so the comparison shows effective values with secret values masked.
- Is promotion a direct write or an edit with a plan? Copy as an edit. The roadmap step says a promotion copies the config of one unit to the other as an edit, with a write-back commit and a plan.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
