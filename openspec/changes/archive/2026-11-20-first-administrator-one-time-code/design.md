## Context

ADR 339 decides the first administrator of a production installation: the bootstrap admin token asks for a one-time code for one person, and the person redeems it in a console session with a fresh sign-in. This design gives the structure that builds decisions 1 to 8. It decides none of the four open items of RFC 117 (see the proposal, "Fix boundary").

The persona is the operator of a new installation with no shell. The operator ends with one person who holds `site-administrator`, a stored grant that names the bootstrap administrator as creator and the person as holder.

## Decisions

1. **One NEW domain package, add the new file pkg/firstadmin/firstadmin.go, owns the rules.** The package holds the port `Store`, the types, the sentinel errors, the code and its digest, and two pure functions that the service and both adapters call: `CheckRedeem` and `CheckPrincipal`. A rule that lives in one place cannot differ between the two dialects. The store runs the pure checks inside its transaction on the rows it just read (ADR 339 decision 6: "in the store transaction").
2. **Two tables, edited in place in the baseline of each dialect.** `first_administrator` is the marker: one row, `id` fixed to 1 by a CHECK. It holds the principal ID, the grant ID, the session ID, the creator subject and the time. `first_administrator_codes` holds the one code: one row, `id` fixed to 1, the principal ID, the SHA-256 digest in hex (CHECK on 64 hex characters), the creator subject, the reason, the creation time and the expiry. A CHECK fixes the lifetime to 600000 ms, so a row with another lifetime cannot exist. The two tables are separate so that the marker travels in a backup and the code does not. A single row per table makes "one code is valid at a time" a fact of the schema.
3. **The marker is a fact about the data, so it is in the backup.** A restore into another installation brings the grants. The marker comes with them, and the route stays closed. The control table `installation` stays live in a restore, so it cannot hold the marker. The code table holds a digest of a secret for at most 10 minutes. A backup leaves it out and a restore empties it.
4. **The code.** 32 bytes from an injected `io.Reader` (the system random reader in production), encoded as base64url without padding (43 characters). The store keeps the SHA-256 digest in hex. The compare is `subtle.ConstantTimeCompare` on the two digests. The code is in the answer of the offer and in the body of the redeem only. It is never in a URL, a log line, an audit entry, a metric label or an error text.
5. **Routes.**
   - `POST /api/server/first-administrator`: the offer. Action `server:offer-first-administrator`. No family classifies it, so only a principal with `Admin` passes the authorizer. The handler also requires the static bootstrap principal (`Admin`, no kind, no token ID, not a temporary administrator): a personal token with `Admin` cannot make an offer. Body `{"principal": "<ID>", "reason": "<text>"}`. Answer `201` with `{"principal", "code", "expiresAt"}` and `Cache-Control: no-store`.
   - `GET /api/server/first-administrator`: the read of the marker. Action `server:read-first-administrator` in the operator family. Answer `{"made": bool, "principal", "grant", "session", "createdBy", "madeAt", "offer": null or {"principal", "createdBy", "expiresAt"}}`. It never shows the code or its digest. This is the read of ADR 339 decision 7.
   - `POST /api/server/first-administrator/redeem`: the redeem. Action `caller:redeem-first-administrator` in the self family, so each signed-in principal reaches the handler and the transaction decides. The handler requires a console session with its CSRF value. The object is the installation (`resolveServer`). Body `{"code": "<code>"}`. Answer `200` with `{"principal", "role", "grant", "madeAt"}`.
6. **Freshness is its own check.** The redeem calls `sessionauth.Fresh` with the window of the session policy and reads the sign-in time of the session. It does not ask `freshAuthEnabled`: ADR 385 decision 5 keeps this code out of the scope of that setting. A request with an access token has no console session and gets `403` `mfa.fresh-step-required`, as ADR 227 decision 6 says. The store transaction checks the sign-in time again from the session row it holds, so a handler that skipped the check would still be stopped.
7. **One transaction for the redeem.** In this order, in one transaction on the installation scope: read the marker (must be unset), read the code row (must exist and not be expired), compare the digest in constant time, compare the principal of the code with the principal of the session, read the principal row and run `CheckPrincipal`, check the sign-in time, insert the grant, delete the code row, insert the marker, append the audit entry. A failed step rolls back the whole transaction. A grant that exists already gives `ErrAlreadyExists`, and nothing is spent.
8. **One audit entry for the redeem.** The subject is the person. The object is the installation. The changes hold the grant ID, the creator subject (the bootstrap administrator), the session ID and the role. The reason is the reason of the offer. The code and the digest are never in it. A failed audit append rolls back the grant, the spend and the marker (ADR 339 decision 6: "a failed audit write refuses the request").
9. **Offer.** In one transaction: read the marker (must be unset), run `CheckPrincipal` on the live row, replace the code row, append an audit entry. The entry has the action of the offer, the principal as object, the bootstrap subject as creator and the reason. If a code row existed, the entry says it replaced one. The audit entry never holds the code.
10. **Refusals write audit entries.** A refusal of the offer or the redeem writes an entry with the outcome `failure` and the name of the failed rule, as `Handler.fresh` does for a stale sign-in. The write is best effort for a refusal that has no open transaction (the main request is already refused). The failure budget of the sign-in (`admitSignIn`, `failSignIn`) counts a wrong code, as `devCodeSignIn` does, so a guess of the code is slow.
11. **Fail closed.** An unknown state, a set marker, a missing session, a store error or a failed audit write refuses with no change. No path grants on an error.

