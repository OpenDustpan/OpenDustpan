---
version: 1
---

Issue: #2561.

## Problem

A lost contract line must stay visible when one e2e report directory holds more than one run.

## Evidence

- Skips match by tool and version over all runs in one report directory, so one run can excuse a missing line of another run. Proof: Issue text: skippedScenarios matches by tool and version over all runs in one report directory.
- CI writes one directory per job and is not affected. Proof: Issue text: CI is not affected, because each job writes its own directory.
- The pack does not locate skippedScenarios in non-test code of pkg, internal or cmd. Proof: Keyword evidence: rg -n -F --glob '!*_test.go' 'skippedScenarios' pkg internal cmd gives 0 matches. The file is in the e2e suite, outside those paths.

## Fix boundary

- Match each recorded skip only to the run that recorded it.
- Add a mixed-directory test with a binary run and an in-process run.

Out of scope:

- Changes to contract lines or skip reasons
- Changes to CI job layout, which already uses one directory per job

## Check

- Write a failing mixed-directory test in the e2e suite, in the NEW file e2e/contract_skip_scope_test.go, with one binary run and one in-process run in one directory; BLOCKED by no server route because the check reads report files offline and uses no credential. Check: The mixed-directory contract skip test fails on main.
- Scope the skip lookup to the recording run in the e2e contract check; the file that holds skippedScenarios in the e2e suite is confirmed first, and no server route or credential is used. Check: The mixed-directory contract skip test passes.
- Run the e2e contract tests and make check; BLOCKED by no server route because the check is offline. Check: `make e2e` and `make check` pass with zero findings.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The pack finds no non-test use of skippedScenarios in pkg, internal or cmd, so main has no proven skip scoping in those paths. (`rg -n -F --glob '!*_test.go' 'skippedScenarios' pkg internal cmd (0 matches)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

Not spec-ready: a code check fails: main_facts[0]: "where" is not file:line (func Name); a code check fails: task "Scope the skip lookup to the recording run in the e2e contract check; " runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part; a code check fails: task "Run the e2e contract tests and make check; BLOCKED by no ser" is not confirmed by the agent alone: end it with one fixed phrase: "Confirmed by: CI (...)", "Confirmed by: the driver's gates." or ; a code check fails: and 1 more.

Questions considered and answered:

- Should a skip excuse a missing contract line of another run in the same report directory? No. A skip excuses a line only in the run that recorded it.
- What happens when a skip has no run identity or an unknown one? It excuses no line. The line stays reported as missing.
- Does a skip still excuse a line in its own run? Yes. The positive case stays.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
