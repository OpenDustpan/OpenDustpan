## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Research note

- [ ] 1.1 Write the failing check first: the note file does not exist on main. Check: `ls docs/research/dusty-hosted-default-model.md` fails before the change.
- [ ] 1.2 Add the new file docs/research/dusty-hosted-default-model.md with the scored table, sources and access dates, and quality marked untested. Check: `rg -n "accessed|untested" docs/research/dusty-hosted-default-model.md` lists them.
- [ ] 1.3 Add the decision rule, conflict rule, no-pinning rule and review date 2027-05-14 with refresh owner. Check: `rg -n "decision rule|2027-05-14|contested|region pinning" docs/research/dusty-hosted-default-model.md` lists them.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Publish the note, read it from the docs page alone, and run all checks. Check: `make check` passes with zero findings and `make docs-lint` passes.
