## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write a failing service test in the NEW file pkg/notify/rotate_test.go for NEW rotate: two active refs, secret answered once Check: TestSinkRotateKeepsTwoKeys fails on main
- [ ] 1.2 Write a failing test in the NEW file pkg/notify/rotate_test.go for NEW confirm: one ref after confirm, unknown ref refused Check: TestSinkRotateConfirmRetiresOldRef fails on main
- [ ] 1.3 Write failing guard tests in the NEW file pkg/notify/rotate_test.go for stale If-Match, concurrent rotate and Idempotency-Key replay Check: TestSinkRotateGuards fails on main

## 2. Build

- [ ] 2.1 Wire the NEW rotate and confirm routes with namespace:manage-notifications in the NEW file internal/api/notify/sinks_rotate.go; reuse sink.conflict and sink.invalid from internal/errcode/catalog_sink.go:15 Check: POST /api/dustpan/v1/namespaces/{namespace}/sinks/{id}/rotate with an access token returns the new key id
- [ ] 2.2 Add the NEW rotate and confirm operations to the NEW file internal/api/apidesc/openapi.sinks.json and regenerate the clients Check: make clients-check passes and the description lists both NEW routes
- [ ] 2.3 Add the e2e step in the NEW file e2e/chat_sinks_rotation_test.go against a local receiver that the operator allowlist permits, using the existing test route Check: POST /api/dustpan/v1/namespaces/{namespace}/sinks/{id}/test with an access token shows both signatures verify on the local receiver
- [ ] 2.4 Write the docs page in the NEW file docs/guides/chat-sinks-rotation.md for rotation and the local receiver setup with the receiver key-id move Check: A test runs each command of the page as written

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run the route policy check, hostile tests, usability check from the docs page, and make check Check: make check reports zero findings
