## Context

CodeQL raised 8 alerts of go/weak-sensitive-data-hashing and go/constant-oauth2-state on main. Issue #2493 records each as a false positive against main at 96ed862 with no code change. This change confirms each site on main and dismisses the alerts.

## Decisions

1. **Dismiss with no code change.** Dismiss alerts 2, 3, 8, 9, 10, 11, 12, 13 as false positive with the reason from the issue table. No file changes. The record is the table in issue #2493 plus the CodeQL dismissal entries.

## Structure

No structure change. No new type or field. No behavior change.

## Risks

- No code change means no new behavior risk. The remaining risk is a wrong verdict, covered because the confirm task re-reads each flagged site on main and stops dismissal when a site changed since 96ed862.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | No code change, so no latency, storage, or limit target changes. |  |  |
