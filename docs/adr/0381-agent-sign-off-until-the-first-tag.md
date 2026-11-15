# 381. Agent sign-off until the first tag

Status: accepted · 2026-11-15. Records a decision of the project owner of 15 November 2026, in the owner's words. Answers the open question of ADR 76

Changes:

- Changes ADR 76 decision 8: the agent sign-off with the commit author identity of the agent applies until the first tag, not until 2026-10-22. This ADR answers the open question of ADR 76.
- Changes ADR 317 decision 2: after the first tag, the `Signed-off-by:` line of an agent commit is the line of the project owner. The author identity of an agent commit stays.
- Extends ADR 21: the release gate gets the item "agent sign-off rule settled".

## Context

ADR 76 decision 8 (owner, 2026-10-17) says: "until the push ends on
2026-10-22, each agent commit carries a `Signed-off-by:` line with the commit
author identity of the agent. The owner cleans up the history later. Nothing
waits for this cleanup." ADR 317 decision 2 (owner, 2026-10-20) sets that
identity for the author and the DCO sign-off. ADR 319 changes only the trailer
part of ADR 317 decision 2.

ADR 76 has an open question for the time after that date: "Who signs off an
agent commit after the current push? A `Signed-off-by:` line is a personal
legal statement, and only the person can give it. The owner decides."

The date passed. The agent sessions kept the practice of decision 8. No rule
covered it after 2026-10-22.

The project owner answered on 15 November 2026: "approved".

## Decision

1. **Until the first tag, the agent signs off (owner, 2026-11-15).** Until
   the first tag, each agent commit keeps a `Signed-off-by:` line with the
   commit author identity of the agent, as ADR 76 decision 8 and ADR 317
   decision 2 say.
2. **The owner cleans up the history before the first tag (owner,
   2026-11-15).** ADR 76 decision 8 already planned this cleanup. Nothing
   waits for it.
3. **After the first tag, the owner signs off (owner, 2026-11-15).** After
   the first tag, each agent commit carries the `Signed-off-by:` line of the
   project owner. The owner gives one standing statement for the agent
   sessions that the owner runs.
4. **The release gate gets an item (owner, 2026-11-15).** The gate of the
   first release gets the item "agent sign-off rule settled".

## Consequences

- `CONTRIBUTING.md` and `CLAUDE.md` say: until the first tag, an agent signs
  off with its own commit author identity. After the first tag, an agent
  commit carries the sign-off of the project owner.
- The release gate in `docs/roadmap.md` gets the item of decision 4.
- ADR 76 gets a pointer to this ADR, and its status line no longer calls the
  sign-off an open question (ADR 365).
- The form of the standing statement of decision 3 is not in this ADR. The
  owner gives it before the first tag, and the release gate item stays open
  until then.
