# Restore access setting

This guide shows how to let each holder of `server:restore` replace the access
tables in a restore, how to read the value, and how to turn it off again.

The access tables hold principals, roles, grants, tokens and sign-in
providers. A restore replaces them only when the caller holds
`server:restore-access`, which only the site administrator holds. Any other
restore keeps the current access tables. The setting `restore.replace-access-tables`
changes this. The default is `false`. When the value is `true`, each holder of
`server:restore` replaces the access tables in a restore.

The examples use a token in `$TOKEN` and the host `dustpan.example.com`.

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

- The actions `server:manage-settings` and `server:restore-access`.
- A console session with a fresh sign-in. An access token never gives a fresh
  sign-in.

```sh
curl -si -X PATCH -H "Content-Type: application/merge-patch+json" \
  -H 'If-Match: "3"' -H "Idempotency-Key: $(uuidgen)" \
  -d '{"restore.replace-access-tables":true}' \
  https://dustpan.example.com/api/dustpan/v1/settings
```

The server writes one audit entry with the old and the new value. The setting
stays on until a person turns it off. While it is on, each restore by a holder
of `server:restore` replaces the access tables. Turn it off when the restore
is done. Turning it off needs no fresh sign-in.

## Check a restore

A restore reads the setting once, at the start of the swap. A change after that
read does not change the running restore. When the setting is on and the caller
did not hold `server:restore-access`, the audit log holds an entry that names
the setting and says that the restore replaced the access tables. The restore
still refuses a result with fewer than two working break-glass paths.

If the stored value is not `true` or `false`, or the server cannot read it, the
restore keeps the access tables.

## Errors

| Answer | Meaning | What to do |
| --- | --- | --- |
| `403` `mfa.fresh-step-required` | The change needs a fresh console sign-in. The value did not change. | Sign in to the console again, repeat the change, then run `GET /api/dustpan/v1/settings`. |
| `403` `access.denied` | The caller lacks `server:manage-settings` or `server:restore-access`. The value did not change. | Ask a holder of both actions to make the change, then run `GET /api/dustpan/v1/settings`. |
| `400` `settings.value-invalid` | The value is not `true` or `false`. | Send `true` or `false`. |
