## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing checks first

- [ ] 1.1 Write a shell check script in NEW `scripts/check-scanning-workflows.sh` for the four scenarios. Check: `sh scripts/check-scanning-workflows.sh` fails on main because the workflow files are absent.

## 2. Workflows

- [ ] 2.1 Add the CodeQL workflow in NEW `.github/workflows/codeql.yml`. Check: `sh scripts/check-scanning-workflows.sh` passes the CodeQL checks.
- [ ] 2.2 Add the Scorecard workflow in NEW `.github/workflows/scorecard.yml`. Check: `sh scripts/check-scanning-workflows.sh` passes the Scorecard checks.
- [ ] 2.3 Add github-actions coverage in NEW `.github/dependabot.yml`. Check: `rg -n 'github-actions' .github/dependabot.yml` finds the ecosystem.

## 3. Definition of done

- [ ] 3.1 Write the triage note in the NEW file docs/guides/ci-scanning.md: CodeQL is advisory-only and Semgrep is the required gate. Check: `make docs-lint` passes on docs/guides/ci-scanning.md.
- [ ] 3.2 Run the pin check, workflow lint and the full check. Check: `rg -n 'uses: .+@[a-f0-9]{40}' .github/workflows/codeql.yml .github/workflows/scorecard.yml` lists only full-SHA refs, `actionlint` passes, and `make check` reports zero findings.
