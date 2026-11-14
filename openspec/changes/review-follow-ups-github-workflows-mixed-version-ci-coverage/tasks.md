## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. CI coverage

- [ ] 1.1 Add the SQLite mixed-version job with full history and a 30 minute timeout to .github/workflows/ci.yml (NEW job, near line 449). It runs `make mixed-version-test` for TestMixedVersion. Check: The CI log of the SQLite job shows fetch-depth 0 with tags and a pass. Confirmed by: CI (the SQLite mixed-version job). The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step.
- [ ] 1.2 Add the PostgreSQL mixed-version job with the existing service wiring and a 30 minute timeout to .github/workflows/ci.yml (NEW job). It runs `make mixed-version-test` for TestMixedVersion. Check: The CI log shows the service ready, the existing DSN in use and a pass with no SQLite fallback. Confirmed by: CI (the PostgreSQL mixed-version job). The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step.
- [ ] 1.3 Add a step to each new job in .github/workflows/ci.yml that prints the fetched tag count before the tests run. Check: The CI logs of both jobs show the tag count line. Confirmed by: CI (both mixed-version jobs). The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step.
- [ ] 1.4 Confirm TestMixedVersionBelowMinimum in e2e/upgrade_mixed_test.go:219 passes in both new jobs, including the simulated zero-tag subcase. Check: The CI logs of both jobs show the pass. Confirmed by: CI (both mixed-version jobs).

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Run `make check` for the workflow edit. The edit touches .github/workflows/ci.yml only. Check: `make check` with zero findings. Confirmed by: the driver's gates. The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step.
