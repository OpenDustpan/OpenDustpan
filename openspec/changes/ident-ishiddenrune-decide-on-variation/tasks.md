## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add hostile test TestIsHiddenRuneRefusesSelectorsFillersJoiner in pkg/audit for each listed rune. Check: Test fails on main, passes after the fix. Confirmed by: CI (go test ./pkg/audit).
- [ ] 1.2 Add boundary test for U+FDFF, U+FE10, U+E01F0, a Hangul syllable and a combining mark in pkg/audit. Check: Test returns false for each. Confirmed by: CI (go test ./pkg/audit).

## 2. Fix

- [ ] 2.1 Change the refused set in pkg/audit/audit.go:195. Check: The tests above pass. Confirmed by: CI (go test ./pkg/audit).
- [ ] 2.2 Write docs/reference/hidden-characters.md (NEW) with the refused list. Check: File lists every refused class. Confirmed by: CI (make docs-lint).

## 3. Definition of done

- [ ] 3.1 Wiring, docs page and usability check from the docs page, then make check. Check: Confirmed by: CI (make check with zero findings).
