## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing API tests for start, approval-wait and terminal notices with scope, stable unit ID and dedupe key. Check: TestJobNoticesAppended fails on main, passes after.
- [ ] 1.2 Add failing test for expiry notice at 14 and 3 days with renew and revoke cases and no secret in body. Check: TestTokenExpiringNoticeAppended fails on main, passes after.
- [ ] 1.3 Add failing sender test for POST headers, body shape with sequence, and two-secret rotation verify. Check: TestWebhookSignatureHeaders fails on main, passes after.
- [ ] 1.4 Add failing tests for filter match, URL refusal, creator skip, dedupe retry, suspend reason, retry delay and redeliver. Check: TestSubscriptionCreatorReach fails on main, passes after.

## 2. Build

- [ ] 2.1 Append notices in the job transaction and in the token expiry schedule with fence and audit entry. Check: `go test ./pkg/notify/... ./pkg/job/...` passes.
- [ ] 2.2 Wire dispatcher checks, enqueue idempotence, retry backoff, suspend and redeliver through the existing subscription deliveries route. Check: `GET /api/dustpan/v1/namespaces/{namespace}/subscriptions/{id}/deliveries` lists pending rows with reason.
- [ ] 2.3 Run e2e with a webhook sink and a matching subscription against the real binary over TLS. Check: The real receiver logs signed POSTs for one Pulumi and one OpenTofu job.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Write docs page: create sink, subscribe with type filter, rotate signing secret, redeliver, read deliveries, order by sequence. Check: Docs page commands run as written in a fresh install.
- [ ] 4.2 Usability check with namespace administrator and recorded errors. Check: Persona completes sink to signed delivery from the docs page alone.
- [ ] 4.3 Route actions, route test, audit entries, and conformance suites pass. Check: `make check` with zero findings.
