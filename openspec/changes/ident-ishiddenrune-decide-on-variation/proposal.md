---
version: 1
---

Issue: #2592, #2600, #2693, #2700.

## Problem

A site administrator sets names that people trust. Hidden variation selectors and Hangul fillers still pass the shared check, so two names look the same while the stored bytes differ.

## Evidence

- The shared hidden-character check exists on main as IsHiddenRune in pkg/audit. Proof: pkg/audit/audit.go:195 (func IsHiddenRune)
- Issues ask for variation selectors U+FE00-U+FE0F and U+E0100-U+E01EF, Hangul fillers U+115F, U+1160, U+3164, U+FFA0, and U+034F. Proof: #2592, #2600
- The rg for ident.IsHiddenRune finds no caller because the symbol lives in pkg/audit, so the rg proves nothing about the validators that call the shared check. Proof: rg -n -F --glob '!*_test.go' 'ident.IsHiddenRune' pkg internal cmd: 0 matches

## Fix boundary

- Add variation selectors, Hangul fillers and U+034F to the one shared check in pkg/audit/audit.go.
- Write the NEW docs page docs/reference/hidden-characters.md with the list of refused characters.

Out of scope:

- Per-field allowlists beyond the refused set
- Rename or purge of names stored before the change
- Rows of #2693 and #2700 outside the hidden-character check

## Check

- Add hostile test TestIsHiddenRuneRefusesSelectorsFillersJoiner in pkg/audit for each listed rune. Check: Test fails on main, passes after the fix. Confirmed by: CI (go test ./pkg/audit).
- Add boundary test for U+FDFF, U+FE10, U+E01F0, a Hangul syllable and a combining mark in pkg/audit. Check: Test returns false for each. Confirmed by: CI (go test ./pkg/audit).
- Change the refused set in pkg/audit/audit.go:195. Check: The tests above pass. Confirmed by: CI (go test ./pkg/audit).
- Write docs/reference/hidden-characters.md (NEW) with the refused list. Check: File lists every refused class. Confirmed by: CI (make docs-lint).
- Wiring, docs page and usability check from the docs page, then make check. Check: Confirmed by: CI (make check with zero findings).

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has IsHiddenRune as a shared hidden-rune check. (`pkg/audit/audit.go:195 (func IsHiddenRune)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 285: Local users sign in with a password and with a passkey** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current), 10 (current).
- Changed by: ADR 297 changes decisions 5, 17, 19, 20 and 21; ADR 301 extends decision 1; ADR 312 changes decision 29; ADR 320 extends decision 21; ADR 333 changes decision 16; ADR 333 extends decision 20; ADR 333 extends decision 23; ADR 372 changes decision 17. Chain to the end: ADR 297, ADR 301, ADR 312, ADR 320, ADR 333, ADR 332.
- Current rule: this ADR, with ADR 297, 301, 312, 320, 333, 372.
- Quote: "The user name (PM decision under the owner's delegation). A user name has 1 to 64 characters that match `^[a-z0-9][a-z0-9._-]{0,63}$`. The API refuses each other value at create time. It does not change a value to make i"
- Quote: "Length, not composition (PM decision under the owner's delegation). A password has at least 15 characters by default, and at most 256. The server counts Unicode code points after the NFC form, and hashes the NFC form. It"
- Effect: The wider refused set applies inside the shared check only. The user name pattern and the password length and NFC handling do not change.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Does any field kind keep variation selectors for emoji sequences?
   - Options: Refuse in every name now; add a named exception later if the owner asks; Allow variation selectors in one named field kind now.
   - Provisional decision (owner review 14 Nov): Refuse in every name now; add a named exception later if the owner asks
   - Reason: Refusal is the safe default, and a later change can add a named exception without a data change. Benchmark: Google: identifier checks refuse invisible and confusable runes and publish the list.
   - Persona impact: No persona needs emoji in names today. Escape hatch: A later change adds one named exception.
   - Owner-tagged ADRs involved: ADR 285 (their words do not change).

Questions considered and answered:

- Does any ADR already fix the refused rune list? No ADR fixes the refused rune list. ADR 285 decision 3 fixes the local user name pattern and decision 10 fixes password length and NFC handling. This change keeps both and widens only the shared hidden-character check.
- One shared check or per-field checks? One shared check, as issue #2600 asks. The per-field emoji exception stays an open owner question.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
