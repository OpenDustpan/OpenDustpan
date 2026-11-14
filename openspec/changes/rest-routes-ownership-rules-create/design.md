## Context

Ownership rules are stored gate data with no REST access today. The change adds thin REST routes over the gate package with ETag preconditions and a single weakens check shared by approval rules, approval defaults and ownership rules.

## Decisions

1. **Ownership rule routes.** Add list, read, put and delete routes for ownership rules at each scope that holds them, each with its authorization action and object in the route table. A put carries ETag and honors If-Match; a stale match answers 412 and changes nothing.
2. **One weaken rule.** Put one weakens function in the gate domain next to the rule types that maps each field to weaken or tighten: mode, count, groups, users, paths, when, unless and freshAuthSeconds, plus create as non-weakening and delete as weakening. Each write route calls it before it writes. A write that does not weaken security works with a session or an access token with no step-up sign-in. A write that weakens security needs a fresh authentication and fails with 403 mfa.fresh-step-required on an access token, a service principal without a fresh step or an agent that never has one.

## Structure

No new tool and no new analyzer. The unexported ownership rule record keeps its constructor check in pkg/gate. The weakens function is a pure function on old and new rule values so the bug class of divergent weaken checks is impossible.

## Risks

- A weaken definition that misses a field lets a pipeline weaken a rule with an access token.
- A missing action on one route breaks the route policy check.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| HTTP handler | Handler checks sign-in, authorization action on the scope object, ETag If-Match, JSON shape and size limit before it calls the service. | Send a stale If-Match, an unknown field and an oversize body directly at the handler; each is refused and the stored rule is unchanged. |
| service | Service holds the weakens check and the fresh-authentication check at the point of use for each write, independent of the handler. | Call the service write with an access token that weakens the rule while the handler check is bypassed; the service refuses with 403 mfa.fresh-step-required. |
| port adapter | Store write runs in the namespace fence transaction with a version compare; a concurrent winner makes the loser fail with no partial write. | Race two puts with the same ETag directly at the port; one wins and the other gets a version conflict with no lost update. |
