# Run several instances

This guide gives the rules for several `opendustpan serve` processes on one PostgreSQL database and one S3-compatible bucket.

This layout is not supported yet. The README gives the current status: the server runs on one node. The [architecture guide](architecture-and-sizing.md) shows the target design. Use this guide to prepare an installation for that design. Do not use several instances in an installation yet.

## What each instance shares

Give each instance the same values for these settings.

| Setting | Rule |
| --- | --- |
| `DUSTPAN_DATABASE` | One PostgreSQL database. SQLite holds one instance only. |
| `--blob-store` (`DUSTPAN_BLOB_STORE`) | One S3-compatible bucket and prefix, `s3://<bucket>[/<prefix>]`. The `fs` store is on the disk of one host. The [S3 guide](s3-blobs.md) shows the endpoint, region and credential settings. |
| `--region` (`DUSTPAN_REGION`) | One region. |
| `DUSTPAN_KEY_ENCRYPTION_KEY` or `DUSTPAN_KEY_ENCRYPTION_KEY_FILE` | One key-encryption key. |
| `DUSTPAN_ADMIN_TOKEN` or `DUSTPAN_ADMIN_TOKEN_FILE` | One list of bootstrap admin tokens, in the same order. |
| `--backup-target` (`DUSTPAN_BACKUP_TARGET`) | One S3-compatible bucket, `s3://<bucket>[/<prefix>]`. |
| `--public-url` (`DUSTPAN_PUBLIC_URL`) | The URL of the load balancer. |
| `--oidc-*` settings and `DUSTPAN_OIDC_CLIENT_SECRET` | One set of sign-in providers. |
| `--audit-chain`, `--audit-signing-key-file`, `--audit-verify-keys-file` | One audit chain setting and one signing key. |

The [configuration reference](../reference/configuration.md) gives each setting.

## The key-encryption key

Make the key one time. Give the same value to each instance.

```sh
openssl rand -base64 32
```

Set the value in `DUSTPAN_KEY_ENCRYPTION_KEY`, or in a file that `DUSTPAN_KEY_ENCRYPTION_KEY_FILE` names on each host. Keep the key outside the database and the backup target.

Do not use `--generate-key-encryption-key` (`DUSTPAN_GENERATE_KEY_ENCRYPTION_KEY`) on more than one instance. Each instance then makes its own key file on its own host. A secret that one instance encrypts can then not be read by the other instances.

When the database holds secrets, a start with a different key stops with an error that names the `rewrap` command. When the database holds no secrets yet, the start cannot compare the keys. Two keys then stay in use without an error.

## Bootstrap admin tokens

Give each instance the same tokens in the same order. The server gives the subject `admin` to the first token, `admin-2` to the second token, and so on. The server checks the holder of an OpenTofu lock by subject. With another order, one token gets another subject on each instance. A lock that one instance gave can then fail on another instance.

A change to the list needs a restart of each instance.

## Sign-in sessions

Console sessions live in the database. Each instance accepts a session that another instance made. No session setting exists.

Give each instance the same sign-in providers and client secrets. The browser returns from the identity provider to the URL in `--public-url`. The load balancer can send that request to any instance.

## Settings that stay on one host

Some targets write to the disk of the host that runs the instance. Each instance then holds a part of the data.

- A `dir:` backup target. Each instance writes its backups to its own disk. A restore on another instance does not find them. Use an S3-compatible backup target.
- A `file:` audit sink. Each instance writes only its own audit entries. Use a `blob:`, `syslog+tls://` or `webhook:` sink.
- A `file:` log sink. Each instance writes only its own log lines. Collect the files from each host, or use a `syslog+tls://` sink.

## Behind a load balancer

Set `--public-url` to the URL that clients use, for example `https://dustpan.example`. Without it, each instance makes absolute URLs from the host of each request. Console sign-in needs the public URL.

Set `--trusted-proxies` (`DUSTPAN_TRUSTED_PROXIES`) to the addresses of the load balancer. The server then reads the client address from the forwarding header in `--trusted-proxy-header`. Without it, the audit log and the IP allowlists see the address of the load balancer.

Send health checks to the readiness probe. The [observe guide](observe.md) gives the probe paths.

## Limits

- SQLite holds one instance only. Do not start two processes on one SQLite file.
- At start, an instance can mark a backup or a restore as failed while another instance runs it. Start or restart an instance only when no backup or restore runs.
