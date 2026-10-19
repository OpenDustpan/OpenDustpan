# Configuration

`opendustpan serve` reads each setting from a flag, from an environment
variable or from the configuration file. A flag wins over its variable, and
a variable wins over the file. A variable that is empty or holds only
spaces is not set, so the next layer wins. `opendustpan serve --help` prints
the same list.

## Server settings

| Flag | Environment | File key | Default |
| --- | --- | --- | --- |
| `--config` | `DUSTPAN_CONFIG` | (none) | (none) |
| `--database` | `DUSTPAN_DATABASE` | `database` | `sqlite:dustpan.db` (or `postgres://...`) |
| `--region` | `DUSTPAN_REGION` | `region` | `local` |
| `--listen` | `DUSTPAN_LISTEN` | `listen` | `:8080` |
| `--console-dir` | `DUSTPAN_CONSOLE_DIR` | `console-dir` | (none) |
| `--allowed-origins` | `DUSTPAN_ALLOWED_ORIGINS` | `allowed-origins` | (none). A comma-separated list. Each origin is a scheme (`http` or `https`), a host and an optional port, in lower case with an ASCII (punycode) host. It has no default port, path, query or fragment. The server refuses `null` and `*`. A listed origin gets CORS headers on `/api/`, with `Access-Control-Expose-Headers: Retry-After` on answers to requests. |
| `--tls-cert` | `DUSTPAN_TLS_CERT` | `tls-cert` | (none, with `--tls-key`) |
| `--tls-key` | `DUSTPAN_TLS_KEY` | `tls-key` | (none, with `--tls-cert`) |
| `--public-url` | `DUSTPAN_PUBLIC_URL` | `public-url` | (none) |
| `--trusted-proxies` | `DUSTPAN_TRUSTED_PROXIES` | `trusted-proxies` | (none: the server uses the address of the TCP peer only) |
| `--trusted-proxy-header` | `DUSTPAN_TRUSTED_PROXY_HEADER` | `trusted-proxy-header` | `x-forwarded-for` (or `forwarded`) |
| `--ip-allowlists` | `DUSTPAN_IP_ALLOWLISTS` | `ip-allowlists` | (none: off). Set `true` to turn the feature on. A namespace opts in with its own list (ADR 55). The server needs `--trusted-proxies` and `--trusted-proxy-header` when a proxy sits in front of it, or every client reads as the address of the proxy. See Recovery in docs/explanation/security-model.md for the recovery path. |
| `--ip-allowlists-include-bootstrap` | `DUSTPAN_IP_ALLOWLISTS_INCLUDE_BOOTSTRAP` | `ip-allowlists-include-bootstrap` | (none: the bootstrap admin token is exempt, so an administrator cannot lock themselves out). Set `true` to apply the lists to it as well. See Recovery in docs/explanation/security-model.md for the recovery path. |
| `--log-level` | `DUSTPAN_LOG_LEVEL` | `log-level` | `info` (or `debug`, `warn`, `error`) |
| `--log-format` | `DUSTPAN_LOG_FORMAT` | `log-format` | `json` (or `text`) |
| `--log-sink` | `DUSTPAN_LOG_SINK` | `log-sink` | (none: stderr). For example `stderr syslog+tls://logs.example:6514` |
| `--blob-dir` | `DUSTPAN_BLOB_DIR` | `blob-dir` | `blobs` next to the SQLite file, or `./blobs` for PostgreSQL |
| `--blob-store` | `DUSTPAN_BLOB_STORE` | `blob-store` | `fs` (the directory in `--blob-dir`), or `s3://<bucket>[/<prefix>]` |
| `--blob-cache-bytes` | `DUSTPAN_BLOB_CACHE_BYTES` | `blob-cache-bytes` | `0`: no cache. The cache keeps only blobs that are written once: OpenTofu state versions. One blob takes at most one eighth of the cache |
| `--s3-endpoint` | `DUSTPAN_S3_ENDPOINT` | `s3-endpoint` | (none: AWS) |
| `--s3-region` | `DUSTPAN_S3_REGION` | `s3-region` | `us-east-1` |
| `--backup-target` | `DUSTPAN_BACKUP_TARGET` | `backup-target` | (none: no backups), or `dir:/path` or `s3://<bucket>[/<prefix>]` |
| `--key-encryption-key-file` | `DUSTPAN_KEY_ENCRYPTION_KEY_FILE` | `key-encryption-key-file` | `dustpan.key` next to the SQLite file, or `./dustpan.key` for PostgreSQL |
| `--generate-key-encryption-key` | `DUSTPAN_GENERATE_KEY_ENCRYPTION_KEY` | `generate-key-encryption-key` | (none: ask on a terminal, else refuse). Set `true` to make the key file with no prompt |
| `--max-checkpoint-bytes` | `DUSTPAN_MAX_CHECKPOINT_BYTES` | `max-checkpoint-bytes` | `0`: 32 MiB, the largest Pulumi checkpoint |
| `--checkpoint-delta-cutoff` | `DUSTPAN_CHECKPOINT_DELTA_CUTOFF` | `checkpoint-delta-cutoff` | `1048576` (1 MiB). `0` turns delta checkpoint uploads off |
| `--disable-pulumi-journaling` | `DUSTPAN_DISABLE_PULUMI_JOURNALING` | `disable-pulumi-journaling` | (none: journaling on). Set `true` to make the Pulumi CLI save checkpoints |
| `--max-secret-batch-bytes` | `DUSTPAN_MAX_SECRET_BATCH_BYTES` | `max-secret-batch-bytes` | `0`: 32 MiB, the largest batch of Pulumi secrets |
| `--max-state-bytes` | `DUSTPAN_MAX_STATE_BYTES` | `max-state-bytes` | `0`: 32 MiB, the largest OpenTofu state |
| `--max-body-bytes-in-flight` | `DUSTPAN_MAX_BODY_BYTES_IN_FLIGHT` | `max-body-bytes-in-flight` | `0`: 128 MiB of large request bodies of the Pulumi CLI and OpenTofu at the same time. The budget covers OpenTofu state uploads as well. An upload whose OpenTofu state has a large outputs document asks for more budget as the document grows. One sealed state with 16 MiB of outputs takes about 72 MiB of the budget. Installations with large outputs should raise this value. An upload that can never fit answers 413. |
| `--audit-chain` | `DUSTPAN_AUDIT_CHAIN` | `audit-chain` | `on`. `off` turns the chain off. A change starts the server in maintenance mode until a person runs `opendustpan audit-chain approve` |
| `--audit-checkpoints` | `DUSTPAN_AUDIT_CHECKPOINTS` | `audit-checkpoints` | (none: off). Set `true` to sign checkpoints. Needs `--audit-chain=on` and a signing key file |
| `--audit-signing-key-file` | `DUSTPAN_AUDIT_SIGNING_KEY_FILE` | `audit-signing-key-file` | (none) |
| `--audit-verify-keys-file` | `DUSTPAN_AUDIT_VERIFY_KEYS_FILE` | `audit-verify-keys-file` | (none) |
| `--soft-delete-retention` | `DUSTPAN_SOFT_DELETE_RETENTION` | `soft-delete-retention` | `720h` (30 days, ADR 47). A Go duration of at least `1h`, for example `168h`. A delete records the end of its retention, so a change applies to later deletes |
| `--audit-sink` | `DUSTPAN_AUDIT_SINK` | `audit-sink` | (none: no sink). For example `siem=syslog+tls://logs.example:6514 blob:audit` |
| `--oidc-issuer` | `DUSTPAN_OIDC_ISSUER` | `oidc-issuer` | (none: no console sign-in). The identity of a person does not hold the issuer. To use another issuer, also set a new `--oidc-provider`. Else a person of the new issuer gets the principal of a person of the old issuer with the same `sub`. |
| `--oidc-provider` | `DUSTPAN_OIDC_PROVIDER` | `oidc-provider` | (none). The ID is part of the identity of each person who signs in: the subject `<provider ID>:<sub>`. A principal with that subject, a local one too, is the principal of that person. Never change the ID after the first sign-in. A new issuer needs a new ID. |
| `--oidc-display-name` | `DUSTPAN_OIDC_DISPLAY_NAME` | `oidc-display-name` | (the provider ID) |
| `--oidc-client-id` | `DUSTPAN_OIDC_CLIENT_ID` | `oidc-client-id` | (none) |
| `--oidc-scopes` | `DUSTPAN_OIDC_SCOPES` | `oidc-scopes` | `profile,email` |
| `--oidc-no-auto-create` | `DUSTPAN_OIDC_NO_AUTO_CREATE` | `oidc-no-auto-create` | false |

