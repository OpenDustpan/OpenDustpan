## Context

Ownership evaluation runs at change.planned inside the gate engine over stored ownership rules and named path sets from the depended route changes. An advanced ownership rule holds an optional unless Cedar body over change.planned facts with the operation source. The result is written into the decision record as autoApply for that requirement.

## Decisions

1. **Ownership match adds one requirement per rule.** Match ownership rules by unit scope then by overlap of named path sets with the touched paths of the plan. Each matched enforce rule adds exactly one requirement for its owners. Evaluate mode writes the match in the record with no requirement.
2. **Ownership-rule unless gates conditional auto-apply.** Evaluate the unless body of the matched ownership rule itself over change.planned facts with the operation source, with the same unless semantics as several-approval-requirements-one-change. A true unless marks only that ownership requirement excused for auto-apply. Any warn on the plan or any unless evaluation error marks autoApply false. Evaluation runs once at change.planned and the apply reuses that record.

## Structure

Unexported ownershipMatcher built only by newOwnershipMatcher with checked rule IDs, path globs, owner groups and referenced path sets; bad input fails at save with gate.invalid.

## Risks

- A broad path pattern adds surprise approvals; the decision record names the matched rule and path set.
- An unless body that reads a missing fact fails closed to no auto-apply.
