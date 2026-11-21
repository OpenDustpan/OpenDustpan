## Context

Person-steps sat inside 173 implementation changes, where any step that an agent could not perform blocked the whole change. The split puts each step where its executor lives: agent steps run in the changes, and only steps that need a person stay in this backlog.

## Decisions

1. **Agent steps return to the changes as flat tasks.** Pure execution, stronger-model reviews (run by the cloud agent as a task step), cloud-agent PM records, and owner pre-approved steps with the approval note. Agents run them, tick them with evidence, and archive their changes without getting blocked.
2. **Usability walks pair up.** The change holds the agent pre-run with the transcript as evidence; the backlog holds the human run-through, done only if the transcript leaves doubt. No buckets, no labels: the pairing is the structure.
3. **Workflows the agent cannot push stage in `.github-workflows-staging/`.** The agent writes the full file there; the backlog holds the human move into `.github/workflows/`. No workflow diffs in pull request bodies.
4. **Strictly-human steps stay here.** Owner-gated checks, cross-PR actions, maintainer publishing, and steps from archived changes (ticked here with evidence).
5. **Archive the 12 completed changes in the same pull request.** They hold no open task after the move, and the project rules ask for the archive in the same pull request.

## Structure

No new structure and no code. The bug class is a person-step lost across 173 files or blocking an agent; one backlog grouped by who acts (run-throughs, strictly-human, archived, workflow moves) plus flat agent tasks in the changes is the structure that removes it.

No custom analyzer and no new tool.

## Risks

- A step names a route or a page that moves before the human reaches it. The source reference leads back to the change, and the human updates the step.
- A new person-step lands in a source change later. The same marker ("A person's step") makes the next sweep find it.
- A staged workflow rots before the human moves it. The move tasks name the writing change, and the agent re-runs the checks after the move.
