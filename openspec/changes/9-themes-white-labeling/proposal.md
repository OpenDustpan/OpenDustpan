---
version: 2
---

## Why

**Persona: operator.** Operator wants the console to match the installation brand, and Developer wants a readable theme. The branding preset is decided in ADR 212, but the theme set and the white-label set are not decided. This coarse change records the wait and the refine step.

The user path this change must make work:

1. **API.** Operator reads the effective value of the branding preset in the installation settings resource.
2. **Console.** Developer opens the console sign-in page and sees the preset applied.
3. **Docs page.** Operator reads the docs page for the default look.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A change to the preset by a caller with no grant for the settings action | The server refuses the change and writes nothing. The exact code and audit entry for a preset change wait for the RFC. | `GET /api/dustpan/v1/settings` |
| A change to the preset with no fresh authentication | The server answers 403 with the error code mfa.fresh-step-required when the change reduces security, as ADR 212 decision 3 and ADR 227 decision 6 set. The exact answer for a preset change waits for the RFC. | `GET /api/dustpan/v1/settings` |

## What Changes

- Nothing yet: coarse placeholder that waits on console core views and a later RFC refinement.

Out of scope:

- Console core views (RFC 52)
- Languages and locales (RFC 11)

Depends on:

- none

Builds on (not a change):

- Console core views (RFC 52)

## Usability

Where the path still costs the person time or surprises them:

- No user path changes yet; the console keeps the default look.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has a test of the console control paths. (`pkg/dustpan/console_control_test.go:14 (func TestConsoleControlPaths)`)
- Main has the error code view.invalid for the saved view routes. (`internal/errcode/catalog_view.go:9 (view.invalid)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (owner-tagged: decision 7).

- Decisions that apply: 6 (changed), 7 (current).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365.
- Quote: "**The branding preset is an installation setting, in the public sign-in answer.**"
- Quote: "One settings API (owner decision 2026-10-18). The installation has one settings resource, and each namespace has one:"
- Effect: This coarse change adds no setting, no route and no action. It stays inside decision 6 (branding preset) and decision 7 (one settings resource) until the RFC is accepted. Decision 7 is owner-tagged and its words do not change.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What white-label controls does the RFC cover beyond the branding preset?
   - Options: Default preset only; Operator-supplied palette and logo; Full white-label with custom text.
   - Provisional decision (owner review 14 Nov): Default preset only
   - Reason: The pack proves only the roadmap wait and the preset of ADR 212 decision 6. Any logo, palette or text input later needs an allowlist, encoding, a size limit, authorization and fail-closed layers, each with a hostile test. Benchmark: ISO 27001: change control keeps a configuration unchanged until a reviewed change is approved.
   - Persona impact: Operator keeps one preset. Developer keeps the default look. Escape hatch: Ship with the default preset only until the RFC defines the set.
   - Owner-tagged ADRs involved: ADR 212 (their words do not change).
2. Where does a personal theme live?
   - Options: Console only; Server-stored per person; Installation-wide only.
   - Provisional decision (owner review 14 Nov): Server-stored per person
   - Reason: Theme and locale are the same kind of display preference. The locale change stores the preference on the server as a person preference behind the API. One stored person-preference resource must hold both theme and locale. Benchmark: AWS: the console keeps display preferences per account behind the API so a second device matches.
   - Persona impact: Developer keeps the same theme on every device. Operator keeps one rule for theme and locale. Escape hatch: Browser storage is temporary only until the server preference ships; the RFC states this in both changes.
   - Owner-tagged ADRs involved: ADR 212 (their words do not change).
3. Which authorization, fresh authentication, errors and audit entries apply to a preset change?
   - Options: Keep the current settings rules, errors and audit entries; Add theme-specific error codes.
   - Provisional decision (owner review 14 Nov): Keep the current settings rules, errors and audit entries
   - Reason: A preset change goes through the settings resource under ADR 212 decisions 3, 6 and 7, and ADR 227 decision 6. Scenarios for an unknown preset, a 403 refusal, the public sign-in read and backup coverage are not decided, and a new setting key needs its declaration and review. Benchmark: SOC 2: each configuration change is authorized and logged.
   - Persona impact: Operator sees one refusal path and one audit record for each change. Escape hatch: Refuse unknown preset values with no write and audit each preset change.
   - Owner-tagged ADRs involved: ADR 212, ADR 227 (their words do not change).

Questions considered and answered:

- Does this coarse placeholder conflict with an accepted ADR? No. ADR 212 decision 6 defines the branding preset and decision 7 defines the one settings resource. This coarse change adds no key and changes no quoted words.
- What does this change wait on? Yes. The roadmap row waits on Console core views (RFC 52). The theme set and the white-label set wait on acceptance of the RFC.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
