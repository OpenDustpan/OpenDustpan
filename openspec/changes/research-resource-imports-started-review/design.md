## Context

Research only. No code, no route, no store change. Output is a research note with primary sources and a provisional recommendation, plus an explanation page under docs/explanation that already exists.

## Decisions

1. **One note plus one page, no code and no decision.** Write one research note that covers imports started from a review request, post-merge apply, resource imports as runner jobs, and state rm and state mv as reviewed jobs, then write one explanation page for post-merge apply. The note quotes owner decisions 27.4, 27.7, 42.1 and 6.4 and states conflicts without deciding, anchors approval rules to ConfigService.ApprovalRules, and names the follow-up RFC or ADR issue. No prototype and no job kind in this change.

## Structure

No code structure. The bug class of code change does not apply because this change adds no constructor and no stored reference.

## Risks

- Recommendation may conflict with owner decisions 27.4, 27.7, 42.1 and 6.4; the note records each conflict and stops before any RFC or ADR.
- Hostile import block in a fork review request, wrong address, conflict with a finding action, and closed or force-pushed review request may change the recommendation; the note covers each case.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | Research and docs only; no runtime behavior changes. |  |  |
