# Restore access setting

This guide shows how to let each holder of `server:restore` replace the access
tables in a restore, how to read the value, and how to turn it off again.

The access tables hold principals, temporary administrators, roles, grants,
tokens and sign-in providers. A restore replaces them only when the caller holds
`server:restore-access`, which only the site administrator holds. Any other
restore keeps the current access tables. The setting `restore.replace-access-tables`
changes this. The default is `false`. When the value is `true`, each holder of
`server:restore` replaces the access tables in a restore.

The examples use a token in `$TOKEN` and the host `dustpan.example.com`.

## What you need

- The bootstrap admin token, in `$TOKEN`. It reads the setting.
- A person who holds the `site-administrator` role. This person changes the
  setting. [Make the first administrator](first-administrator.md) shows how to
  make this person.
- The password of that person. The commands below use a local user. A person of
  an identity provider signs in to the console as in
  [Console sign-in](console-sign-in.md), and copies the session cookie
  `__Host-dustpan_session` from the browser.

## Read the value

```sh
curl -si -H "Authorization: Bearer $TOKEN" \
  https://dustpan.example.com/api/dustpan/v1/settings
```

Find the entry with the key `restore.replace-access-tables`. The entry shows
the value, the default and the source. Note the `ETag` header for `If-Match`.

## Turn the setting on

Only the API changes this setting. No flag, environment variable or file sets
it. The caller needs these things:

- The actions `server:manage-settings` and `server:restore-access`. The
  `site-administrator` role holds both.
- A console session with a fresh sign-in. An access token never gives a fresh
  sign-in.

Sign in as the administrator. The command keeps the session cookie in a file.
The answer is `204`:

```sh
curl -c session.txt -X POST -H "Content-Type: application/json" \
  -d '{"userName":"alice","password":"<password>"}' \
  https://dustpan.example.com/api/dustpan/v1/sign-in/password
```

Read the CSRF value of the session. It is the field `csrfToken`:

```sh
curl -b session.txt https://dustpan.example.com/api/server/session
```

Put the value in `$CSRF`. Then send the change with the cookie file, the
`X-Dustpan-CSRF` header and the `Origin` header. The `Origin` header is the
public URL of the server. Send a new random `Idempotency-Key` with each
change, for example the output of `uuidgen`. A request with only
`Authorization: Bearer $TOKEN` gets `403` with `mfa.fresh-step-required`. A
request with no `Origin` header gets `403` with `session.other-site`:

```sh
curl -si -X PATCH -H "Content-Type: application/merge-patch+json" \
  -b session.txt -H "Origin: https://dustpan.example.com" \
  -H "X-Dustpan-CSRF: $CSRF" \
  -H 'If-Match: "3"' -H "Idempotency-Key: $(uuidgen)" \
  -d '{"restore.replace-access-tables":true}' \
  https://dustpan.example.com/api/dustpan/v1/settings
```

Use the `ETag` that the read gave in `If-Match`. The answer is `200`, and the
entry shows `"source":"database"`.

The server writes one audit entry with the old and the new value. The setting
stays on until a person turns it off. While it is on, each restore by a holder
of `server:restore` replaces the access tables. Turn it off when the restore
is done. Turn it off with the same command and the value `false`. This change
needs no fresh sign-in, but it needs the `Origin` header and the CSRF header.

## What a restore does to the setting

The setting is a row in the settings table, and a backup holds that table. A
restore does not use the row of the backup for this key unless the caller holds
`server:restore-access`:

- A caller without `server:restore-access` keeps the live row of the key. A
  backup in which the value is `true` does not turn the setting on, and a
  backup in which it is `false` does not turn it off. This holds also when the
  restore replaces the access tables because the setting is on.
- A caller with `server:restore-access` takes the value of the backup. When the
  value changes, the audit log holds a setting-change entry with the old and the
  new value, and the restore entry names the setting.

## Check a restore

A restore reads the setting once, at the start of the swap, and records the
decision in the same transaction as the swap. A change after that read does not
change the running restore. If the swap step runs again before it commits, it
reads the setting again, and the record always names the swap that happened.
The entry that the server writes when it accepts the request names the value
of the setting that the server read at the request. It does not say what the
swap did. The restore entry in the audit log says what the swap did to the
access tables and to the setting. The server writes the setting-change entry of
a value that the swap took from the backup from the record of the swap, so a
change of the setting after the swap does not change that entry. When the setting is on and the caller did not hold
`server:restore-access`, the entry says that the restore replaced the access
tables because the setting is on. The restore still refuses a result with fewer
than two working break-glass paths.

If the stored value is not `true` or `false`, or the server cannot read it, the
restore keeps the access tables.

## Errors

| Answer | Meaning | What to do |
| --- | --- | --- |
| `403` `mfa.fresh-step-required` | The change needs a fresh console sign-in. The value did not change. | Run the sign-in command again (`POST /api/dustpan/v1/sign-in/password`), repeat the change, then run `GET /api/dustpan/v1/settings`. |
| `403` `session.other-site` | The request has a session cookie and no `Origin` header of the server. The value did not change. | Add `-H "Origin: https://dustpan.example.com"` to the command and repeat it. |
| `403` `access.denied` | The caller lacks `server:manage-settings` or `server:restore-access`. The value did not change. | Ask a holder of both actions to make the change, then run `GET /api/dustpan/v1/settings`. |
| `400` `settings.value-invalid` | The value is not `true` or `false`. | Send `true` or `false`. |
| `412` `settings.revision-stale` | The `If-Match` value is not the current `ETag`. The value did not change. | Run `GET /api/dustpan/v1/settings`, copy the new `ETag` and repeat the change. |