## Structure

Each path below is NEW.

```
pkg/firstadmin/                  domain: Store port, Code, Digest, State, CheckRedeem, CheckPrincipal, errors
pkg/firstadmin/firstadmintest/   conformance suite of the port
internal/firstadminsvc/          service: Offer, Redeem, State; boundary checks; the clock and the reader are injected
pkg/store/sqlite/firstadmin.go   adapter
pkg/store/postgres/firstadmin.go adapter
internal/api/server/firstadmin.go  the three routes
```

The domain package imports no `net/http`, no `database/sql` and no driver, as `.golangci.yml` requires for domain packages. The service gets the port, the grant ID source (`ident.NewID` with `access.GrantIDPrefix`), the clock and the random reader. The adapter runs the grant insert with the same helper as the grant store, so the grant row has the same checks (the role, the object, the creator) as any other grant.

## Contract

Errors, with the one command that helps in each text:

| Code | Status | When |
| --- | --- | --- |
| `first-administrator.already-made` | 409 | the marker is set (offer and redeem) |
| `first-administrator.code-invalid` | 400 | the code is wrong, spent, expired or for another person |
| `first-administrator.principal-invalid` | 400 | the principal is not a live, enabled person, or is a break-glass person, a temporary administrator, a service or a `dev:` subject |
| `first-administrator.reason-invalid` | 400 | the reason is blank, too long, not UTF-8 or holds a hidden character |
| `first-administrator.grant-exists` | 409 | the person holds the role already |
| `mfa.fresh-step-required` | 403 | the sign-in is not fresh, or the request has no console session |

The reason of the offer uses the same rule as the reason of a temporary administrator: not blank, UTF-8, at most 500 characters, no control or format character. The code rule has one text for the four code failures, so an answer does not say which one happened. The audit entry of the refusal does.

The three routes declare their actions. The route table, the API description and the generated clients carry them.

## Layers (defense in depth)

Each layer stops the attack alone. Each row is a test that sends hostile input to that layer as if the others failed.

| Step of ADR 250 | Port adapter (store) | Service | HTTP handler |
| --- | --- | --- | --- |
| 1. Check the input at the boundary | the CHECK of the digest, the lifetime and the single row; the pure `CheckPrincipal` on the row | the service refuses a non-bootstrap caller, a bad principal ID, a bad reason, a bad code form | the handler decodes with the strict body reader (size limit, no unknown fields) and refuses a code in the URL or a header |
| 2. Encode or contain | placeholders only; no code in a query text | the code never goes to a log, an audit entry or an error | `Cache-Control: no-store`; no code in a log line |
| 3. Limit the effect | one row, 10 minutes, one use, one role, the installation object only | the service builds the grant with the fixed role and object, never from input | the body size limit; the failure budget counts a wrong code |
| 4. Authorize at the point of use | the transaction compares the code principal with the session principal | the service takes the session principal from the caller, never from the body | the route declares its action; the offer needs the static bootstrap principal; the redeem needs a console session and CSRF |
| 5. Fail closed and audit | an unknown state, a set marker or a failed audit append rolls back | an error from the port is a refusal, with a failure entry | each error maps to a closed status; an access token gets `403` |

Redundant checks are kept and commented as defense in depth: the principal checks run in the service and again in the store transaction, and the freshness check runs in the handler and again in the transaction.

## Risks

- A guess of the code. A guess has 256 bits to search, the code lives 10 minutes, and each wrong try spends the sign-in failure budget of the address. The risk is low.
- A leaked code. A person who holds the code and signs in as the named person is the named person, so a leaked code is useful only to that person. The code names the principal, so another person gets `first-administrator.code-invalid`. The operator can replace a code with a new offer.
- The bootstrap admin token stays valid after the redeem (non-goal 3). A holder of the token can still make grants through the grants API with a fresh console session of an administrator, as today. The marker closes only this route.
- An installation of a development build that has an administrator and no marker keeps the route open (non-goal 4). A person who holds the bootstrap admin token can make an offer. The token holder is the highest authority of the installation in any case.
- The marker and the grant can differ after a restore of an old backup. The marker is in the same backup as the grants, so they come from one moment.
