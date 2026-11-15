## Context

Two small corrections remain: a wrong comment and a missing count of skipped operations.

## Decisions

1. **Fix in place.** Edit the comment text. Add a counter and a log line where the poll applies its bound of 64.
2. **The counter lives where the bound is.** (product manager, 14 Nov 2026) DurableClaimer gets the small injected port `job.ClaimObserver` (NEW), set through the option `job.WithClaimObserver` (NEW); the default is the no-op `job.NoopClaimObserver` (NEW), and a nil observer is refused. The log line uses the claimer's injected slog logger. The metric is wired in pkg/dustpan, which owns the metrics package, so pkg/job imports no metrics code. No file under pkg/store changes.

## Structure

One NEW port (`job.ClaimObserver`) with a no-op default and one NEW option (`job.WithClaimObserver`); no new type in a store. The count uses the existing poll code path in pkg/job/claimer.go.

## Risks

- A new counter adds one metric series.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| completeness | 100 | percent counted | operations skipped by the poll bound of 64 |
