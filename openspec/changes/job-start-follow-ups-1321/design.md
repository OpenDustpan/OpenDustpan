## Context

Two small corrections remain: a wrong comment and a missing count of skipped operations.

## Decisions

1. **Fix in place.** Edit the comment text. Add a counter and a log line where the poll applies its bound of 64.

## Structure

No new type and no new constructor. The count uses the existing poll code path.

## Risks

- A new counter adds one metric series.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| completeness | 100 | percent counted | operations skipped by the poll bound of 64 |
