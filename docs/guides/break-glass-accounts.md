# Keep a break-glass account

A break-glass account is an account of one named person that does not
depend on the identity provider. It is the way in when the identity
provider is not available. Each installation keeps at least two, unless a
self-hosted operator sets the recovery mode `none`.

## The rule

The server refuses to disable or delete the last enabled break-glass
account. This applies to two requests:

- `POST /api/server/principals/{id}/disable`
- `DELETE /api/server/principals/{id}`

Both answer 409 with the error code `principal.last-break-glass` and this
message:

```text
cannot remove the last enabled break-glass account: keep a second enabled break-glass account first
```

The request changes nothing. The account stays enabled.

A disabled account does not count as a second account, because it cannot
sign in. The server checks the count in the same database transaction as the
write. When two administrators disable the last two accounts at the same time,
one request succeeds and the other answers 409.

## Disable one of two accounts

You can disable one break-glass account at any time, also when you suspect a
compromise of its credential. Do this while a second enabled account exists.
The disable works in maintenance mode, as every other revocation does. The
server writes an audit entry for it.

The caller must hold every action that the grants of the account give, as
for every other principal. A caller that does not hold them gets 403.

## Replace the last account

1. Make the second break-glass account before you disable the first.
2. Check that the second account can sign in.
3. Disable the first account.

The enable of a disabled account is not a revocation. It stays refused with
503 in maintenance mode.

## Other paths

The inactivity disable never disables a break-glass account. The expiry of a
temporary administrator ends a temporary administrator only, and a temporary
administrator is never a break-glass account. The two routes above are the
other ways to disable or delete a principal, and both follow the rule.
