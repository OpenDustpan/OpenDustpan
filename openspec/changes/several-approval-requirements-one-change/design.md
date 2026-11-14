## Context

Slice 1 decider supports one group requirement and returns gate.ErrUnavailable for the rest. This change replaces that unavailable path with evaluation of all stored requirements in the same decider and record shape. It reuses the fresh authentication window of fresh-authentication-window-0-turns and the dev-data approver rules of gate-evaluation-must-apply-dev.

## Decisions

1. **Maximum matching over distinct approvers.** Evaluate all stored approval requirements in one pure matcher in pkg/gate/jobgate with maximum bipartite matching by augmenting-path search over eligible approvers, so a greedy order never refuses a valid set and never counts one approver twice.
2. **Atomic recheck at apply time with stable IDs.** Recheck every approval inside the same store transaction that moves the job or change order to apply: stable principal IDs, current group and owner membership, fresh authentication window at approval and apply time per the window model of fresh-authentication-window-0-turns, session revocation, plan revision binding, withdrawal, self-approval exclusion, dedupe by stable ID, and unless exemption through the existing gate engine called with the change fact document; an unless result of error, timeout, or oversize means not exempt.

## Structure

Unexported requirementSet built only by newRequirementSet which checks at least one requirement, valid approver kinds, and non-negative freshAuthSeconds.

## Risks

- One approver counted twice lets a change apply with too few people.
- Stale fresh-authentication check lets an old sign-in approve a destroy.
- Membership change between approval and apply lets an ineligible approver persist.
- Greedy matching refuses a valid approver set or accepts too few people.
- Concurrent duplicate approvals count one person twice.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter | Matcher allows only distinct eligible approvers per requirement by maximum bipartite matching on stable IDs. | Submit one approver covering two requirements plus a second approver covering one, and check the matcher still finds the valid assignment. |
| service | Service rechecks eligibility, fresh window at approval and apply time, revocation, plan binding, withdrawal, self-approval, and dedupes concurrent duplicate approvals inside the apply transaction. | Change group membership between approval and apply, replay an old approval past the window, post duplicate approvals at once, and check refusal or single count. |
| HTTP handler | Handler returns 403 that names each unmet requirement and the count of missing distinct approvers. | Post an apply with one requirement unmet and check the error body names the requirement and the missing count. |
