# 389. Window 0 passes once and never locks out

Status: accepted · 2026-11-20. Decided by the project owner (owner, 2026-11-20). The owner accepted the reading of ADR 227 decision 3 that the lead recommended. The words of ADR 227 decision 3 stay as they are

Changes:

- Extends ADR 227 decision 3: a window of 0 asks for a fresh authentication at each step. A fresh authentication that the person made for the step passes the step once, and then it is used up. A window of 0 never locks a step out.

## Context

ADR 227 decision 3 says (owner, 2026-10-18):

> **0 asks every time (owner, 2026-10-18).** A window of 0 asks for a
> fresh authentication at each step. No value turns the check off.

The decision does not say what "asks for a fresh authentication at each
step" means for a person who has just signed in or who has just made a
fresh step. A walk of the code found this result: a window of 0 locked
every route that needs a fresh authentication, the session policy
included. The lock held right after a sign-in, and it held for the
bootstrap token too. The only way back was a change in the database.

The owner decided on 20 November 2026, after a recommendation of the lead
("Yes sounds good"):

> Fresh window 0 (ADR 227 decision 3 reading): a fresh authentication made
> for this step passes once, then it is used up. It is never a lockout.

ADR 385 (owner, 2026-11-15) already changed the end of ADR 227 decision 3:
the setting `freshAuthEnabled` turns the check off. This ADR does not
change that.

No tag shipped a window setting, so no data conversion is needed (ADR 73).

## Decision

1. **A fresh authentication passes one step, once (owner, 2026-11-20).**
   With a window of 0, a fresh authentication that was made for this step
   passes the step. Then it is used up. The next step needs a new fresh
   authentication.
2. **Never a lockout (owner, 2026-11-20).** A window of 0 is never a
   lockout. A person who makes a fresh authentication for a step can do
   that step, also right after a sign-in. This holds for each route that
   needs a fresh authentication, the session policy included.
3. **The words of ADR 227 decision 3 stay (owner, 2026-11-20).** "0 asks
   every time" and "no value turns the check off" keep their meaning. This
   ADR gives the reading of "asks every time". It adds no other change.

## Implementation reading of the lead

Decisions 1 to 3 are the words of the owner. The text in this section is
not an owner word. It is the implementation reading of the lead. The Opus
security review (rev2) of the code pull request checks it.

1. The mark of a fresh authentication is per sign-in, that is per console
   session. It is single-use. The first step that needs a fresh
   authentication uses it, and the store consumes it in one transaction
   with the fence of that step.
2. An access token never gives a fresh step, and the bootstrap token is an
   access token. This is the existing rule of `requireFresh` and of
   `errFreshStepRequired` in `internal/api/server/freshness.go` (the
   comment and the check at lines 64 to 66 and 108 to 116 on `main` of
   20 November 2026). The bootstrap token has no way to give the step.
3. The way out of a window of 0: sign in as an administrator, then do one
   step, for example a change of the session policy.

## Consequences

- The fresh-authentication lane writes the spec and the code from this ADR.
  The code of the session policy route, `internal/api/server/freshness.go`
  and the store that keeps the mark change.
- Each layer (the handler, the service and the store) gets a test with a
  window of 0: a fresh step passes once, a second use is refused, and a
  person right after a sign-in is not locked out. The test also shows that
  an access token and the bootstrap token are refused.
- The open points that the owner did not decide go back to the project
  manager as questions. This ADR gives no answer to them.
- ADR 227 gets the reverse item.
