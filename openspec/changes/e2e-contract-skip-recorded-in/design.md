## Context

The contract check reads one report directory that can hold several runs. The check copies the identity of each run record onto its skips while it reads the run. No new identifier is made.

## Decisions

1. **Scope skips to the recording run.** A skip excuses a missing line only when the run of the line is the run that recorded the skip. A skip with no run or an unknown run excuses nothing.

## Structure

The skip lookup is keyed by run identity taken from the run record, so a skip cannot resolve across runs.

## Risks

- Without a mixed-directory test the bug can return.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | Test-only correctness fix of the e2e contract check |  |  |