With journaling on, the server refuses to complete an update with
`succeeded` while the server has not stored a journal batch of the update. The
server does not detect a missing last batch: if the last journal batch of an
update is never sent, complete does not refuse it. Cancel the update when its
batches stop arriving.

## Configuration file

`--config` (`DUSTPAN_CONFIG`) names one YAML file. Each key in the file is
the file key of a setting in the table above, or `admin-token-file` for
`DUSTPAN_ADMIN_TOKEN_FILE`. A secret is never in the file: the file can
only name the file that holds it.

The server refuses a file that:

- is larger than 64 KiB,
- has an anchor or an alias,
- has a key two times,
- has a key that no setting declares,
- has a value of the wrong type.

A bool is only `true` or `false`. A string setting takes only a string, so
`no` stays the string `no`. An int setting takes only a decimal integer of
0 or more. A refused file stops the start. The error names the key and the
rule, never the value.

A string value that YAML reads as a number, a bool or a date needs quotes,
for example `"8080"`, `"true"` or `"2026-10-14"`. The JSON shape of
the example below quotes each string.

The file refuses a `database` URL with a password. Set such a URL in
`DUSTPAN_DATABASE`, because a secret comes only from a variable or from a
file that a `*_FILE` key names. The `--database` flag refuses a URL with a
password too: `serve` stops with an error before it opens the database.
Use `DUSTPAN_DATABASE` instead.

