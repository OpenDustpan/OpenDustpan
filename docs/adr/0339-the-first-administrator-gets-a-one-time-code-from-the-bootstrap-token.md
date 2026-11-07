# 339. The first administrator gets a one-time code from the bootstrap token

Status: accepted · 2026-11-07. Decided by the project owner on 7 November 2026. Accepts option B of [RFC 117](../rfc/0117-the-first-administrator-of-an-installation.md)

Changes:

- Extends ADR 6: the bootstrap admin token has one more use at first boot. It asks for the code that makes the first administrator.
- Extends ADR 227 decision 6: the rule that a change needs a fresh authentication stays. The code makes an offer. The person's fresh authentication makes the grant.

## Context

On a new production installation, nobody can make the first grant. The bootstrap admin token cannot make a grant. Each grant needs a fresh authentication, and an access token never gives one (ADR 227 decision 6). A person who signs in has no role. Only `opendustpan dev` gives an administrator session. [RFC 117](../rfc/0117-the-first-administrator-of-an-installation.md) gives the facts and five options.

The project owner chose option B of RFC 117.

## Decision

Decided by the project owner on 7 November 2026.

1. **Option B of RFC 117 is accepted (owner, 2026-11-07).** The bootstrap admin token asks for a one-time code for one principal. The person redeems the code in a console session with a fresh sign-in.
2. **The request (owner, 2026-11-07).** The bootstrap admin token calls `POST /api/server/first-administrator` with the principal ID and a reason. The route works only while the marker "first administrator made" is not stored. After the marker, it answers `409` with its own error code.
3. **The redeem (owner, 2026-11-07).** The person sends the code in the body of a `POST` in a console session with a fresh authentication. One transaction makes the `site-administrator` grant on the installation, spends the code and stores the marker. It writes an audit entry. The entry names the bootstrap administrator as the creator, and the person and the sign-in ID as the one who took the role.
4. **The code (owner, 2026-11-07).** The code is 32 bytes from `crypto/rand`. It goes in a `POST` body only. The server never logs it, stores a hash only and compares in constant time. One code is valid at a time, for 10 minutes, for one use, for one role (`site-administrator`) on the installation only.
5. **The principal (owner, 2026-11-07).** The route accepts only the ID of a live, enabled person of the installation. It also needs a reason with no control or format characters. It refuses a break-glass person, a temporary administrator, a service and a `dev:` subject.
6. **Fail closed (owner, 2026-11-07).** The redeem checks three facts in the store transaction. The marker is not set. The code names the principal of the session. The authentication of the session is fresh. An unknown state, a set marker or a failed audit write refuses the request. The refusal writes an audit entry.
7. **The marker is a stored fact (owner, 2026-11-07).** The marker is an installation fact with a read in the API. A later loss of every administrator goes through recovery (ADR 57, ADR 245), not through this route.
8. **The guides change with the code (owner, 2026-11-07).** Three guides change in the pull request that builds the route. They are console sign-in, local users and workload identity.

## Consequences

- No new command opens the database, so ADR 39 stays as it is. The route works in a container and in the Helm chart with no shell. It works with local users and with the persons of an OpenID or OAuth provider.
- The route, the redeem and the marker each need an entry in the route test, the policy check and the API description. Each defense layer in decisions 4 to 6 needs its own test.
- A holder of the bootstrap admin token can make an offer before the first administrator exists. That holder can already do each action that needs no fresh authentication.
- This ADR does not decide the open questions 2 to 5 of RFC 117 beyond decision 3. Open: if the person needs a second factor besides the fresh authentication. Open: if the first setup must ask for the two break-glass accounts of ADR 70 decision 24. Open: if the redeem ends the bootstrap admin token (ADR 53 decision 9). Open: how an upgraded installation that has a site administrator gets the marker.
