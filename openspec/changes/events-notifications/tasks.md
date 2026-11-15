## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing API tests in NEW file internal/api/notifications/job_notices_test.go for start, approval-wait and terminal notices with scope, stable unit ID and dedupe key. Check: TestJobNoticesAppended fails on main, passes after.
- [ ] 1.2 Add failing test in pkg/accesstoken/accesstoken_test.go for expiry notice at 14 and 3 days with renew and revoke cases and no secret in body. Check: TestTokenExpiringNoticeAppended fails on main, passes after.
- [ ] 1.3 Add failing sender test in pkg/logsink/webhook/webhook_test.go for POST headers, body shape with sequence, and two-secret rotation verify. Check: TestWebhookSignatureHeaders fails on main, passes after.
- [ ] 1.4 Add failing tests in pkg/notify/notify_test.go for filter match, URL refusal, creator skip, dedupe retry, suspend reason, retry delay and redeliver. Check: TestSubscriptionCreatorReach fails on main, passes after.

## 2. Build

- [ ] 2.1 Append notices in the job transaction and in the token expiry schedule with fence and audit entry, in pkg/notify (notice builders) and pkg/accesstoken/accesstoken.go (expiry schedule). Check: `go test ./pkg/notify/... ./pkg/job/...` passes.
- [ ] 2.2 Wire dispatcher checks, enqueue idempotence, retry backoff, suspend and redeliver in pkg/notify through the existing subscription deliveries route. Declaration file: the route table of the adapter package (`make generate` then writes internal/api/apidesc/openapi.json; never edit it by hand). Adapter file: internal/api/notifications. Check: `GET /api/dustpan/v1/namespaces/{namespace}/subscriptions/{id}/deliveries` with a personal access token of a principal that holds namespace:manage-notifications lists pending rows with reason.
- [ ] 2.3 Run e2e in NEW file e2e/notifications_test.go with a webhook sink and a matching subscription against the real binary over TLS. Check: `POST /api/dustpan/v1/namespaces/{namespace}/subscriptions` and `GET /api/dustpan/v1/namespaces/{namespace}/subscriptions/{id}/deliveries` with a personal access token; the real receiver logs signed POSTs for one Pulumi and one OpenTofu job. Confirmed by: CI (the agent writes the e2e test).

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Write docs page in the existing file docs/guides/notification-destinations.md: extend it with create sink, subscribe with type filter, rotate signing secret, redeliver, read deliveries, order by sequence. Check: Docs page commands run as written in a fresh install. Confirmed by: CI (an e2e step in the NEW file e2e/events_notifications_docs_test.go runs each command of the docs page against the e2e test server). A person's step. `make docs-lint` passes on each edited guide.
- [ ] 4.2 Usability check with namespace administrator and recorded errors. Check: Persona completes sink to signed delivery from the docs page alone. A person's step.
- [ ] 4.3 Route actions, route test, audit entries, and conformance suites pass. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
