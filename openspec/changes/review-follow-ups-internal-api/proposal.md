---
version: 1
---

Issue: #2655, #2660, #2672.

## Problem

Operator runs notification tests and restores installations, and site administrators sign in with header auth. Unbounded warnings flood logs, lost maintenance codes give the wrong 503, a lingering secret header exposes the credential to downstream handlers, and unbounded manifest reads exhaust memory during restore.

## Evidence

- The notification test limiter work lives in internal/api/notifications/notifications.go. Proof: Path internal/api/notifications/notifications.go exists per pack.
- The plain-mode secret header work lives in internal/headerauth/headerauth.go. Proof: Path internal/headerauth/headerauth.go exists per pack.
- The manifest bound work lives in pkg/backup/service.go. Proof: Path pkg/backup/service.go exists per pack.
- Main defines the limit-unavailable sentinel used by the sink limiter. Proof: pkg/notify/sinks/limit.go:37 defines ErrLimitUnavailable per pack main list.
- No ADR in the pack decides these three follow-ups. Proof: Pack field adrs is an empty list, and keywords, gaps and rfcs are empty.

## Fix boundary

- Log rate-limited warnings on repeated notification test counter failures without logging database error text and without sending messages or audit entries
- Preserve maintenance and schema-minimum 503 codes when the notification test limiter wraps those sentinels with ErrLimitUnavailable
- Remove the plain-mode secret header from trusted requests after authentication consumes it on success and on refusal
- Bound unverified blob-key manifest growth to 100000 keys and limit untrusted manifest bytes to 10 MB before full read without weakening restore completeness checks

Out of scope:

- Other review follow-ups outside #2655, #2660 and #2672
- Changes to Pulumi or TFE-compatible API shapes
- New notification sinks or new backup formats

## Check

- Write NEW test `TestNotificationTestLimiter` in the NEW file internal/api/notifications/limit_test.go with maintenance, schema-minimum and plain ErrLimitUnavailable cases Check: `go test ./internal/api/notifications/ -run TestNotificationTestLimiter` fails on main
- Write NEW test `TestPlainHeaderStripped` in the NEW file internal/headerauth/strip_test.go for success, refusal, duplicate values and no header Check: `go test ./internal/headerauth/ -run TestPlainHeaderStripped` fails on main
- Write NEW test `TestBackupManifestByteAndKeyBounds` in pkg/backup/manifest_format_test.go with oversized, exactly 10 MB, lying-length and many-key cases Check: `go test ./pkg/backup/ -run TestBackupManifestByteAndKeyBounds` fails on main
- Fix the limiter in internal/api/notifications/notifications.go Check: `TestNotificationTestLimiter` passes
- Strip the secret header in internal/headerauth/headerauth.go Check: `TestPlainHeaderStripped` passes
- Bound manifest bytes and keys in pkg/backup/service.go Check: `TestBackupManifestByteAndKeyBounds` passes
- Run the affected suites Check: `go test -race ./internal/api/notifications/... ./internal/headerauth/... ./pkg/backup/...` passes
- Run the full gate with no new routes. Confirmed by: CI (`make check`). Check: `make check` with zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main defines the ErrLimitUnavailable sentinel for the sink limiter. (`pkg/notify/sinks/limit.go:37`)
- Main defines the test limiter constructor used by notification tests. (`pkg/notify/sinks/limit.go:73 (func newTestLimiter)`)
- Main defines the request body too large error code. (`internal/errcode/catalog_request.go:9`)
- Main has a test that refuses a foreign path in portable file indexing. (`pkg/backup/manifest_format_test.go:14 (TestIndexPortableFilesRefusesForeignPath)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Do any ADRs decide notification test limits, plain-mode header handling, or backup manifest bounds? No ADR is listed in the pack. The pack adrs list is empty and the only inputs are the issue texts plus the three file paths, so there is no ADR decision to apply.
- Does this change conflict with an existing change or ADR? The work stays inside internal/api/notifications/notifications.go, internal/headerauth/headerauth.go and pkg/backup/service.go. It changes no tool-compatible API shape.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