At start, the server writes one INFO line for each setting, with its key,
its value and its layer: `flag`, `env`, `file` or `default`. When more than
one of `flag`, `env` and `file` sets a key, the server writes one WARN line
for that key, also when the values are equal. For a sensitive setting, a
line shows only `set` or `not set`, never the value. The sensitive settings
are the database URL and the secrets. Each other URL setting refuses a URL
with a user name or a password, so the log can show its value.

At the log level `warn` or `error`, the server does not write these INFO
lines: the level that the operator sets wins. The start audit entry (see
"Reload") records the whole configuration at each level.

### Reload

The server reloads the file on SIGHUP, and when its content changes. It
reads the file every 10 seconds and compares the SHA-256 of the content.
A flag and a variable change only at a restart. A reload checks the whole
file with the rules of the start. When the file fails a rule, the server
keeps the old configuration and writes one ERROR line that names the key
and the rule. The server writes one INFO line for each changed value, and
one audit entry with the channel `config-file` for the reload, with each
changed key. A changed setting takes effect at the next start. The metrics `dustpan_config_reloads_total` and
`dustpan_config_last_reload_success` count the reloads.

A ConfigMap volume mounted with `subPath` never updates. Mount the whole
volume, so the poll sees the change.

At each start, the server also writes one audit entry with the channel
`startup` and each setting, with the rule for sensitive settings. The
[audit guide](../guides/audit-log.md) shows these entries.

This example uses the JSON shape with `#` comments. That text is valid
YAML.

