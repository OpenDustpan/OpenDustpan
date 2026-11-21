---
version: 2
---

## Why

**Persona: operator.** As the human verifier I walk each deferred person-step: usability checks from the docs page alone, by-hand command runs, and other steps that need a person. Today those steps sit inside 173 implementation changes, where they block agents that cannot perform them and hide the human backlog across many files.

The user path this change must make work:

1. **Backlog.** The human opens `openspec/changes/person-steps/tasks.md` and finds each person-step with its source change and its original check.
2. **Work.** The human completes a step and ticks it here. No server code changes in this change.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A step names a route or a page that moved | The source change moved on. Read the current docs page and update the step. | `make docs-lint` |

Evidence for the claims above:

- 207 open tasks carry the marker "A person's step" across 173 active changes. Proof: `grep -rh --include=tasks.md "person's step" openspec/changes/*/tasks.md | grep -c "^- \[ \]"` answers 207, and the file list counts 173 changes. No hit lives in `openspec/specs/` or in a delta spec.
- 12 of those changes hold no other open task after the move, so this pull request archives them in the same pull request as the project rules require.

## What Changes

- Move the 207 open "A person's step" tasks from 173 changes into the NEW change `openspec/changes/person-steps/`, each keeping its original number, text, and check, plus its source change in parentheses.
- Remove those lines from the 173 source `tasks.md` files. Agent-doable tasks, including ticked person-steps that are already done, stay where they are.
- Archive the 12 changes that hold no open task after the move, in the same pull request.

Out of scope:

- Completing any person-step (the human does that later, one tick at a time).
- Changing any task text, check, or server behavior.
- Archived changes: their ticked person-steps stay as history.

Depends on:

- none

Builds on (not a change):

- the docs pages and routes that the moved steps walk

## Usability

Where the path still costs the person time or surprises them:

- 207 steps in one file is a long list. The per-change sections keep each step next to its siblings, so the human completes one change at a time.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The project rules say a pull request that completes the last task of a change archives it in the same pull request (`openspec/project.md`, Tick and archive). This pull request archives the 12 changes it completes.

## ADR check

No ADR is touched: this change moves task tracking only, decides no behavior, and changes no code, route, schema, or setting.

## Open questions

none

Each question below was considered and answered, so none stays open:

- Does any moved step need a design decision first? No. Each step keeps its source change, where the design and the route already live.
- Does the move weaken any change's definition of done? No. The steps stay tracked with their checks in this backlog, and the source changes keep every agent-doable task.
- Do archived changes lose history? No. Their ticked tasks, including done person-steps, move to `openspec/changes/archive/` untouched.
