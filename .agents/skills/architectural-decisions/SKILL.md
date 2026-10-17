---
name: architectural-decisions
description: >
  Use when the user asks why code is built a certain way, or before an architectural change
  (new pattern, restructuring, choice between approaches). Also use when code or commit
  messages contain "WHY:", "DECISION:", "TRADEOFF:" or "ADR:". Uses the Repowise MCP tools.
user-invocable: false
---

# Architectural decisions with Repowise

`get_why` finds recorded decisions and the history behind code. Choose the mode by the
arguments:

- `get_why(query="why is X done this way?")`: keyword and semantic search of decisions.
- `get_why(query="path/to/file")`: decisions for that file. If none exist, it returns the
  git history of the file.
- `get_why(query="…", targets=["path/to/file"])`: search anchored to files. Decisions that
  touch the files rank higher.
- `get_why()`: decision-health dashboard (stale decisions, ungoverned hotspots).

The workspace has more than one repository. Pass `repo` (an alias from `list_repos`) to
limit the answer to one. This project also keeps its decisions in `docs/adr/` and
`docs/rfc/`. Read those files for the full text.

## Before an architectural change

1. Call `get_why(query="<area you change>")`.
2. If a decision applies, show it to the user before you continue. The change may
   contradict it. This project requires an RFC for a change that contradicts an ADR.
3. If no decision applies, continue and say that none is recorded.

## When a file has decision markers

If a file has `WHY:`, `DECISION:`, `TRADEOFF:` or `ADR:` comments, call
`get_context(targets=["that/file"])` for the decision record and the affected modules.

## Recording a decision

Do not record a decision yourself. If the user makes one, say so. This project writes an
ADR only after the project owner accepts the decision. An open question is an RFC.
