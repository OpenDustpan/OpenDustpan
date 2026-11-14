## Context

The TFE workspace list pages and counts in SQL on main. This change applies two review nits with no behavior change: a defense-in-depth comment and a test context parent.

## Decisions

1. **Comment and test hygiene only.** Keep both checks in Handler.readsEveryUnit and add a comment that says each check stands alone. Derive the test address context from t.Context(). File separate follow-up issues for the allowlist cost and the frozen migration comment.

## Structure

No new type. Handler.readsEveryUnit keeps both checks.

## Risks

- A comment-only edit cannot change list output; existing handler tests prove no behavior change.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | Comment and test hygiene with no behavior or performance effect |  |  |
