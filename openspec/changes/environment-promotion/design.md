## Context

Two units from one blueprint hold different config values. The team needs a read of the difference and a controlled copy from one unit to the other with history, gates, and a write-back commit. RFC 55 is a draft design for this step; this change follows its draft semantics of copy as an edit with a write-back commit and a plan under milestone 5 gates, and the step proves that behavior.

## Decisions

1. **Comparison is a read of effective values.** Comparison reads effective values of the two units and returns per-field source, target, and equality state with secret values masked and equality without plain text.
2. **Promotion is an edit plus a plan.** Promotion is an edit of the target unit settings that copies selected config fields, then starts a plan job under milestone 5 gates with unit:read on source and unit:manage on target.
3. **Stable ID references.** Units are referenced by stable ID in comparison and promotion; names resolve once to IDs, a rename still resolves, and a remove makes the ID resolve to no live unit.
4. **Committed edit with decision record.** Promotion commits the target edit first and returns 201 with the committed edit and the decision record when a gate refuses the plan; a refused plan never returns 403, and only a new edit undoes the commit.
5. **Revision guard and crash recovery.** Promotion carries the compared target revision in the request and the server checks it atomically against a concurrent edit; a crash between commit and plan start leaves a committed edit that a retry reuses with a new plan and an audit entry.

## Structure

Promotion runs inside unitsettings edit path with unexported promotionEdit built only by promoteUnits constructor that checks same tool, same namespace, and field allowlist, so an incompatible copy cannot be built.

## Risks

- Copy of a secret value leaks plain text in comparison output or in git history
- Concurrent edit of target unit loses an update
- Promotion across tools or blueprints copies an incompatible shape
- Crash between commit and plan leaves an edit with no plan
