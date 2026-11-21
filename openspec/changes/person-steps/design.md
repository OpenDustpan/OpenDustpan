## Context

Person-steps (usability checks from the docs page alone, by-hand command runs, and other steps that need a person) sat inside 173 implementation changes, where they blocked agents and hid the human backlog. This change gathers the 207 open person-steps into one backlog with no behavior change.

## Decisions

1. **One backlog, grouped by source change.** Each task keeps its original number, text, and check, plus its source change in parentheses. The human completes them in any order and ticks each one here.
2. **No task text changes.** The checks stay exactly as the source changes wrote them, so a step still means what its change meant.
3. **Done steps stay put.** Ticked person-steps, including done ones in archived changes, stay where they are as history.
4. **Archive the 12 completed changes in the same pull request.** They hold no open task after the move, and the project rules ask for the archive in the same pull request.

## Structure

No new structure and no code. The bug class is a person-step lost across 173 files; one backlog list with a source reference on each task is the structure that removes it.

No custom analyzer and no new tool.

## Risks

- A step names a route or a page that moves before the human reaches it. The source reference leads back to the change, and the human updates the step.
- A new person-step lands in a source change later. The same marker ("A person's step") makes the next sweep find it.
