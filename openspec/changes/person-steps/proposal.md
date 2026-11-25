---
version: 3
---

## Why

**Persona: operator.** As the human verifier I walk each deferred person-step that needs a person: usability run-throughs where the check requires a human mind, plus steps only a person can do. Today agents run every other step in the changes themselves.

The user path this change must make work:

1. **Backlog.** The human opens `openspec/changes/person-steps/tasks.md` and finds each step with its source change and its original check.
2. **Work.** The human completes a step and ticks it here. No server code changes in this change.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A step names a route or a page that moved | The source change moved on. Read the current docs page and update the step. | `make docs-lint` |

Evidence for the claims above:

- 207 open "A person's step" tasks sat across 173 active changes, where they blocked agents that cannot perform them. Proof: `grep -rh --include=tasks.md "person's step" openspec/changes/*/tasks.md | grep -c "^- \[ \]"` answered 207 on the pre-move tree, and no hit lived in `openspec/specs/` or in a delta spec.
- After the split, no open task outside this backlog needs a person. Proof: the same `rg` on the current tree answers 0 for open boxes (ticked history lines stay as record).

## What Changes

- Return every agent-executable step to its source change as a flat task: pure execution, stronger-model reviews (run by the cloud agent as a task step), cloud-agent PM records, and owner pre-approved steps that carry the note "Owner approved 2026-11-21, explicitly not blocking for agents".
- Pair each usability walk whose check requires a human mind: the change holds the agent pre-run with the transcript as evidence, and this backlog holds the human run-through.
- Route workflow files the agent cannot push through `.github-workflows-staging/`: the agent writes the full file there, and this backlog holds the human move into `.github/workflows/`.
- Keep strictly-human steps here: owner-gated checks, cross-PR actions, maintainer publishing, and steps from the 12 archived changes (ticked here with evidence since the change is closed).
- Archive the 12 changes that hold no open task after the move, in the same pull request.

Out of scope:

- Completing any human step (the human does that later, one tick at a time).
- Changing any task check or server behavior.
- Archived changes: their ticked steps stay as history.

Depends on:

- none

Builds on (not a change):

- the docs pages and routes that the steps walk

## Usability

Where the path still costs the person time or surprises them:

- 88 tasks in one file is shorter than 207, and the four sections separate run-throughs from strictly-human steps, archived leftovers, and workflow moves.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The project rules say a pull request that completes the last task of a change archives it in the same pull request (`openspec/project.md`, Tick and archive). This pull request archives the 12 changes it completes.

## ADR check

No ADR is touched: this change moves task tracking only, decides no behavior, and changes no code, route, schema, or setting.

## Open questions

none

Each question below was considered and answered, so none stays open:

- Does any returned step still need a person? No. Each returned step runs with agent-held credentials and its check is a command, a test, or CI. Steps that need owner hands, cross-PR action, or publishing stay here.
- Does the move weaken any change's definition of done? No. Agent steps return with their checks, and each usability walk keeps its human run-through in this backlog.
- Do archived changes lose history? No. Their ticked tasks stay as history, and their open steps tick here with evidence.
