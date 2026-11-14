## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Fix

- [ ] 1.1 Add failing table rows for Cc, Cf, Zl, Zp in unit references in NEW pkg/identity/identity_hidden_test.go Check: NEW pkg/identity/identity_hidden_test.go TestParseObjectRejectsHiddenRunes fails on main for U+202E, U+2066, U+2028, U+2029, U+200B, newline, U+0007
- [ ] 1.2 Add failing rows for Object and ObjectID hidden runes to pkg/audit/audit_test.go Check: pkg/audit/audit_test.go TestValidateRefusesHiddenRunesInObjectID fails on main for U+202E and U+2028
- [ ] 1.3 Confirm the single ParseObject gate covers inventory, saved view, and store lookup paths Check: `rg -n 'validUnitName' pkg/identity` shows the single gate that each ParseObject path uses

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Run e2e step against GET /api/dustpan/v1/namespaces/{namespace}/units with access token for hidden-rune reference Check: GET /api/dustpan/v1/namespaces/{namespace}/units with access token refuses hidden-rune reference as invalid
- [ ] 3.2 Update docs page for unit references and audit objects Check: Docs page for unit references states the rejected Cc, Cf, Zl, Zp set
- [ ] 3.3 Run usability check of refused reference path Check: Developer completes the user path from the docs page alone with no raw hidden text in errors
- [ ] 3.4 Run full check with zero findings Check: `make check` reports zero findings
