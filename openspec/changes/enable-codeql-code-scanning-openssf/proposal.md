---
version: 1
---

Issue: #1083.

## Problem

CI has no dataflow SAST and no supply-chain posture score. Both tools are free once the repo is public. Both stay blocked while the repo is private on the free plan.

## Evidence

- Issue 1083 asks for a CodeQL workflow for Go and an OpenSSF Scorecard action. Proof: Issue #1083 body: Add a CodeQL workflow (github/codeql-action) for Go; Add the OpenSSF Scorecard action on a schedule.
- Both tools are blocked on the private free-plan repo. Proof: Issue #1083 body: Blocked today by the free-plan private repo.
- Issue 1083 requires pinned actions and least-privilege permissions. Proof: Issue #1083 body: actions pinned to commit SHAs as the rest of CI is; Keep permissions least-privilege per job.

## Fix boundary

- Add NEW `.github/workflows/codeql.yml` for Go on push, pull request and a weekly schedule.
- Add NEW `.github/workflows/scorecard.yml` on a schedule, with publish_results false until the owner approves true once public.
- Add NEW `.github/dependabot.yml` with the github-actions ecosystem on a weekly interval.
- Add NEW `scripts/check-scanning-workflows.sh` that checks pins, permissions, triggers and gates.
- Add NEW `docs/guides/ci-scanning.md` with the triage note.

Out of scope:

- Replace or reconfigure the Semgrep gate
- Change product behavior, API or the console
- Buy GitHub Advanced Security for the private repo

## Check

- Write a shell check script in NEW `scripts/check-scanning-workflows.sh` for the four scenarios. Check: `sh scripts/check-scanning-workflows.sh` fails on main because the workflow files are absent.
- Add the CodeQL workflow in NEW `.github/workflows/codeql.yml`. Check: `sh scripts/check-scanning-workflows.sh` passes the CodeQL checks.
- Add the Scorecard workflow in NEW `.github/workflows/scorecard.yml`. Check: `sh scripts/check-scanning-workflows.sh` passes the Scorecard checks.
- Add github-actions coverage in NEW `.github/dependabot.yml`. Check: `rg -n 'github-actions' .github/dependabot.yml` finds the ecosystem.
- Write the triage note in the NEW file docs/guides/ci-scanning.md: CodeQL is advisory-only and Semgrep is the required gate. Check: `make docs-lint` passes on docs/guides/ci-scanning.md.
- Run the pin check, workflow lint and the full check. Check: `rg -n 'uses: .+@[a-f0-9]{40}' .github/workflows/codeql.yml .github/workflows/scorecard.yml` lists only full-SHA refs, `actionlint` passes, and `make check` reports zero findings.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The workflow cmd/s3bench/bench-s3.workflow.yml already declares a permissions block, so least-privilege permissions have a pattern on main. (`cmd/s3bench/bench-s3.workflow.yml:23 (permissions)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

Not spec-ready: a code check fails: `.github/workflows/codeql.yml` is named by the design or a task but is not in the touches of the stack plan; a code check fails: `.github/workflows/scorecard.yml` is named by the design or a task but is not in the touches of the stack plan; a code check fails: `.github/dependabot.yml` is named by the design or a task but is not in the touches of the stack plan; a code check fails: and 1 more.

Questions considered and answered:

- Does this change need an ADR or RFC? No. Issue 1083 Notes state scanning changes no product behavior and needs no ADR or RFC. The pack ADR list is empty, so no ADR decision applies.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
