## Context

Confirmation stays a human act on the change order. The server stores the delegator as immutable author at proposal time, binds each confirmation to one plan revision, checks revision and approved state atomically, and blocks apply and destroy jobs until the order is approved.

## Decisions

1. **Author stored at proposal.** Store authorSubject at proposal time from the delegation subject and never change it; later revoke or expiry of the delegation does not change author.
2. **Confirm route and state.** The NEW route POST /api/dustpan/v1/namespaces/{namespace}/change-orders/{id}/confirm takes revisionId, needs the NEW action changeorder:confirm on the namespace object, and moves state in-review to approved only when the approval rule is met; else it records the confirmation and stays in-review.
3. **Atomic revision and approval check.** Check revisionId match and approved state in one fenced transaction at apply and destroy start; a new revision or missing approval refuses with the NEW error code changeorder.not-ready.
4. **Grant loss and queued jobs.** A queued destroy job that has not started is cancelled when a new revision arrives; the next approval check re-evaluates the grant of the confirmer.

## Structure

Unexported type confirmGate with constructor newConfirmGate that holds author subject, confirmer subject, revision ID and approval rule; only the constructor builds a valid check.

## Risks

- Delegator counted as non-author breaks four-eyes.
- Apply or destroy starts before any human confirmation.
- Confirmation applies to a newer revision than the one the person read.
