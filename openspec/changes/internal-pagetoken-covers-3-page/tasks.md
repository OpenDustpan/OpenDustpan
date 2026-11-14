## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing strict-decode tests for unknown members and trailing data on each wire shape that calls shared Decode. Check: TestPagetokenDecodeRejectsUnknownMember and TestPagetokenDecodeRejectsTrailingData in internal/pagetoken fail on main.
- [ ] 1.2 Add failing boundary tests in NEW internal/api/inventory/deleted_cursor_test.go for deleted-units Tool, Name, and UnitID checks, registry provider fingerprint mismatch, and namespace cursor field checks. Check: TestDeletedCursorRejectsUnknownTool, TestProviderTokenRejectsBadFingerprint, and TestNamespaceCursorRejectsBadField fail on main in NEW internal/api/inventory/deleted_cursor_test.go.

## 2. Fix

- [ ] 2.1 In internal/pagetoken/pagetoken.go:58 (func Decode) call DisallowUnknownFields, reject non-whitespace trailing data, update the package doc to state that unknown members are rejected, and route deleted, registry provider, and namespace cursors through pagetoken.Encode and pagetoken.Decode with filter fingerprints and field checks. Check: `go test ./internal/pagetoken/... ./internal/api/inventory/... ./internal/api/registry/...` passes.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire check: exercise GET /api/dustpan/v1/namespaces/{}/deleted-units with valid, overlong, fingerprint-mismatch, extra-member, and bad-field tokens through the existing route and gate with an access token. Check: `GET /api/dustpan/v1/namespaces/{}/deleted-units` with a forged token returns an invalid-token error with an access token through the existing namespace grant gate, and the same strict check applies to GET units, registry, and profile list routes; confirmed by CI server tests and e2e list paging.
- [ ] 4.2 Run the full check and the usability walk of the list path from the docs page alone. A person's step. Check: `make check` passes with zero findings.
