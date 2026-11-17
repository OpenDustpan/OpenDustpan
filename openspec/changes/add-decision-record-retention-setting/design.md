## Context

The settings resource lists installation settings from declarations (`pkg/dustpan/settings_resource.go`). A declaration holds the key, the kind, the default, a `Check` and a `ReducesSecurity` function. The setting package writes the audit entry and asks for a fresh authentication.

## Decisions

1. **One key, one unit.** The key is `decisionRecords.retentionHours`. The value is a whole number of hours as text, kind int. `0` keeps forever and is the default. A value from 24 to 87600 is a period. No suffix and no duration text, so the sweep reads one number.
2. **Canonical form only.** `Check` rejects an empty value, a sign, a space, a leading zero, a fraction and an exponent. Reject, not repair.
3. **Shorter is a reduction.** `ReducesSecurity(old, next)` is true when `next` is not `0` and (`old` is `0` or `next` < `old`). A value that does not parse counts as a reduction (fail closed).
4. **Decision records only.** The declaration `Doc` text says "decision records". The setting is not read by any code in this change. The sweep reads it in the follow-up.

## Structure

NEW file pkg/gate/retention_setting.go holds the key constant, `RetentionSettingDeclarations()` and a function `RetentionPeriod(value string) (time.Duration, bool, error)` that parses the stored value once. The sweep of the follow-up calls this function, so no other code parses the text. The function is the one place that knows `0` means keep forever.

## Contract

- Key: `decisionRecords.retentionHours`, level installation, kind int, default `0`, database layer, apply live.
- `Check(value string) error`: nil for `0` and for a canonical whole number from 24 to 87600; otherwise an error that wraps `setting.ErrInvalidValue` and names the key, the value and the range. The settings route answers 400.
- `ReducesSecurity(old, next string) bool`: true when `next` is not `0` and (`old` is `0` or `next` is less than `old`), and true when either value does not parse.
- `RetentionPeriod(value string) (period time.Duration, keepForever bool, err error)` in pkg/gate: `0` gives `keepForever` true; a valid number gives `period` in hours; a refused text gives an error that wraps `setting.ErrInvalidValue`.
- Routes: GET and PATCH /api/dustpan/v1/settings, unchanged. Item shape: the existing item of the settings resource.

## Risks

- A setting that deletes nothing may look broken. The docs page and the `Doc` text say that no job deletes records until the follow-up ships.
- A restore from a backup of another version may hold a value that `Check` refuses. The resource then shows the default, which keeps forever.

## Quality attributes

- Predictable: one number, one unit, one canonical form.
- Safe default: keep forever. Nothing is deleted by default.
- Visible: the effective value and the source are in the API.
