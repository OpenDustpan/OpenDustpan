## Context

Push intake stays in the trigger domain and starts plans only through the job service. The Forge port delivers authenticated push events; the dispatcher matches units in the same namespace under the namespace fence. ADR 267 gives the match and plan-only rule; ADR 299 changes ADR 267 decisions 14 and 19 to one credential rule; ADR 338 and ADR 273 give the starter and read checks.

## Decisions

1. **Paths filter and push dispatcher.** Extend Trigger with an unexported paths field set only by NewTrigger; empty input defaults to the cleaned working directory of the unit code location with a trailing separator rule, or the repository root marker for a root location, and any empty or absolute-escaping pattern fails the constructor. The dispatcher accepts only events that the Forge port authenticated, loads candidate review-request-push triggers of the namespace under the namespace fence, keeps targets of the same repository and branch, keeps triggers whose normalized paths touch the union of changed files across commits counting a rename as a delete plus an add and a force-push on its head files, refuses a fork push or a missing fork flag with an audit entry, starts each plan as the namespace service principal with source kind forge and the head commit per ADR 267 and ADR 299, serializes supersede steps per namespace so two concurrent pushes leave only the newer head plan, and supersedes the queued or running plan of the same trigger subject to the open-job limit.
2. **Credential path for author code.** Run author code with unit profile credentials obtained through the profile attachment authority and not through the starter token. The namespace service principal holds only the stored start-of-plan grant with no token and no session per ADR 338; the job open of the profile uses the attachment authority and output reads use the last saver of the trigger per ADR 273.

## Structure

Trigger holds an unexported paths field set only by NewTrigger; empty input defaults to the normalized working directory of the code location and any invalid pattern fails the constructor so no stored trigger holds an open filter.

## Risks

- A fork push must never run author code with unit profile credentials.
- A spoofed push event must never start a plan.
- A truncated changed-file list must never silently skip or misroute a plan.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| intake authentication | Boundary: the push intake accepts only events authenticated by the Forge port with signature and delivery-ID deduplication and rejects replayed or spoofed deliveries without a start. | Send a forged push with a bad signature and a replayed push directly to the intake and show no plan starts. |
| fork refusal | Service: the dispatcher refuses a push marked as fork and refuses a push with a missing fork flag, and writes an audit entry for each refusal even when other checks are bypassed. | Send a fork push and a push with an empty fork flag with all other checks bypassed and show no plan starts. |
| fork refusal at start | Service: the job start checks the fork flag again and refuses when the flag is missing before any profile open. | Call the job start with a missing fork flag directly at the service and show a denial with no profile open. |
| authorization at start | Use: the job service checks the namespace fence, the plan-only port, the open-job limit, and the stored start grant of the namespace service principal at the point of start. | Call the start with a cross-namespace unit and with an apply operation directly at the service and show a denial. |
