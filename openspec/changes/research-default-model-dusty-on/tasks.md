## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Research note

- [ ] 1.1 Write the failing check first: the NEW note file docs/research/dusty-hosted-default-model.md does not exist on main yet. Check: `ls docs/research/dusty-hosted-default-model.md` fails before the change.
- [ ] 1.2 Add the new file docs/research/dusty-hosted-default-model.md with the scored table, sources and access dates, and quality marked untested. Check: `rg -n "accessed|untested" docs/research/dusty-hosted-default-model.md` lists them.
- [ ] 1.3 Add the decision rule, conflict rule, no-pinning rule and review date 2027-05-14 with refresh owner. Check: `rg -n "decision rule|2027-05-14|contested|region pinning" docs/research/dusty-hosted-default-model.md` lists them.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Publish the note, read it from the docs page alone, and run all checks. Check: `make check` passes with zero findings and `make docs-lint` passes. Confirmed by: CI (an e2e step in the NEW file e2e/research_default_model_dusty_on_docs_test.go runs each command of the docs page against the e2e test server). Confirmed by: CI (the agent runs the fast checks only).
