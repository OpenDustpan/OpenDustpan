---
version: 2
---

## Why

**Persona: developer.** A developer works in their own language and locale. The console needs readable text, dates, and numbers before translation can help.

The user path this change must make work:

1. **Docs page.** Developer reads the docs page for supported languages and fallback.
2. **Console.** Developer opens the console and reads stacks and workspaces with dates and numbers in the active locale.

## What Changes

- Record the language and locale scope for RFC 11 once console core views exist.

Out of scope:

- Console core views
- Themes and white-labeling

Depends on:

- none

Builds on (not a change):

- Console core views (RFC 52)

## Usability

Where the path still costs the person time or surprises them:

- No usability check yet; the console views that translation covers do not exist.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has language-tag checks in preferences tests covering accepted tags. (`pkg/preferences/zone_test.go:198 (TestLanguageTags)`)
- Main has formatting locale layer tests covering layer order. (`internal/locale/layers_internal_test.go:13 (TestFormattingLocaleLayers)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (owner-tagged: decision 7).

- Decisions that apply: 6 (changed), 7 (current).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365.
- Quote: "One settings API (owner decision 2026-10-18). The installation has one settings resource, and each namespace has one:"
- Quote: "**The branding preset is an installation setting, in the public sign-in answer.**"
- Effect: If locale becomes a setting, it lives on the settings resource with effective value and source. This change adds no setting now.
- Conflict: none.

**ADR 41: The server reads state and masks secrets** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 60 changes decisions 5 and 13; ADR 103 changes decision 13; ADR 146 extends decision 13; ADR 151 changes decision 10; ADR 238 changes decision 12; ADR 240 changes decisions 9 and 13; ADR 240 extends decision 5; ADR 352 extends decision 10. Chain to the end: ADR 60, ADR 103, ADR 146, ADR 151, ADR 238, ADR 240, ADR 352, ADR 63, ADR 64, ADR 65, ADR 68, ADR 72, ADR 126, ADR 210, ADR 334, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 149, ADR 262, ADR 297, ADR 154, ADR 217, ADR 220, ADR 315, ADR 356, ADR 354, ADR 124, ADR 216.
- Current rule: this ADR, with ADR 60, 103, 146, 151, 238, 240, 352.
- Quote: "A read masks secret values. A state read answers the structure and the values of a state. It replaces each secret value with a marker."
- Effect: Masked reads keep markers and value paths independent of locale. Translation must not alter markers or secret values.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which locales ship first and who translates product copy?
   - Options: English only first, then one locale per release; English plus a fixed set of locales at once.
   - Provisional decision (owner review 14 Nov): English only first, then one locale per release.
   - Reason: Core views must exist before translation has value. Benchmark: AWS: the console adds one language at a time.
   - Persona impact: A Developer who reads another language waits longer, but the console stays exact and scriptable. Escape hatch: Add one locale at a time behind the same console views.
   - Owner-tagged ADRs involved: ADR 212 (their words do not change).
2. Where does the locale preference live, which API and authorization action changes it, and which locale does the unauthenticated sign-in page use?
   - Options: Stored person preference with browser header as display hint; Browser header only; Installation setting.
   - Provisional decision (owner review 14 Nov): Stored person preference with browser header as display hint only.
   - Reason: A stored preference is explicit and scriptable. The sign-in page uses the header hint because no person is known. Benchmark: AWS: the console language is a stored account setting with a browser default.
   - Persona impact: No persona is upset; a person can set it through the API. Escape hatch: The browser hint applies when no preference is stored.
   - Owner-tagged ADRs involved: ADR 212 (their words do not change).
3. Do API errors, audit entries, and CLI output stay in English when the console translates?
   - Options: All user paths stay in English; Display text translates, errors and audit stay in English.
   - Provisional decision (owner review 14 Nov): Display text translates, errors and audit entries stay in English.
   - Reason: Stable machine-readable output needs one language for codes and logs. Benchmark: ISO 27001: audit records stay consistent and unambiguous.
   - Persona impact: Scripts and audits stay stable. Escape hatch: Machine-readable fields and codes never change.
4. What is the fallback chain for a hostile tag, a missing key, a partial translation, an unsupported stored preference, or a removed locale?
   - Options: fr-CA falls back to fr then en; Request fails on unsupported locale.
   - Provisional decision (owner review 14 Nov): Reject hostile tags at the boundary, then fall back fr-CA to fr to en, with missing keys in English.
   - Reason: The page stays usable and the answer is predictable. Benchmark: AWS: locale fallback goes through the parent locale to English.
   - Persona impact: No persona is upset; the page stays usable. Escape hatch: Never fail the request on locale.

Questions considered and answered:

- Does any accepted ADR already decide the language or locale set? No ADR in the pack decides a language or locale set. ADR 212 decides where a setting lives if one is added. ADR 41 decides masking stays locale independent.
- Would a locale preference need the settings resource? ADR 212 decision 7 says each declared setting answers with effective value and source. No locale setting is declared now.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
