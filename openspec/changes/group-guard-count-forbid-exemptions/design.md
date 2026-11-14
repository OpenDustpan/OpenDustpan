## Context

Membership in a group named by an access limit changes the result of the Cedar forbid check. The guard blocks a caller with group management rights alone from widening access through add or remove. It runs in the same member-add transaction as check-reach-when-grant-membership.

## Decisions

1. **Guard widening membership changes.** At membership write time the access service lists each saved access limit whose Cedar policy tests the group stable ID in a principal-in-group test, in when or in unless, directly or through a parent group. An add to a group named in unless widens access. A remove from a group named in when widens access. A change that widens any limit needs the guard of the open question. A group named by no limit keeps the current guard.
2. **Stable ID matching with fail closed parse.** Group references in limits use stable group IDs. The guard parser extracts Group stable IDs from the saved Cedar policy text and compares IDs only, with parent chain resolution. A limit set that the parser fails to parse fails closed: the service refuses the membership change.
3. **Single fenced transaction for check and write.** The limit read, the reach check of check-reach-when-grant-membership, and the membership write share one fenced namespace transaction. A limit saved between a separate read and write fails to bypass the guard because the check and the write commit together and retry on a concurrent limit change, covered by a named concurrency test.
4. **Break-glass exemption and site admin refusal.** Break-glass sessions stay outside the guard, per ADR 223 decision 7. The guard refuses a site administrator without the widening rights; only a holder of the rights for a widening change makes the change, and a break-glass session makes it without the guard with the audit entry that says so.

## Structure

Unexported type wideningCheck built solely by newWideningCheck(groupID, limitSet) which parses saved forbid policies for Group stable IDs in when and unless and returns the set of limited actions; callers fail to build the set by hand.

## Risks

- Parser misses a group reference form and the guard passes a widening change.
- Guard demands more actions than needed and blocks routine membership work.
