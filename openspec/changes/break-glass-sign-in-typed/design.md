## Context

Break-glass sign-in is the last-resort path when the identity provider is lost. The change makes misuse visible and short-lived.

## Decisions

1. **One value checks reason and expiry.** One unexported value makes the session claims. Its constructor rejects an empty reason, an over-long reason, any control character, DEL, a line or paragraph separator, and an expiry beyond the setting or in the past.
2. **Limit is a setting.** The session limit is a key of the generic settings API. It follows the range and the shorter-of rule of the temporary administrator session limit. The effective value shows in the API.
3. **Generation read at each request.** The session check reads the session generation of the principal and denies an older cookie. No per-cookie row exists.
4. **Derived stale flag.** The principal stores the last test time. The response derives the flag from the injected clock, and from the creation time when no test exists. The column is in the baseline of each dialect and in the table list.
5. **Alert in the sign-in transaction.** The alert audit entry is written in the sign-in transaction. The WARN line follows the commit with CR and LF removed.
6. **Fail closed per request.** A failed per-request audit write fails the request with 503. No code path of this change disables an account.

## Structure

Unexported breakGlassClaims with constructor newBreakGlassClaims. Only the constructor makes a value, and it checks the reason and the expiry bound.

## Risks

- A crash after the commit can lose the WARN line, and the audit entry stays.
- Clock skew between server processes can end a session early or late. The expiry bound limits the error.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter | The sign-in adapter refuses an empty or hostile reason before any session claim. | Sign in with an empty reason, a 501-character reason, a tab, a DEL, U+2028 and CR LF. Each gives refusal and no cookie. |
| service | The service issues only claims from the constructor and denies an expired or old-generation cookie. | Replay an expired cookie, a far-future cookie and an old-generation cookie. Confirm denial with an audit entry. |
| HTTP handler | The handler checks the expiry at the point of use and writes the audit entry. | Send a request after expiry and confirm 401. Fail the audit write and confirm no session at sign-in and 503 on a request. |
