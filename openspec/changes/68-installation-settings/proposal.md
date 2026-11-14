---
version: 2
---

## Why

**Persona: operator.** An operator sets up one installation and needs one clear place for installation settings. Main already serves one settings resource for the installation and one for each namespace. The names, defaults, layers and sensitive marks of installation settings stay open until the shape of the installation API is fixed.

The user path this change must make work:

1. **Docs page.** The operator reads the page about installation settings.
2. **API.** The operator lists each installation setting with its effective value and its source.
3. **API.** The site administrator changes an installation setting with a PATCH that has a fresh authentication where a weaker value needs it.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A PATCH holds an unknown installation setting key. | The server answers 400 with the error code settings.key-unknown and changes nothing. | `GET /api/dustpan/v1/settings` |
| A PATCH holds an invalid value. | The server answers 400 with the error code settings.value-invalid and changes nothing. | `GET /api/dustpan/v1/settings` |
| A PATCH that makes the installation less secure has no fresh authentication. | The server answers 403 with the error code mfa.fresh-step-required and changes nothing. | `GET /api/dustpan/v1/settings` |

Evidence for the claims above:

- Main already serves one installation settings resource and one namespace settings resource. Proof: The routes ANY/GET/PATCH /api/dustpan/v1/settings and ANY/GET/PATCH /api/dustpan/v1/namespaces/{}/settings in the route table of the pack.
- Main already has error codes for unknown keys and invalid values. Proof: settings.key-unknown and settings.value-invalid in internal/errcode/catalog_settings.go:7

## What Changes

- Records RFC 68 as a coarse change that waits on the shape of the installation API.
- Fixes the rule for the direction of a weaker setting, so that the refinement can test it.
- Keeps the existing settings resources unchanged.

Out of scope:

- The names, defaults and layers of each installation setting
- Namespace placement and routing, which wait on this item
- Console views for installation settings

Depends on:

- none

Builds on (not a change):

- RFC 59 shape of the installation API

## Usability

Where the path still costs the person time or surprises them:

- No user path changes in this coarse change.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main builds the default installation settings of the agent through a constructor. (`internal/agent/settings.go:52 (func DefaultInstallationSettings)`)
- Main has a test that sign-in settings are for the site only. (`pkg/access/security_test.go:631 (func TestSignInSettingsAreSiteOnly)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (owner-tagged: decision 3, 7).

- Decisions that apply: 3 (changed), 7 (current).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365.
- Quote: "One settings API (owner decision 2026-10-18). The installation has one settings resource, and each namespace has one:"
- Quote: "Fresh MFA for a change that reduces security, and for approvals.  **Settings.** A change of an installation setting or of a namespace setting through the API needs a fresh MFA step when it makes the installation or the namespace less secure."
- Effect: The change keeps one settings resource for each scope and the fresh step for a weaker setting. It adds no setting and changes no words.
- Conflict: none.

**ADR 227: One fresh-authentication window** (owner-tagged: decision 2, 3).

- Decisions that apply: 2 (current), 3 (current).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends. Chain to the end: ADR 301, ADR 320, ADR 339, ADR 354, ADR 332.
- Current rule: this ADR, with ADR 301, 320, 339, 354.
- Quote: "One window everywhere (owner). The installation has one setting, `freshAuthSeconds`, with the default 900 (15 minutes)."
- Quote: "0 asks every time (owner, 2026-10-18). A window of 0 asks for a fresh authentication at each step."
- Effect: The change keeps freshAuthSeconds as the one window. A window of 0 is the strictest value, and a longer window is the weaker direction.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which installation settings ship first once the shape of the installation API is fixed?
   - Options: Wait for the installation API shape, then fix names, types, defaults, layers and sensitive marks; Fix setting names now.
   - Provisional decision (owner review 14 Nov): Wait for the installation API shape, then fix names, types, defaults, layers and sensitive marks
   - Reason: Waiting avoids rework, and ADR 212 already fixes the resource shape. Benchmark: AWS Systems Manager Parameter Store: one typed store with a masked read for secrets.
   - Persona impact: No persona is upset, because no user path changes now. Escape hatch: The existing settings resource stays in use meanwhile.
   - Owner-tagged ADRs involved: ADR 212 (their words do not change).
2. Is a PATCH with a bad key or value atomic?
   - Options: The whole PATCH is atomic and changes nothing on any bad key; Valid keys apply and bad keys fail one by one.
   - Provisional decision (owner review 14 Nov): The whole PATCH is atomic and changes nothing on any bad key
   - Reason: Atomic is simpler to explain, audit and script. Benchmark: PCI DSS: a change set is applied whole or not at all.
   - Persona impact: The operator corrects the keys and sends the PATCH again. Escape hatch: One PATCH for each key gives per-key results.
   - Owner-tagged ADRs involved: ADR 212 (their words do not change).

Questions considered and answered:

- Does RFC 68 change the shape of the settings API? No. ADR 212 decision 7 decides one settings resource for each scope with the effective value and its source.
- Which direction of a change is weaker for freshAuthSeconds? ADR 227 decision 3: a change to 0 needs no fresh step, and a change to a longer window needs one.
- Which installation settings already exist? ADR 227 decision 2 owns freshAuthSeconds. The refinement lists the others and does not redefine them.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
