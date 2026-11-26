## Context

Two runner jobs plan from state versions of one OpenTofu workspace and register disjoint non-replace claims. The server records both as accepted with state, start time and principal. No apply and no merge happen.

## Decisions

1. **Claim record.** Store claim ID, workspace ID, base state version ID, normalized address set, principal, optional job ID, state, start time and end time. Compare only against accepted claims in one transaction.
2. **No change to the lock path.** Serialize registrations with a workspace row lock in one store transaction plus a unique key on job ID, base and normalized set. Registration takes no lock.

## Structure

Unexported field claimSet inside type claimRegistry, with constructor newClaimRegistry that normalizes addresses once.

## Risks

- Two registrations race and read the same accepted set.
- Address spelling hides an overlap.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| HTTP handler | Checks unit:claim-register for POST and unit:read for GET, rejects unknown fields, oversize and empty sets. | Send each hostile request directly to the handler; each fails closed. |
| service | Normalizes addresses once and runs the disjoint check in the write transaction. | Send duplicate and mixed-case addresses directly to the service. |
| port adapter | Holds the workspace row lock and writes the claim in the same transaction. | Run parallel registrations on SQLite and PostgreSQL. |
