## Context

`pkg/preferences` is SDK and its checks for language, formattingLocale, digits and timeZone use `internal/locale`, which outside modules cannot import. The change makes the checks callable and replaceable.

## Decisions

1. **Move locale checks to the NEW package pkg/locale.** Move the four pure checks to the NEW file `pkg/locale/locale.go` as NEW `ValidateLanguage`, NEW `ValidateFormattingLocale`, NEW `ValidateDigits` and NEW `ValidateTimeZone` with identical sentinels and messages. `pkg/preferences/check.go` calls the NEW package `pkg/locale`. `internal/locale/format.go` keeps thin wrappers that call the NEW package `pkg/locale`, so one implementation remains. Time zone uses the embedded database only.
2. **Replaceable Validators port in pkg/preferences.** Add exported type Validators with four function fields and option WithValidators in the NEW file `pkg/preferences/validators.go`. The default Validators calls the NEW package `pkg/locale`. No environment variable and no user-named file supplies checks. The allow tables stay unexported in the NEW package `pkg/locale`.

## Structure

The NEW package `pkg/locale` holds pure check functions over unexported allow tables; `pkg/preferences` holds unexported defaultValidators plus exported Validators and the WithValidators constructor.

## Risks

- Two implementations if `internal/locale` copies logic: `internal/locale` holds only wrappers that call the NEW package `pkg/locale`.

## Diagrams

Modules that the change touches, and their imports on main:

```mermaid
flowchart LR
P1[pkg/preferences]
P2[internal/locale]
P1 --> P2
```

New imports of this change (each one allowed by the module rules):

- `pkg/preferences` imports `pkg/locale`
- `internal/locale` imports `pkg/locale`

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | Package move only; no latency, storage, limit or timeout change |  |  |

## Contracts

**Locale checks**

- Invariant: Same accept and reject for language, formattingLocale, digits and timeZone before and after the move.
- Invariant: One implementation: `internal/locale` calls the NEW package `pkg/locale`.
- `ValidateLanguage`: pre: Input is the language field value.. Post: Returns nil for inputs accepted on main and the same sentinel otherwise..
- `ValidateFormattingLocale`: pre: Input is the formattingLocale field value.. Post: Returns nil for inputs accepted on main and the same sentinel otherwise..
- `ValidateDigits`: pre: Input is the digits field value.. Post: Returns nil for inputs accepted on main and an error that names the valid values otherwise..
- `ValidateTimeZone`: pre: Input is the timeZone field value.. Post: Returns nil for time zones accepted from the embedded database and the same sentinel otherwise..
- `WithValidators`: pre: Supplied Validators holds non-nil functions for the four checks.. Post: Later patch calls in `pkg/preferences` use the supplied Validators..

## Change impact (computed from main)

Tests:

- `internal/locale/locale_test.go` names `ValidateLanguage`
- `internal/locale/locale_test.go` names `ValidateFormattingLocale`
- `internal/locale/layers_internal_test.go` names `ValidateFormattingLocale`
