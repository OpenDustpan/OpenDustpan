---
version: 1
---

Issue: #1209, #1217.

## Problem

A Developer lists stacks and workspaces and reviews history. A hidden bidi or zero-width character in an echoed reference can change display order in the console, so the server must refuse such references and audit objects at the boundary.

## Evidence

- identity.ParseObject validates unit names through validUnitName Proof: pkg/identity/identity.go:819:	if !validUnitName(name) {
- Callers pass raw references to identity.ParseObject Proof: internal/api/inventory/views.go:444:		obj, err := identity.ParseObject(ref)
- audit.IsHiddenRune is the shared hidden-character check Proof: pkg/audit/audit.go:195 (func IsHiddenRune)
- A test for hidden runes in audit Object exists on main Proof: pkg/audit/audit_test.go:462 (TestValidateRefusesHiddenRunesInTheObject)

## Fix boundary

- In pkg/identity/identity.go validUnitName rejects Cc, Cf, Zl, Zp with the audit.IsHiddenRune set and no new import
- In pkg/audit/audit.go audit.Entry.Validate rejects hidden runes in Object and ObjectID
- Table tests for each class Cc, Cf, Zl, Zp in NEW pkg/identity/identity_hidden_test.go and in pkg/audit/audit_test.go

Out of scope:

- Label value grammar and its move to pkg/inventory per the label half of #1217
- Saved view name checks beyond references
- Console display escaping beyond stored-value rejection

## Check

- Add failing table rows for Cc, Cf, Zl, Zp in unit references in NEW pkg/identity/identity_hidden_test.go Check: NEW pkg/identity/identity_hidden_test.go TestParseObjectRejectsHiddenRunes fails on main for U+202E, U+2066, U+2028, U+2029, U+200B, newline, U+0007
- Add failing rows for Object and ObjectID hidden runes to pkg/audit/audit_test.go Check: pkg/audit/audit_test.go TestValidateRefusesHiddenRunesInObjectID fails on main for U+202E and U+2028
- Confirm the single ParseObject gate covers inventory, saved view, and store lookup paths Check: `rg -n 'validUnitName' pkg/identity` shows the single gate that each ParseObject path uses
- Run e2e step against GET /api/dustpan/v1/namespaces/{namespace}/units with access token for hidden-rune reference Check: GET /api/dustpan/v1/namespaces/{namespace}/units with access token refuses hidden-rune reference as invalid
- Update docs page for unit references and audit objects Check: Docs page for unit references states the rejected Cc, Cf, Zl, Zp set
- Run usability check of refused reference path Check: Developer completes the user path from the docs page alone with no raw hidden text in errors
- Run full check with zero findings Check: `make check` reports zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- audit.IsHiddenRune reports hidden runes (`pkg/audit/audit.go:195 (func IsHiddenRune)`)
- audit.Entry.Validate validates entries (`pkg/audit/audit.go:267 (func Entry.Validate)`)
- identity.ParseObject parses object references (`pkg/identity/identity.go:843 (func ParseObject)`)
- validUnitName checks unit names (`pkg/identity/identity.go:945 (func validUnitName)`)
- refusal.Clean cleans denied audit text (`internal/api/refusal/clean.go:16 (func Clean)`)
- A test for hidden runes in audit Object exists (`pkg/audit/audit_test.go:462 (TestValidateRefusesHiddenRunesInTheObject)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Which hidden-character set applies to unit references and audit objects? No ADR touches hidden characters. The issue text decides the set: Cc, Cf, Zl, Zp from audit.IsHiddenRune on main.
- Does one check in validUnitName cover each unit reference path? Yes. Callers in inventory views, collections, filters, saved views, and store lookups call identity.ParseObject, so one check in validUnitName covers each reference path.
- Which half of #1217 does this change close? This change closes only the reference half of #1217: the shared Cc, Cf, Zl, Zp set and its use in references. Label values stay out of scope until the label grammar moves to pkg/inventory.
- Do existing stored rows hold hidden runes? Per the issue trace stored names come from tool adapters that validate names, so no stored row is expected to hold hidden runes. The change rejects at parse time only and adds no read-path repair or shim.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
