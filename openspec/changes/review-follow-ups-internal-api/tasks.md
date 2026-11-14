## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write NEW test `TestNotificationTestLimiter` in the NEW file internal/api/notifications/limit_test.go with maintenance, schema-minimum and plain ErrLimitUnavailable cases Check: `go test ./internal/api/notifications/ -run TestNotificationTestLimiter` fails on main
- [ ] 1.2 Write NEW test `TestPlainHeaderStripped` in the NEW file internal/headerauth/strip_test.go for success, refusal, duplicate values and no header Check: `go test ./internal/headerauth/ -run TestPlainHeaderStripped` fails on main
- [ ] 1.3 Write NEW test `TestBackupManifestByteAndKeyBounds` in pkg/backup/manifest_format_test.go with oversized, exactly 10 MB, lying-length and many-key cases Check: `go test ./pkg/backup/ -run TestBackupManifestByteAndKeyBounds` fails on main

## 2. Fixes

- [ ] 2.1 Fix the limiter in internal/api/notifications/notifications.go Check: `TestNotificationTestLimiter` passes
- [ ] 2.2 Strip the secret header in internal/headerauth/headerauth.go Check: `TestPlainHeaderStripped` passes
- [ ] 2.3 Bound manifest bytes and keys in pkg/backup/service.go Check: `TestBackupManifestByteAndKeyBounds` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run the affected suites Check: `go test -race ./internal/api/notifications/... ./internal/headerauth/... ./pkg/backup/...` passes
- [ ] 4.2 Run the full gate with no new routes. Confirmed by: CI (`make check`). Check: `make check` with zero findings
