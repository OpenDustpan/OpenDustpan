# Configuration

`opendustpan serve` reads each setting from a flag or from an environment
variable. A flag wins over its variable. `opendustpan serve --help` prints the
same list.

## Server settings

| Flag | Environment | Default |
| --- | --- | --- |
| `--database` | `DUSTPAN_DATABASE` | `sqlite:dustpan.db` (or `postgres://...`) |
| `--region` | `DUSTPAN_REGION` | `local` |
| `--listen` | `DUSTPAN_LISTEN` | `:8080` |
| `--console-dir` | `DUSTPAN_CONSOLE_DIR` | (none) |
| `--allowed-origins` | `DUSTPAN_ALLOWED_ORIGINS` | (none) |
| `--tls-cert` | `DUSTPAN_TLS_CERT` | (none, with `--tls-key`) |
| `--tls-key` | `DUSTPAN_TLS_KEY` | (none, with `--tls-cert`) |
| `--public-url` | `DUSTPAN_PUBLIC_URL` | (none) |
| `--log-level` | `DUSTPAN_LOG_LEVEL` | `info` (or `debug`, `warn`, `error`) |
| `--log-format` | `DUSTPAN_LOG_FORMAT` | `json` (or `text`) |
| `--blob-dir` | `DUSTPAN_BLOB_DIR` | `blobs` next to the SQLite file, or `./blobs` for PostgreSQL |
| `--blob-store` | `DUSTPAN_BLOB_STORE` | `fs` (the directory in `--blob-dir`), or `s3://<bucket>[/<prefix>]` |
| `--s3-endpoint` | `DUSTPAN_S3_ENDPOINT` | (none: AWS) |
| `--s3-region` | `DUSTPAN_S3_REGION` | `us-east-1` |
| `--backup-target` | `DUSTPAN_BACKUP_TARGET` | (none: no backups), or `dir:/path` or `s3://<bucket>[/<prefix>]` |
| `--max-checkpoint-bytes` | `DUSTPAN_MAX_CHECKPOINT_BYTES` | `0`: 32 MiB, the largest Pulumi checkpoint |
| `--checkpoint-delta-cutoff` | `DUSTPAN_CHECKPOINT_DELTA_CUTOFF` | `1048576` (1 MiB). `0` turns delta checkpoint uploads off |
| `--max-secret-batch-bytes` | `DUSTPAN_MAX_SECRET_BATCH_BYTES` | `0`: 32 MiB, the largest batch of Pulumi secrets |
| `--max-state-bytes` | `DUSTPAN_MAX_STATE_BYTES` | `0`: 512 MiB, the largest OpenTofu state |
| `--max-body-bytes-in-flight` | `DUSTPAN_MAX_BODY_BYTES_IN_FLIGHT` | `0`: 128 MiB of large Pulumi request bodies at the same time |

## Secrets

No flag takes these values, because flags show in process lists.

| Environment | Value |
| --- | --- |
| `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN` | The S3 credentials. |
| `DUSTPAN_ADMIN_TOKEN` | Required when you set no token file. At least 32 bytes. Comma-separated tokens sign in as distinct administrators: `admin`, `admin-2`, and so on. The server clears the variable from its environment after the read. |
| `DUSTPAN_ADMIN_TOKEN_FILE` | A file that holds the tokens, one per line or comma-separated. Only its owner can read it. When you set both, `DUSTPAN_ADMIN_TOKEN` wins. |
| `DUSTPAN_KEY_ENCRYPTION_KEY` | The base64 text of 32 random bytes. It can stay unset while the database holds no secrets. |
| `DUSTPAN_KEY_ENCRYPTION_KEY_FILE` | A file that holds that value. Only its owner can read it. |

Make a key-encryption key with this command:

```sh
openssl rand -base64 32
```

## Client commands

`opendustpan namespace create`, `opendustpan backup`, `opendustpan backup
check`, `opendustpan upgrade check` and `opendustpan restore` call a running
server. They read the server URL from `--server` or
`DUSTPAN_SERVER`, and the token from `DUSTPAN_ADMIN_TOKEN` or
`DUSTPAN_ADMIN_TOKEN_FILE`. `--ca-file` adds a PEM bundle of CA certificates
to the system store.

`opendustpan health` reads `--listen`, `--tls-cert` and their variables, as
`serve` does. The [observe guide](../guides/observe.md) shows the probes.
The [backup guide](../guides/backup-restore-upgrade.md) shows the flags of
the backup commands.
