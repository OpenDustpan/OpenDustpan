## 1. failing tests

- [ ] 1.1 Add failing conformance cases for Trigger Paths. Check: Trigger conformance cases for Paths default, normalization, root marker, and match pass.
- [ ] 1.2 Add failing dispatcher tests for push intake. Check: Dispatcher push match, paths union with rename and force-push, truncation record, fork and missing-flag refusal with audit at both guards, supersede with cancel edge cases, serialization, duplicate and branch-delete, intake spoof and replay refusal, authorization denial, and start audit cases fail on main and pass after the fix.

## 2. delivery

- [ ] 2.1 Implement Paths field, dispatcher match, forge source kind, service-principal start with attachment-authority credentials, and plan replacement. Check: All new unit and conformance tests pass.
- [ ] 2.2 Wire dispatcher to the Forge port event with authorization actions on trigger routes. Check: Trigger routes declare unit:read and unit:manage on the unit object and the route test passes.
- [ ] 2.3 Add hostile tests for each guard layer. Check: Hostile intake test with bad signature and replayed delivery starts nothing, and fork-guard bypass tests at dispatcher and at start deny with audit.
- [ ] 2.4 Add e2e step against a fake forge. Check: Fake-forge e2e step shows push starts plan, unrelated push starts nothing, fork push writes audit, newer push replaces older plan.
- [ ] 2.5 Write the user docs page. Check: Docs page for push plans is present and make docs-lint passes.
- [ ] 2.6 Run definition of done: wiring, routes with actions, e2e, docs, usability, make check. Check: Usability check from the docs page passes and make check reports zero findings.
