# 388. A service token ends when its maker loses the right

Status: accepted · 2026-11-20. Decided by the project owner (owner, 2026-11-20). The owner reversed the kept token of ADR 70 decision 6

Changes:

- Changes ADR 70 decision 6: the server revokes a service token when its maker loses the last act-as path. The flag of the access review stays for decision 2.

## Context

ADR 70 decision 6 says that a service token outlives the right of its
maker. When the maker loses the last act-as path to the principal of the
token, the server keeps the token, and the access review flags it. A token
can live up to 365 days. So a person who lost the right to act as a
principal can still act as it with a token that the person made before.
The access review no longer shows that the person can act as the
principal, but the person can.

Google Cloud keeps a service account key after its maker loses access.
GitHub ends a token when its holder loses access. The project wants each
permission to be a stored grant that a person can see and explain
("No magic" in `CLAUDE.md`).

On 20 November 2026 the owner decided on issue #555 that the token ends.

## Decision

1. **The server revokes the token (owner, 2026-11-20).** When the maker of
   a service token loses the last act-as path to the principal of the
   token, the server revokes the token. The act-as path is
   `principal:manage-tokens` on the principal, through any grant: a direct
   grant, a grant of a group, or a grant on a container of the principal.
   A bootstrap admin token always has a path. A disabled or deleted maker
   has no path.
2. **Each revocation is a recorded automatic action (owner, 2026-11-20).**
   Each revocation writes an audit entry. The subject is the server. The
   entry names the token, its maker, the change that ended the path (its
   action and object) and the principal that made that change.
3. **The check runs after each change of access.** After each committed
   change that can end an act-as path, the server checks the maker of
   each active service token, before it answers. These changes are a
   revoke of a grant, a change of a role, a change of the members or the
   grants of a group, a delete of a group, and a disable or a delete of a
   principal. The check is not in the transaction of the change, because
   the act-as path is a decision of the authorizer, and the SQL store
   cannot make that decision in the transaction without a second copy of
   the rules.
4. **The check also runs at each use.** The authentication of a service
   token checks its maker too. A token whose maker has no path gets 401,
   and the server revokes it, with an audit entry. This covers a change that runs no
   check: a group sync of an identity provider, a change on another
   instance, or a stop of the server between the commit and the check. A
   check that cannot decide refuses the token and keeps it.
5. **A credential that must outlive a person uses a trust rule (owner,
   2026-11-20).** A team that wants a credential that does not end with the
   right of a person uses a workload trust rule. The guide of access tokens
   says so.
6. **The access review flag stays for decision 2.** ADR 70 decision 6 also
   flags a token that decision 2 would refuse today, because the principal
   got more grants after its maker made the token. That flag stays. This
   ADR changes only the case of a maker that lost the last path.

## Consequences

- A person who loses the right to act as a principal can no longer act as
  it with an old token. The access review shows what a person can do.
- A pipeline that uses a token of a person who left stops. The audit entry
  names the change that caused it. The fix is a trust rule or a new token
  from a person who holds the right.
- The token row stores its maker as a subject, not as a stable principal
  ID. A new principal that takes the subject of a deleted maker could be
  read as the maker. The check after the delete revokes the token first, so
  this needs a delete with no check and a new principal with the same
  subject before the first use. A stable maker ID needs a change of the
  schema. Issue #3798 tracks it.