```yaml
# opendustpan serve --config /etc/opendustpan/config.yaml
{
  # The region that this server is authoritative for.
  "region": "eu-west",
  "listen": ":8443",
  "public-url": "https://dustpan.example",
  "tls-cert": "/etc/opendustpan/tls.crt",
  "tls-key": "/etc/opendustpan/tls.key",
  "log-level": "info",
  "log-format": "json",
  # A bool is only true or false.
  "disable-pulumi-journaling": false,
  # An int is a decimal integer. 0 means the built-in limit.
  "max-state-bytes": 0,
  # The file holds the name of the token file, never a token.
  "admin-token-file": "/run/secrets/dustpan-admin-tokens"
}
```

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
| `DUSTPAN_AUDIT_WEBHOOK_SECRET` | The key of the HMAC-SHA256 signature of each request of a `webhook:` audit sink. Required when `--audit-sink` names a webhook and you set no secret file. |
| `DUSTPAN_AUDIT_WEBHOOK_SECRET_FILE` | A file that holds the webhook secret. Only its owner can read it. When you set both, `DUSTPAN_AUDIT_WEBHOOK_SECRET` wins. |
| `DUSTPAN_LOG_WEBHOOK_SECRET` | The key of the HMAC-SHA256 signature of each request of a `webhook:` log sink. Required when `--log-sink` names a webhook and you set no secret file. |
| `DUSTPAN_LOG_WEBHOOK_SECRET_FILE` | A file that holds the log webhook secret. Only its owner can read it. When you set both, `DUSTPAN_LOG_WEBHOOK_SECRET` wins. |

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

## Variables of a step

The launcher sets these variables for the process of each step of a job. The
server does not read them, and no flag or file key sets them. A step cannot
set a variable with the prefix `DUSTPAN_`: the launcher refuses it.

| Environment | Value |
| --- | --- |
| `DUSTPAN_JOB_ID` | The ID of the job that the step belongs to. |
| `DUSTPAN_PHASE` | The name of the phase of the job that the step belongs to. |
| `DUSTPAN_STEP` | The name of the step. |
| `DUSTPAN_UNIT` | The unit that the job runs on. |
| `DUSTPAN_NAMESPACE` | The namespace of the unit. |
| `DUSTPAN_COMMIT` | The commit of the source that the job runs. |
| `DUSTPAN_SERVER_URL` | The URL of the server that the step calls. |
| `DUSTPAN_SWEEP_REPORT` | Set for the command of a cloud sweep only: the path of the JSON Lines file that the command writes its report to. The launcher uploads the file when the command ends. |

## Client commands

`opendustpan namespace create`, `opendustpan backup`, `opendustpan backup
check`, `opendustpan upgrade check`, `opendustpan restore` and the
`opendustpan audit-chain` commands call a running server. They read the
server URL from `--server` or
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
| `opendustpan audit-chain status` | `--server`, `--ca-file` |
| `opendustpan audit-chain approve` | `--server`, `--ca-file` |
| `opendustpan health` | `--listen`, `--tls-cert` |

`opendustpan health` reads `--listen`, `--tls-cert` and their variables, as
`serve` does. The [observe guide](../guides/observe.md) shows the probes.
The [backup guide](../guides/backup-restore-upgrade.md) shows the flags of
the backup commands. The [audit log guide](../guides/audit-log.md) shows
the audit chain commands.

## Host command

`opendustpan host create-temp-admin` opens the database itself, as `serve`
does, and makes a temporary administrator for one named person. It reads
these settings. The [host recovery guide](../guides/host-recovery.md) shows
the steps.

| Flag | Environment | Default |
| --- | --- | --- |
| `--database` | `DUSTPAN_DATABASE` | `sqlite:dustpan.db` (or `postgres://...`) |
| `--reason` | (none) | (none) |
| `--recovery-mode` | `DUSTPAN_RECOVERY_MODE` | `bootstrap`: recovery through the bootstrap administrator or the host command. `signed-request`: recovery with a request signed with an offline operator key. `none`: no break-glass, the operator restores from a backup, and the host command refuses to run |
| (none) | `DUSTPAN_DISABLE_HOST_RECOVERY` | (none: the host command is on). Set `true` or `1` in the environment of the operator account to turn it off. `false`, `0` and an empty value keep it on. The host command refuses any other value |
