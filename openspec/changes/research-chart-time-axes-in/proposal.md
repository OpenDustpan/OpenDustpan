---
version: 1
---

Issue: #992.

## Problem

A Developer reads charts in Arabic and Hebrew, and the direction of chart time axes in right-to-left languages is undecided and needs precedent before any chart change.

## Evidence

- Issue 992 asks for a research note that states time-axis direction in Arabic and Hebrew with precedent. Proof: A research note that says which direction a time axis runs in Arabic and Hebrew, with precedent.
- Issue 992 asks for a recommendation and an ADR 231 amendment if needed. Proof: A research note with screenshots or sources and a recommendation; an ADR 231 amendment if needed.
- ADR 231 includes Arabic and Hebrew in the language set. Proof: The other languages are `af`, `ar`, `he`, `ja`, `ko`, `pt-BR`, `ru`, `uk` and `zh-Hans`.

## Fix boundary

- Add a research note on chart time-axis direction in Arabic and Hebrew with precedent and one recommendation.

Out of scope:

- Chart implementation in the console
- Amendment to ADR 231, done by a follow-up change named in the note
- Translation of docs or messages

## Check

- Collect precedent for Material, Fluent, Grafana, and Arabic and Hebrew financial sites and record it in NEW docs/research/chart-time-axes-rtl.md with links first, at least 5 sources, and no stored third-party screenshot unless its license allows it. Check: NEW docs/research/chart-time-axes-rtl.md exists with precedent table for Material, Fluent, Grafana, one Arabic and one Hebrew financial site, each with source link, access date, product version, axis direction, digit form, and mixed-content behavior.
- Record user-expectation evidence in NEW docs/research/chart-time-axes-rtl.md separate from product precedent. Check: NEW docs/research/chart-time-axes-rtl.md cites a study, survey, or native-speaker review for whether users expect time to run right to left or left to right.
- Write the recommendation, disagreement rule, mixed-content rule, and amendment statement in NEW docs/research/chart-time-axes-rtl.md. Check: NEW docs/research/chart-time-axes-rtl.md states one recommendation with the disagreement rule, mixed-content rule, and which ADR holds the future rule plus the follow-up change name; `make docs-lint` passes for the new file.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has a language-tag test in preferences, which is the closest proven language handling near this research. (`pkg/preferences/zone_test.go:198 (func TestLanguageTags)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 231: Afrikaans** (owner-tagged: decision 1).

- Decisions that apply: 1 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The other languages are `af`, `ar`, `he`, `ja`, `ko`, `pt-BR`, `ru`, `uk` and `zh-Hans`."
- Effect: The research covers Arabic and Hebrew because the language set includes them, and it makes no language change; ADR 231 holds no time-axis rule so the note states which ADR holds the future rule and this change amends no ADR.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Should this change decide the direction of chart time axes? RFC 78 in the pack states the time axis of charts in right-to-left languages waits for research, so this change writes only the research note and decides no axis direction; an RFC states an open question and is not quoted as a decision.
- Which languages does the research cover? ADR 231 decision 1 keeps Arabic and Hebrew in the language set, so the research covers those two right-to-left languages.
- Which ADR text can this draft quote as proof? The pack proves only ADR 231 decision text; it does not prove ADR 181 text, so this draft quotes only ADR 231.
- Who accepts the recommendation and the follow-up change? The owner accepts the recommendation through the follow-up change named in the note; this research change only records the recommendation and the follow-up name.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
