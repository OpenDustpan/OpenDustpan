---
version: 1
---

Issue: #1263.

## Problem

A developer sets a timeZone preference once and expects the same value to work on each host. Today a host zone file can accept a name that another host refuses, so a saved preference is not portable.

## Evidence

- The timeZone check lives in pkg/preferences and ends in a host-dependent load call. Proof: pkg/preferences/check.go:188:	_, err := time.LoadLocation(value)
- The shape helper and the refused host-only names live in the same file. Proof: pkg/preferences/check.go:224:	case "local", "localtime", "posixrules", "posix", "right":
- Preferences are read and patched on the me route. Proof: ANY/GET/PATCH /api/dustpan/v1/me/preferences

## Fix boundary

- Check timeZone only against the embedded time zone database, never host zone files or ZONEINFO.
- Add same-answer tests with and without host zone data and a two-direction list freshness test.
- State the embedded database as the source in the user docs; the ADR 181 note waits on the owner.

Out of scope:

- New timeZone values outside the embedded database.
- Console picker widget changes.

## Check

- Add failing tests in pkg/preferences/patch_test.go for host-only names, refused local names, bad shape and case, UTC, repeat of the stored value, and the same answer with ZONEINFO set and unset. Check: The NEW test TestPreferencesTimeZoneUsesEmbeddedDatabaseOnly in pkg/preferences/patch_test.go fails on main and passes after the fix.
- Replace the host-dependent load layer in pkg/preferences/check.go with a lookup in the generated embedded zone list. Check: The NEW test TestEmbeddedZoneListMatchesTzdata passes in both directions and `rg -n LoadLocation pkg/preferences --glob '!*_test.go'` finds no match.
- Wire the generator with a go generate directive in pkg/preferences, a NEW make target that runs internal/zonegen, and a CI freshness check. Check: The NEW make target rebuilds the list from internal/zonegen and the build fails when the list is stale.
- Verify the existing PATCH /api/dustpan/v1/me/preferences path with an access token uses the new check; no openapi change. Check: PATCH /api/dustpan/v1/me/preferences with an access token stores an embedded zone and refuses a host-only zone with 422 preferences.field-invalid.
- Update the preferences docs page in the same pull request. Check: The docs page for timeZone names the embedded database as the source.
- Run the usability check from the docs page and make check. Check: `make check` reports zero findings.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main checks timeZone in pkg/preferences before storage. (`pkg/preferences/check.go:155 (func timeZone)`)
- Main checks the shape of an IANA name with zoneShape. (`pkg/preferences/check.go:199 (func zoneShape)`)
- Main has a route test that preferences need the right caller. (`internal/api/server/preferences_test.go:443 (func TestPreferencesNeedTheRightCaller)`)
- Main refuses a bad preferences field with preferences.field-invalid. (`internal/errcode/catalog_preferences.go:11 (error code preferences.field-invalid)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 181: Console languages, formats and translation** (not owner-tagged).

- Decisions that apply: the ADR as a whole.
- Changed by: ADR 210 changes; ADR 231 changes decision 1; ADR 231 extends decisions 16 and 26; ADR 232 changes decisions 18 and 19; ADR 232 extends decisions 12, 20 and 21; ADR 233 extends decisions 7, 10, 11, 13 and 17; ADR 234 changes; ADR 234 extends decision 8; ADR 286 changes. Chain to the end: ADR 210, ADR 231, ADR 232, ADR 233, ADR 234, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 210, 231, 232, 233, 234, 286.
- Quote: "Six new fields in the preferences document. This ADR adds these fields to the document of ADR 180:"
- Effect: No quoted ADR 181 line states the timeZone database. The change narrows only the check in pkg/preferences to the embedded database and changes no ADR 181 words. The ADR note waits on the owner in open questions.
- Conflict: none.

## Open questions

1. Does the owner accept an ADR 181 note that the timeZone check reads only the embedded database? Decides: owner.
   - Options: Add a note to ADR 181 that the timeZone check reads only the embedded database; Leave ADR 181 unchanged and document the source only in user docs.
   - Recommended: Add a note to ADR 181 that the timeZone check reads only the embedded database Reason: The issue asks to say in ADR 181 which database the check reads, and only the owner can accept ADR words.

Not spec-ready: question 1 needs a change to the words of an owner-tagged decision.

Questions considered and answered:

- Does the fix change the preferences document or only the timeZone check? Only the check changes (ADR 181 decision 8). The pack shows the check in pkg/preferences/check.go func timeZone with helper zoneShape. The document shape and defaults do not change.
- How to ask the embedded database only without reading host zone files? A generated allowlist of embedded zone names is checked before any load, so the check path never reads ZONEINFO or host files (ADR 181 decision 8). The refused-name block and zoneShape stay as first layers.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
