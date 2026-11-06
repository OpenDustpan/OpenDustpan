# 252. The dev sign-in person and the reserved dev prefix

Status: accepted · 2026-10-19. The project owner decided it. Supersedes in part ADR 237 decision 12 (who the session of the link belongs to). Applies ADR 250 to the dev sign-in.

Changed by:

- ADR 253 extends decision 7: two more layers before dev data arrives.

## Context

ADR 237 decision 12 says that the one-time code signs a browser in "as the
admin of the dev installation". The bootstrap administrator (ADR 6) is the
holder of a token, not a stored person, and a session needs a stored person.
So the sign-in must make one. A person that a path outside the dev sign-in
can make is a risk. An identity provider with the ID `dev` composes the
subject `dev:<sub>`, and the API makes a principal with a subject that the
user chooses. Either path could make or reach the person `dev:admin` and
take the session role of the administrator. A database of `dev` that an
operator restores into a production installation would carry the person with
it.

## Decision

1. **The person.** The session of the dev link belongs to the stored person
   `dev:admin`. It is a local person with no group. The sign-in makes it
   with one internal path of the principal service, which takes no subject.
   No other path makes a principal with a subject that starts with `dev:`.
2. **The role.** The bootstrap administrator of `dev`, the holder of the
   token in the token file, gives the person the site-administrator role.
   The grant uses the access service, so the audit log names the bootstrap
   administrator as the granter. A grant that exists already is not an
   error. The sign-in fails, and starts no session, when the access service
   or the bootstrap administrator is missing. It checks both before it
   spends the code.
3. **A failed grant.** The code is spent when the person is made, because the
   check of the code comes first. When the grant fails, the person stays with
   no role and no session, so it can do nothing. The next valid sign-in with
   a new code finds the same person and gives it the role.
4. **The reserved prefix.** The subject prefix `dev:` is reserved, without
   regard to case. The validation of a subject refuses it, so
   `POST /api/server/principals` and the other public paths cannot make such a
   principal. The sign-in through a provider refuses a subject with the prefix
   before it reads the store.
5. **The reserved provider ID.** The provider ID `dev` is reserved. The
   configuration of the server, the API of the stored providers and the
   lookup of a stored provider refuse it. So a provider cannot compose
   `dev:<sub>`.
6. **Use only on a dev installation.** The route of the dev sign-in is
   mounted, and the handler of the route works, only when the installation
   kind is dev. A handler with a code and with the kind production serves no
   route, and the handler function alone refuses.
7. **Production refuses dev people.** A server that is not of the kind dev
   refuses to authenticate a session or a token whose principal has a subject
   that starts with `dev:`. This covers a dev backup that an operator restores
   into a production installation. The start of such a server logs one
   warning that names the people with that prefix, up to 20 and with the
   count, and does not stop.
8. **Tests.** Each layer has its own test that sends the hostile input to
   that layer alone (ADR 250 decision 4): the validation of a subject, the
   check of a principal, the create of a principal, the sign-in of a provider,
   the update of a principal, the provider ID in the configuration, in the
   API and in the lookup, the route table, the handler, the check of the
   session cookie, the check of a token and the warning at the start.

## Consequences

- A person `dev:admin` exists in each database that `dev` served. It has the
  role of the site administrator and a session, and it stays in the audit
  log.
- The display name of `dev:admin` cannot change through the API of the
  principals, because the check of a principal refuses the prefix. This is
  on purpose: the dev sign-in owns the person.
- A provider with the ID `dev` cannot exist. A deployment that used the ID
  before must give the provider a new ID, and its people sign in again.
- The warning at the start looks for the prefix `dev:` in lower case. The
  authentication refuses every case.
