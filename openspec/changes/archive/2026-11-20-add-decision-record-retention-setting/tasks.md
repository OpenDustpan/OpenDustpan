## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [x] 1.1 Write the failing table test TestRetentionSettingCheck of `RetentionCheck`: default `0`, valid values `0`, `24`, `8760`, `87600`, and the hostile values `23`, `87601`, `-1`, `+24`, `024`, `1e3`, ` 24`, `24.0`, `abc` and the empty text, each refused by `Check`. Check: `go test ./pkg/gate -run TestRetentionSettingCheck` fails on main: it calls the NEW function `RetentionCheck` (in NEW file pkg/gate/retention_setting_test.go).
- [x] 1.2 Write the failing test TestRetentionSettingReducesSecurity of `RetentionReducesSecurity`: `0` to `720`, `8760` to `720` and an unparsable value are reductions; `720` to `8760`, `720` to `0` and the same value are not. Check: `go test ./pkg/gate -run TestRetentionSettingReducesSecurity` fails on main: it calls the NEW function `RetentionReducesSecurity` (in pkg/gate/retention_setting_test.go).
- [x] 1.3 Write the failing test TestRetentionPeriod of `RetentionPeriod`: `0` gives keep-forever, `24` gives 24 hours, and a refused text gives an error. Check: `go test ./pkg/gate -run TestRetentionPeriod` fails on main: it calls the NEW function `RetentionPeriod` (in pkg/gate/retention_setting_test.go).
- [x] 1.4 Write the failing resource test TestDecisionRecordRetentionSetting: the installation settings resource lists the key with value `0` and source `default`, a patch stores a valid value with an audit entry, and a hostile value gets 400. Check: `go test ./pkg/dustpan -run TestDecisionRecordRetentionSetting` fails on main: the resource has no such key (NEW test in pkg/dustpan/settings_decision_retention_test.go).

## 2. Build

- [x] 2.1 Add the key constant, `ErrInvalidRetention`, `RetentionCheck`, `RetentionReducesSecurity` and `RetentionPeriod` in NEW file pkg/gate/retention_setting.go, with no import of the setting package. Check: `go test ./pkg/gate -run 'TestRetention' -count=1` passes.
- [x] 2.2 Add the NEW function `decisionRecordRetentionDeclaration` (built from the functions of pkg/gate, with `Check` and `ReducesSecurity` wrapping them and `ErrInvalidRetention` wrapped in `setting.ErrInvalidValue`) and add its declaration to `settingDeclarations` in pkg/dustpan/settings_resource.go. Check: `go test ./pkg/dustpan -run TestDecisionRecordRetentionSetting` passes.
- [x] 2.3 Add the term "Decision record retention" to GLOSSARY.md, with the avoid line "Audit retention (that is the retention of audit entries)". Check: `make docs-lint` passes.

## 3. Side effects (computed from main)

- [x] 3.1 Confirm that no request flow changed: the change adds a declaration to a list that an existing route serves, with no route and no caller. Check: the existing round-trip tests pass unchanged. Ticked on the condition that the CI of this pull request runs it green.

## 4. Definition of done

- [x] 4.1 Extend docs/guides/decision-record-retention.md: the key, the default `0`, the minimum 24, the PCI DSS v4.0.1 requirement 10.5.1 (at least 12 months), the GDPR storage limitation principle, a note that no job deletes records until the sweep ships and that the setting never deletes an audit entry, and the commands of a read and a patch. Check: `make docs-lint` passes and the page is listed in docs/commands-pending-pages.txt.
- [x] 4.2 Add an e2e step in the NEW file e2e/decision_record_retention_setting_test.go. It reads GET /api/dustpan/v1/settings, patches PATCH /api/dustpan/v1/settings with `If-Match`, reads it again and checks the refusal of `23`, with an access token of a site administrator and the real server binary. Check: `make e2e`.
- [x] 4.3 Run the full check. Check: `make check` reports zero findings. Ticked on the condition that the CI of this pull request runs it green.
