## 1. Build

- [ ] 1.1 Failing tests first: add failing conformance tests for path match, rule counting, evaluate mode, ownership unless auto-apply, warn block, unless error, author-only wait and record fields. Check: gatetest.TestOwnershipPathAddsRequirement fails on main, passes after the fix.
- [ ] 1.2 Implement ownership match, ownership-rule unless evaluation and autoApply in the decision record. Check: gatetest.TestOwnershipUnlessAutoApply passes on SQLite and PostgreSQL.
- [ ] 1.3 Add limit and matching-cost tests for broad globs and large rule sets. Check: A save above 256 rules, 64 path sets or a 4 KiB condition fails, and 256 rules with broad globs evaluate at change.planned within 500 ms.

## 2. Definition of done

- [ ] 2.1 Add e2e step with a real runner and real CLI binaries over TLS for schedule-sourced and warn paths. Check: Real CLI plan and apply for a schedule-sourced unit show autoApply true with no approval, and a warn case waits, with triggers provided through #901.
- [ ] 2.2 Write the docs page in the same pull request. Check: Docs page runs as written and shows ownership examples, unless and autoApply behavior.
- [ ] 2.3 Run usability check for the owner approval and auto-apply path. Check: Namespace administrator completes the path from the docs page alone; each error names the one command that helps.
- [ ] 2.4 Run full checks and conformance suites. Check: `make check` reports zero findings.
