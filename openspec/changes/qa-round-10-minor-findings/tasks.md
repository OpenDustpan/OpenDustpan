## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing test for U+FE0F, NBSP, and invalid UTF-8 in temporary-administrator reason and job location repository in internal/api/server/passkeys_test.go:428 Check: TestPasskeyNameWithHiddenCharacters
- [ ] 1.2 Add failing test for hidden characters in deleted-unit cursor in pkg/access/review_cursor_internal_test.go:15 Check: TestDecodeReviewCursorBounds
- [ ] 1.3 Add failing test for case-only duplicate person name in pkg/access/access_test.go:792 Check: TestGrantChecksSubjectAndNamespace
- [ ] 1.4 Add failing test for cancel of succeeded restore as 409 restore.ended and second cancel as 202 in pkg/backup/service_durable_test.go:577 Check: TestStartRefusesAnInvalidID
- [ ] 1.5 Add failing test for text/plain namespace create as 415 with no stored row in pkg/dustpan/bodygate_test.go:132 Check: TestSharedBodyGateAcrossAdapters

## 2. Fix

- [ ] 2.1 Fix the shared hidden-character set, person-name folding, public lock and drift-start answers, and ended-restore cancel in internal/errcode/catalog_restore.go:30 Check: TestStartRefusesAnInvalidID
- [ ] 2.2 Fix 415 on POST /api/server/namespaces and the version echo cap in pkg/dustpan/bodygate_test.go:132 Check: TestSharedBodyGateAcrossAdapters
- [ ] 2.3 Fix the restore cancel and artifact-sweep wording in the NEW docs/guides/qa-round-10-fixes.md page Check: `rg -n "restore.ended" docs/guides/qa-round-10-fixes.md`

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run wiring, e2e step against the real CLI on POST /api/server/namespaces with an access token in internal/errcode/catalog_restore.go:30, docs page and usability check. Confirmed by: CI (make check, make e2e). Check: make check with zero findings and POST /api/server/namespaces with an access token plus make e2e
