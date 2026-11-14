## Context

The mixed-version upgrade tests exist on main. The linked review asks for CI jobs with complete git history and PostgreSQL. The change adds CI wiring only.

## Decisions

1. **Two-store mixed-version CI jobs.** Add two jobs in the CI workflow that run the existing mixed-version-test target, one on SQLite and one on PostgreSQL, each with full history and tags.
2. **Zero-tag coverage without tagless checkout.** TestMixedVersionBelowMinimum covers the zero-tag fallback by its existing simulated subcase. Each job logs the fetched tag count in the job log.
3. **PostgreSQL wiring reuses existing service.** The PostgreSQL job reuses the existing PostgreSQL service wiring, DSN variable and readiness wait of the CI workflow. If the database is not ready, the job fails and never falls back to SQLite.
4. **Run schedule and required status.** Both jobs run on every pull request with a 30 minute timeout each. They start as non-required status checks until they are stable for 14 days.

## Structure

No new type. The fix boundary is the CI workflow file. No product code changes.

## Risks

- Full git history increases checkout time for these two jobs only.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | CI-only change with no product latency, storage or rate-limit effect |  |  |
