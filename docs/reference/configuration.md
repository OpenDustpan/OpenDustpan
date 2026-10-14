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
| `--key-encryption-key-file` | `DUSTPAN_KEY_ENCRYPTION_KEY_FILE` | `dustpan.key` next to the SQLite file, or `./dustpan.key` for PostgreSQL |
| `--generate-key-encryption-key` | `DUSTPAN_GENERATE_KEY_ENCRYPTION_KEY` | (none: ask on a terminal, else refuse). Set `true` to make the key file with no prompt |
| `--max-checkpoint-bytes` | `DUSTPAN_MAX_CHECKPOINT_BYTES` | `0`: 32 MiB, the largest Pulumi checkpoint |
| `--checkpoint-delta-cutoff` | `DUSTPAN_CHECKPOINT_DELTA_CUTOFF` | `1048576` (1 MiB). `0` turns delta checkpoint uploads off |
| `--disable-pulumi-journaling` | `DUSTPAN_DISABLE_PULUMI_JOURNALING` | (none: journaling on). Set `true` to make the Pulumi CLI save checkpoints |
| `--max-secret-batch-bytes` | `DUSTPAN_MAX_SECRET_BATCH_BYTES` | `0`: 32 MiB, the largest batch of Pulumi secrets |
| `--max-state-bytes` | `DUSTPAN_MAX_STATE_BYTES` | `0`: 32 MiB, the largest OpenTofu state |
| `--max-body-bytes-in-flight` | `DUSTPAN_MAX_BODY_BYTES_IN_FLIGHT` | `0`: 128 MiB of large request bodies of the Pulumi CLI and OpenTofu at the same time. The budget covers OpenTofu state uploads as well. An upload whose OpenTofu state has a large outputs document asks for more budget as the document grows. One sealed state with 16 MiB of outputs takes about 72 MiB of the budget. Installations with large outputs should raise this value. An upload that can never fit answers 413. |
| `--audit-chain` | `DUSTPAN_AUDIT_CHAIN` | `auto`: on when the audit log holds chained entries, else off. `on` or `off` decide alone |

## Secrets

No flag takes these values, because flags show in process lists.

| Environment | Value |
| --- | --- |
| `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN` | The S3 credentials. |
| `DUSTPAN_ADMIN_TOKEN` | Required when you set no token file. At least 32 bytes. Comma-separated tokens sign in as distinct administrators: `admin`, `admin-2`, and so on. Give each CI job or runner that writes OpenTofu state its own token, because the server checks the lock holder by subject. Each token is a full administrator, and a change to the list needs a restart. The server clears the variable from its environment after the read. |
| `DUSTPAN_ADMIN_TOKEN_FILE` | A file that holds the tokens, one per line or comma-separated. Only its owner can read it. When you set both, `DUSTPAN_ADMIN_TOKEN` wins. |
| `DUSTPAN_KEY_ENCRYPTION_KEY` | The base64 text of 32 random bytes. It can stay unset while the database holds no secrets. |
| `DUSTPAN_KEY_ENCRYPTION_KEY_FILE` | A file that holds that value. Only its owner can read it. |
| `DUSTPAN_GENERATE_KEY_ENCRYPTION_KEY` | Set `true` to make the key file with no prompt when no key exists. The server never overwrites a file that exists. |

Make a key-encryption key with this command:

```sh
openssl rand -base64 32
```

Without the variable, the server uses the key file in
`--key-encryption-key-file` (`DUSTPAN_KEY_ENCRYPTION_KEY_FILE`), by default
`dustpan.key` next to the SQLite database file, or `./dustpan.key` with
PostgreSQL. On a terminal, `serve` asks for an explicit yes before it makes
the file. Without a terminal, it makes the file only with
`--generate-key-encryption-key`
(`DUSTPAN_GENERATE_KEY_ENCRYPTION_KEY=true`). Without the prompt or the
opt-in, `serve` stops before it opens the database and names each setting
that helps. The server never makes a key silently. A made file holds 32
random bytes in base64 with owner-only mode. Each start logs the path of the
key file in use, never the key. An operator who loses the key file loses
every secret that the key encrypts, and nobody can recover them. A key file
next to the data does not protect a backup of that data.

## Client commands

`opendustpan namespace create`, `opendustpan backup`, `opendustpan backup
check`, `opendustpan upgrade check` and `opendustpan restore` call a running
server. They read the server URL from `--server` or
`DUSTPAN_SERVER`, and the token from `DUSTPAN_ADMIN_TOKEN` or
`DUSTPAN_ADMIN_TOKEN_FILE`. `--ca-file` adds a PEM bundle of CA certificates
to the system store.

Each client command takes only the flags in its row:

| Command | Flags |
| --- | --- |
| `opendustpan namespace create <name>` | `--server`, `--ca-file` |
| `opendustpan backup` | `--server`, `--ca-file` |
| `opendustpan backup check --backup <id>` | `--server`, `--ca-file`, `--backup` |
| `opendustpan restore --backup <id>` | `--server`, `--ca-file`, `--backup`, `--check-only`, `--force` |
| `opendustpan upgrade check` | `--server`, `--ca-file` |
| `opendustpan health` | `--listen`, `--tls-cert` |

`opendustpan health` reads `--listen`, `--tls-cert` and their variables, as
`serve` does. The [observe guide](../guides/observe.md) shows the probes.
The [backup guide](../guides/backup-restore-upgrade.md) shows the flags of
the backup commands.
