---
version: 1
---

Issue: #2038.

## Problem

The problem is that teams lack shared health signals such as apply failure rate, time to restore, plan time, approval wait, toil signals and fleet hygiene. Collection without accepted opt-in and privacy rules risks trust.

## Evidence

- Issue #2038 is unscheduled later work and the next step is a full RFC 46. Proof: Issue #2038 body: This work is not scheduled, because Milestone 7 comes first. The next step is a full RFC 46 with the research as its evidence.
- RFC 46 is a draft stub with no accepted outcome. Proof: RFC 46 excerpt in pack: Status: draft. Outcome: None yet.
- The issue requires everything opt-in, off by default, with a stored visible setting. Proof: Issue #2038 body: Everything here is opt-in: off by default, and turned on with a stored, visible setting.
- The issue requires team-level only with minimum group 5 and no per-person ranking or error text. Proof: Issue #2038 body: Insights are team-level only, with a minimum group of 5 people. There is no filter by person, no ranking, and no error or log text.
- The issue requires each number to ship with a published definition and links to the records behind it. Proof: Issue #2038 body: Each number ships with a published definition and links to the records behind it.
- No ADR is listed for this item. Proof: The context pack field adrs is empty, so no decision lines exist to quote.

## Fix boundary

- Marks engineering-health insights as later work, not scheduled before Milestone 7.
- Records the boundary: team-level only, minimum group 5, no per-person views, all opt-in off by default.
- Names RFC 46 as the next step with the research as evidence.

Out of scope:

- Metric definitions and formulas
- New lock history, drift transition, commit flag and survey records
- Insight API, console views and docs pages
- Milestone 7 work
- Published definition and record links for each number, deferred to RFC 46

## Check

- Refine docs/rfc/0046-insights.md when RFC 46 is accepted: metric definitions, opt-in levels, privacy floor, retention and record shapes. Check: `gh issue view 2038 --json state` plus `rg -n accepted docs/rfc/0046-insights.md`

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main saves an approval-wait value in the audit test chain helper (`pkg/audit/audittest/chain.go:1023 (func saveApprovalWait)`)
- Main checks the approval-wait chain in the postgres audit adapter (`pkg/store/postgres/audit_chain.go:96 (func checkChainApprovalWait)`)
- Main checks error text in the profile provider test helper (`pkg/profile/provider/providertest/providertest.go:282 (func checkErrorText)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What privacy floor applies to every insight?
   - Options: Team-level only with minimum group 5, no per-person filter, no ranking, no error or log text; Allow per-person views with consent; Allow ranking of teams.
   - Provisional decision (owner review 14 Nov): Team-level only with minimum group 5, no per-person filter, no ranking, no error or log text.
   - Reason: The issue states this floor. No accepted RFC confirms how it is enforced and audited. Benchmark: ISO 27001: define purpose, retention and access control for workforce analytics before collection, with aggregated reporting.
   - Persona impact: No persona is upset. Persons stay unidentifiable. Escape hatch: No collection exists until RFC 46 is accepted.
2. What opt-in levels control insights?
   - Options: Three levels: installation, namespace, each person for the pulse survey; One installation switch only; One namespace switch only.
   - Provisional decision (owner review 14 Nov): Three levels: installation, namespace, each person for the pulse survey.
   - Reason: The issue names the levels. The setting keys and precedence are not defined. Benchmark: AWS: features off by default with an explicit stored setting per scope and a visible effective value.
   - Persona impact: No persona is upset. Each scope controls its own data. Escape hatch: Each level can stay off while the others turn on.
3. What retention limits apply to lock history, drift transitions, commit flags and survey answers?
   - Options: Bounded retention for each new record class with published limits; Unbounded retention; No new records, views over existing data only.
   - Provisional decision (owner review 14 Nov): Bounded retention for each new record class with published limits.
   - Reason: The issue asks for a retention limit on lock history but gives no numbers. Benchmark: SOC 2: keep derived telemetry only as long as the stated purpose needs, with a maximum for each record class.
   - Persona impact: No persona is upset. Old records leave on schedule. Escape hatch: Retention stays at the shortest proposed limit until RFC 46 sets each class.
4. What happens when a team drops below the minimum group of 5?
   - Options: Suppress the group with no merge and no drill-down; Merge small groups into a larger aggregate; Return an error for small groups.
   - Provisional decision (owner review 14 Nov): Suppress the group with no merge and no drill-down.
   - Reason: The issue sets the minimum but does not define suppression, membership counting or differencing over time. Benchmark: ISO 27001: suppress small-group aggregates and limit differencing over time.
   - Persona impact: No persona is upset. A small team sees no insight instead of a re-identifying one. Escape hatch: Small groups stay suppressed until RFC 46 defines merging.
5. How are pulse survey answers kept apart from person identity?
   - Options: Store answers in the installation only, separated from person identity; Store answers with person identity.
   - Provisional decision (owner review 14 Nov): Store answers in the installation only, separated from person identity.
   - Reason: The issue says the survey is stored only in the installation. It does not define the separation from identity. Benchmark: SOC 2: store survey answers apart from identity with a stated separation control.
   - Persona impact: No persona is upset. Persons answer without attribution. Escape hatch: No survey runs until the separation is defined.
6. What capability and requirements will RFC 46 define before any build starts?
   - Options: Keep specs empty until RFC 46 accepts definitions; Add a placeholder capability now.
   - Provisional decision (owner review 14 Nov): Keep specs empty until RFC 46 accepts definitions.
   - Reason: This short-form change ships no behavior, so no delta spec ships yet. Benchmark: SOC 2: keep a change with no behavior free of capability specs until its RFC accepts metric definitions.
   - Persona impact: No persona is upset. No partial view ships. Escape hatch: No insight capability ships until RFC 46 is accepted.

Questions considered and answered:

- Must this change define insight metrics now? No. Issue #2038 says this work is not scheduled because Milestone 7 comes first and the next step is a full RFC 46.
- Does any ADR already decide team-level analytics? No. The pack field adrs is empty and RFC 46 status is draft with outcome None yet, so there is no accepted decision to quote.
- Does main already hold part of the item? No. Main has settings routes and audit and drift-check tests, but no insight view, no lock history record, no survey, and no metric definitions. This change ships no behavior.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
