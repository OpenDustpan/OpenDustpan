## Context

A text check that builds its own control-character test from `unicode.IsControl` or a Unicode category table misses format characters (for example U+202E) and the line and paragraph separators (U+2028, U+2029). Copies of the check are common, and a wider scanner rule only finds more forms. One shared helper removes the bug class.

This change adds two functions to `internal/ident`, moves every call site to them, removes the duplicate `audit.IsHiddenRune`, removes the semgrep copy rule, and keeps the ban in the linter configuration.

## Decisions

1. **One helper with an explicit allow list.** `internal/ident` keeps `IsHiddenRune` and `HasHidden` and gains `IsHiddenRuneExcept(r rune, allowed ...rune) bool` for text that may hold line breaks or tabs. Each caller names its allowed runes. A check that also refuses spaces calls `unicode.IsSpace(r) || ident.IsHiddenRune(r)`. A sanitizer that changes or drops a character tests with the same function.
2. **One narrow exception for passwords.** A password may hold a format character (U+200D joins the parts of an emoji), so the password check calls the NEW `IsControlRune`, whose comment gives this reason. It stays as strict as before, no stricter. Every other check is stricter or the same, never looser.
3. **One duplicate removed, one scanner rule removed.** `audit.IsHiddenRune` is removed; its callers call `ident.IsHiddenRune`. The semgrep rule `.semgrep/hidden-rune-copy.yml` is removed. One `forbidigo` pattern in `.golangci.yml` refuses `unicode.IsControl` and the tables `unicode.Cc`, `unicode.Cf`, `unicode.Zl`, and `unicode.Zp` outside `internal/ident` and tests, which is the place for such a rule. The pattern applies to server code only. The client generator and its conformance suite keep their own copy because they are a separate unit that imports no server code; the exclusion uses the same globs as the depguard `clientgen` rule.

## Structure

The single definition `ident.IsHiddenRune` with `IsHiddenRuneExcept` and `IsControlRune` is the structure that makes a second hand-written check impossible. These are pure functions with no value to guard, so no unexported type applies; the `forbidigo` pattern in `.golangci.yml` is the linter setting for what visibility cannot express. No custom analyzer and no new tool.

Call sites were rewritten with a scripted search first: the `git grep` of the proposal lists every site, and the implementer runs the same search on main and records each difference.

## Contract

`internal/ident` (no new sentinel errors; each function reports by value):

- `func IsHiddenRune(r rune) bool` (existing): true for a control character, a format character, a line separator, or a paragraph separator.
- `func IsHiddenRuneExcept(r rune, allowed ...rune) bool` (NEW): false when `r` is in `allowed`, else `IsHiddenRune(r)`.
- `func IsControlRune(r rune) bool` (NEW, password only): `unicode.IsControl(r)`.
- `func HasHidden(s string) bool` (existing): true when `s` holds a rune for which `IsHiddenRune` is true.
- Removed: `audit.IsHiddenRune`; its callers call `ident.IsHiddenRune` with no other change.

## Risks

- A caller that needs a line break but calls `IsHiddenRune` instead of `IsHiddenRuneExcept` refuses valid input. The hostile rows with `\n`, `\r`, and `\t` in `internal/ident/hidden_test.go` and in each strict package catch the wrong choice.
- A new caller that copies the Unicode test instead of calling `ident` fails the linter before review.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| boundary check | Each boundary check calls `ident` and refuses at least what it refused before. | Send U+202E and U+2028 to each strict check; the check refuses them. |
| password exception | The password check calls `IsControlRune` and accepts an emoji with U+200D. | Send an emoji password; the check accepts it as before. |
| linter guard | `forbidigo` refuses direct use of `unicode.IsControl` and the category tables outside `internal/ident`, tests, and the client generator unit. | A scratch file in server code that calls `unicode.IsControl` fails `golangci-lint run`; the same call in `internal/clientgen/conformance` reports no finding. |
