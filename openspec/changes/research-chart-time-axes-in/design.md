## Context

Console charts need precedent for time-axis direction in Arabic and Hebrew before any behavior change.

## Decisions

1. **Research note first, no chart change.** Write the note to NEW docs/research/chart-time-axes-rtl.md with one precedent table, source dates and versions, one recommendation, user-expectation evidence, disagreement rule, mixed-content rule, license rule, and a statement of which ADR holds the future rule.
2. **Links first for third-party sources.** Link to third-party sources with access date and product version instead of storing screenshots, unless the license allows storage in the repository.

## Structure

No code structure changes; the note is a document that a later change may use.

## Risks

- A product precedent may not reflect what users expect, so the note records sources separately from the recommendation.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | Research note only; no runtime behavior changes |  |  |
