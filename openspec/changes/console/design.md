## Context

The server behavior for members, groups, grants, the export, the token warning and the caller access endpoint is defined in the depends_on changes. This change adds no server route, action or answer shape. It wires the browser tests of the console repository against the suite server over TLS and writes the docs page.

## Decisions

1. **Browser-test wiring only.** Run the browser tests against the suite server over TLS for each step of the roadmap. Grant, export and banner behavior is asserted in the owning changes.
2. **Reuse the access endpoint reason.** Show each locked section with the reason that the existing access endpoint answers. No new redaction rule is added here.

## Structure

No new stored type and no new unexported type. Each existing answer shape stays.

## Risks

- Browser tests run in the console repository and can drift from the server API shape.
- The locked-section layout follows a draft RFC; only the console repository changes if it changes.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | none | none | This change adds no server code, so no latency, size or availability target changes. |

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| Browser tests against the suite server | The browser job loses TLS trust to the suite server. | Fix the test trust bundle and run the job again. | No member, group or grant change. |
| Add member and grant flow | The session cookie expires in the middle of the flow. | Sign in again and run the step again. | The step stops at the sign-in page with no write. |
