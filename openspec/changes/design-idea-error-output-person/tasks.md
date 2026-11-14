## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Side effects (computed from main)

- [ ] 1.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 2. Design

- [ ] 2.1 Refine this change when its RFC is accepted; the NEW file openspec/changes/design-idea-error-output-person/tasks.md holds the refined tasks Check: `git status --short openspec/changes/design-idea-error-output-person/tasks.md`
