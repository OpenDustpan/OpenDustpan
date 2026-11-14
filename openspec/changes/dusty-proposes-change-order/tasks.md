## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write failing tests of the scenarios: draft then confirm-draft, scope denials, once per key, confirm-draft window. Check: TestDustyDraftThenConfirmDraft and TestDustyConfirmDraftWindow fail on main

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Wire the draft and the confirm-draft through changeorder.Service with the distinct NEW confirm-draft route and NEW action changeorder:confirm-draft; write draft and confirm-draft audit entries through every-step-audited. Check: BLOCKED by the missing NEW draft and NEW confirm-draft routes and their actions
- [ ] 3.2 Write the docs page for confirm-draft and check the user path. Check: usability check from the docs page alone
- [ ] 3.3 Run the full check. Check: `make check` with zero findings
