## 0. Confirm against the code first

- [ ] 0.1 Run the `git grep` of the proposal on main after #2584 merges, and record each difference from the list in the pull request body. Check: the pull request body lists the differences, or says that there are none.

## 1. Tests first

- [ ] 1.1 Add `TestIsHiddenRuneExcept` and `TestIsControlRune` (NEW) in `internal/ident/hidden_test.go`: a table of runes (NUL, `\n`, `\r`, `\t`, U+202E, U+2028, U+2029, U+200D, `a`) with the allowed lists `{}`, `{'\n', '\t'}` and `{'\n', '\r', '\t'}`. No route: the test calls the functions only. Check: `go test ./internal/ident/ -run 'TestIsHiddenRuneExcept|TestIsControlRune' -count=1` fails on main.
- [ ] 1.2 For each package whose check gets stricter, add one hostile row with U+202E and one with U+2028 to the existing test of that check, in the test file of the package, for example `pkg/objectcontext/objectcontext_test.go` for `pkg/objectcontext/objectcontext.go:477`. Check: `go test ./internal/... ./pkg/... -run 'Hidden|Control|Invalid' -count=1` passes after task 2.2.

## 2. Build

- [ ] 2.1 Add `IsHiddenRuneExcept` and `IsControlRune` (NEW) to `internal/ident/hidden.go`, with comments: the first is `IsHiddenRune` without the allowed runes; the second is for a password only, and says why. Check: `go test ./internal/ident/ -count=1` passes.
- [ ] 2.2 Replace each hand-written check of the proposal list with the `ident` function of its group, and remove `audit.IsHiddenRune` from `pkg/audit/audit.go` with its callers moved to `ident.IsHiddenRune` (`pkg/forgelink/forgelink.go:161`). Check: `rg -n 'unicode\.(IsControl|Cc|Cf|Zl|Zp)\b' --type go -g '!*_test.go' -g '!internal/ident/**'` finds no line.
- [ ] 2.3 Remove `.semgrep/hidden-rune-copy.yml`, and add one `forbidigo` pattern to `.golangci.yml` for `unicode.IsControl` and the tables `unicode.Cc`, `unicode.Cf`, `unicode.Zl` and `unicode.Zp`, with the message "Call ident.IsHiddenRune, ident.HasHidden or ident.IsHiddenRuneExcept", and an exclusion for `internal/ident/` and `_test.go`. Check: `golangci-lint run ./...` reports nothing, and `make semgrep` reports nothing.

## 3. Definition of done

- [ ] 3.1 Run the checks. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
