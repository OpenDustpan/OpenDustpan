## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Research

- [ ] 1.1 Write the NEW file docs/research/cost-gates-finops.md with primary sources and options. Check: The NEW file docs/research/cost-gates-finops.md covers HCP Terraform, Spacelift, env zero and Infracost, cost facts, gates, Expr as gate.Engine adapter, approval rules, fail closed for each option, the 15 minute wait, FinOps policy scope without export shape and a minimal spike on push data. No route: the test reads the file only.
- [ ] 1.2 Open the spike issue and record its number in the NEW file docs/research/cost-gates-finops.md. Check: `gh issue view <number recorded in the note>` shows the open spike issue. No route: the test reads the issue only.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Run check for the changed files. Confirmed by: CI (make check). Check: `make check` with zero findings. No route: the test reads docs files only.
