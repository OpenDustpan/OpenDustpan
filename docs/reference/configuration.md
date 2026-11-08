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
| `--database` | `DUSTPAN_DATABASE` | `database` | (none: `serve` stops without it). `sqlite:<path>` or `postgres://...` for PostgreSQL 18 or later. The server checks the failover settings of the URL, for example `target_session_attrs` and `pool_max_conn_lifetime`. It sets a default for each one that the URL does not name: see [Database settings for a failover](../guides/several-instances.md#database-settings-for-a-failover) |
| `--installation` | `DUSTPAN_INSTALLATION` | `installation` | `local` |
| `--components` | `DUSTPAN_COMPONENTS` | `components` | `all` (or a comma-separated list of `api` and `worker`). `api` runs each HTTP route and starts durable operations, but never claims one. `worker` runs the durable operations, the schedules, the audit shipper and the audit chain check. A process that runs `worker` without `api` answers 404 on each route except the probes and the metrics route. An unknown name stops the start. See the several instances guide (docs/guides/several-instances.md) |
| `--listen` | `DUSTPAN_LISTEN` | `listen` | `:8080` |
| `--console-dir` | `DUSTPAN_CONSOLE_DIR` | `console-dir` | (none) |
| `--allowed-origins` | `DUSTPAN_ALLOWED_ORIGINS` | `allowed-origins` | (none). A comma-separated list. Each origin is a scheme (`http` or `https`), a host and an optional port, in lower case with an ASCII (punycode) host. The host is an IP address or a DNS name of letters, digits and hyphens. The port is a number from 1 to 65535. It has no default port, user information, path, query or fragment. The server refuses `null` and `*`. A listed origin gets CORS headers on `/api/`, with `Access-Control-Expose-Headers: Retry-After, ETag, Idempotent-Replayed` on answers to requests. |
| `--console-content-type-options` | `DUSTPAN_CONSOLE_CONTENT_TYPE_OPTIONS` | `console-content-type-options` | (none: the server sends `nosniff`). A value is sent as given. `off` (in any case) sends no header. It applies to the console files only. |
| `--console-frame-options` | `DUSTPAN_CONSOLE_FRAME_OPTIONS` | `console-frame-options` | (none: the server sends `DENY`). A value is sent as given. `off` (in any case) sends no header. It applies to the console files only. |
| `--console-csp` | `DUSTPAN_CONSOLE_CSP` | `console-csp` | (none: the server sends `default-src 'self'; connect-src 'self' <each allowed origin>; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'`). `connect-src` follows `--allowed-origins`: a console that calls another API origin needs that origin listed there. A value is sent as given. `off` (in any case) sends no header. It applies to the console files only. |
| `--console-hsts` | `DUSTPAN_CONSOLE_HSTS` | `console-hsts` | (none: the server sends `max-age=31536000` when it serves TLS, and nothing without TLS). A value is sent as given. `off` (in any case) sends no header. The server never sends it without `--tls-cert`. It applies to the console files only. |
| `--tls-cert` | `DUSTPAN_TLS_CERT` | `tls-cert` | (none, with `--tls-key`) |
| `--tls-key` | `DUSTPAN_TLS_KEY` | `tls-key` | (none, with `--tls-cert`) |
| `--public-url` | `DUSTPAN_PUBLIC_URL` | `public-url` | (none) |
| `--issuer-base-url` | `DUSTPAN_ISSUER_BASE_URL` | `issuer-base-url` | (none: the issuer uses `--public-url`. With neither setting, the issuer is off) |
| `--trusted-proxies` | `DUSTPAN_TRUSTED_PROXIES` | `trusted-proxies` | (none: the server uses the address of the TCP peer only) |
| `--trusted-proxy-header` | `DUSTPAN_TRUSTED_PROXY_HEADER` | `trusted-proxy-header` | `x-forwarded-for` (or `forwarded`) |
| `--ip-allowlists` | `DUSTPAN_IP_ALLOWLISTS` | `ip-allowlists` | (none: off). Set `true` to turn the feature on. A namespace opts in with its own list. The server needs `--trusted-proxies` and `--trusted-proxy-header` when a proxy sits in front of it, or every client reads as the address of the proxy. See Recovery in docs/explanation/security-model.md for the recovery path. |
| `--ip-allowlists-include-bootstrap` | `DUSTPAN_IP_ALLOWLISTS_INCLUDE_BOOTSTRAP` | `ip-allowlists-include-bootstrap` | (none: the bootstrap admin token is exempt, so an administrator cannot lock themselves out). Set `true` to apply the lists to it as well. See Recovery in docs/explanation/security-model.md for the recovery path. |
| `--git-host-allowed-ranges` | `DUSTPAN_GIT_HOST_ALLOWED_RANGES` | `git-host-allowed-ranges` | (none: a connection reaches public addresses only). Set the range of a GitHub Enterprise Server on a private network, for example `10.20.0.0/16`. A range opens private-network addresses only. These are RFC 1918 and unique local addresses. An IPv4 range has at least 8 bits, and an IPv6 range has at least 32 bits. The server always refuses loopback, link-local, cloud metadata, unspecified and multicast addresses, also inside a range. It checks this when a person sets an address and at each call. |
| `--allow-plain-connections` | `DUSTPAN_ALLOW_PLAIN_CONNECTIONS` | `egress.allowPlainConnections` | (none: off). Set `true` only in a trusted network. An LDAP bind or an SMTP login then sends its password with no encryption. The settings resource also holds it, and a value from a flag, a variable or this file locks it |
| `--branding-preset` | `DUSTPAN_BRANDING_PRESET` | `branding.preset` | (none: the console shows its standard palette). A name of lower-case letters, digits and hyphens, at most 32 characters. The settings resource also holds it, and a value from a flag, a variable or this file locks it |
| `--log-level` | `DUSTPAN_LOG_LEVEL` | `log-level` | `info` (or `debug`, `warn`, `error`) |
| `--log-format` | `DUSTPAN_LOG_FORMAT` | `log-format` | `json` (or `text`) |
| `--log-sink` | `DUSTPAN_LOG_SINK` | `log-sink` | (none: stderr). For example `stderr syslog+tls://logs.example:6514` |
| `--blob-dir` | `DUSTPAN_BLOB_DIR` | `blob-dir` | `blobs` next to the SQLite file, or `./blobs` for PostgreSQL |
| `--blob-store` | `DUSTPAN_BLOB_STORE` | `blob-store` | `fs` (the directory in `--blob-dir`), or `s3://<bucket>[/<prefix>]` |
| `--blob-cache-bytes` | `DUSTPAN_BLOB_CACHE_BYTES` | `blob-cache-bytes` | `0`: no cache. The cache keeps only blobs that are written once: OpenTofu state versions. One blob takes at most one eighth of the cache |
| `--s3-endpoint` | `DUSTPAN_S3_ENDPOINT` | `s3-endpoint` | (none: AWS) |
| `--s3-region` | `DUSTPAN_S3_REGION` | `s3-region` | `us-east-1` |
| `--backup-target` | `DUSTPAN_BACKUP_TARGET` | `backup-target` | (none: no backups), or `dir:/path` or `s3://<bucket>[/<prefix>]` |
| `--unsafe-allow-restoring-dev-installation-backups-into-production` | (none) | (none) | false. Only the flag sets it. No variable and no configuration file key sets it. The server writes one warning to the log at each start with the flag. Without it, a production installation refuses to restore a backup of a dev installation. A restore of any backup deletes the dev users and their data from the staged data |
| `--key-encryption-key-file` | `DUSTPAN_KEY_ENCRYPTION_KEY_FILE` | `key-encryption-key-file` | (none: `serve` stops without a key). Make a file with `opendustpan key generate --out <path>` |
| `--max-checkpoint-bytes` | `DUSTPAN_MAX_CHECKPOINT_BYTES` | `max-checkpoint-bytes` | `0`: 32 MiB, the largest Pulumi checkpoint |
| `--checkpoint-delta-cutoff` | `DUSTPAN_CHECKPOINT_DELTA_CUTOFF` | `checkpoint-delta-cutoff` | `1048576` (1 MiB). `0` turns delta checkpoint uploads off |
| `--disable-pulumi-journaling` | `DUSTPAN_DISABLE_PULUMI_JOURNALING` | `disable-pulumi-journaling` | (none: journaling on). Set `true` to make the Pulumi CLI save checkpoints |
| `--max-secret-batch-bytes` | `DUSTPAN_MAX_SECRET_BATCH_BYTES` | `max-secret-batch-bytes` | `0`: 32 MiB, the largest batch of Pulumi secrets |
| `--max-state-bytes` | `DUSTPAN_MAX_STATE_BYTES` | `max-state-bytes` | `0`: 32 MiB, the largest OpenTofu state |
| `--max-body-bytes-in-flight` | `DUSTPAN_MAX_BODY_BYTES_IN_FLIGHT` | `max-body-bytes-in-flight` | `0`: 128 MiB of large request bodies of the Pulumi CLI and OpenTofu at the same time. The budget covers OpenTofu state uploads as well. An upload whose OpenTofu state has a large outputs document asks for more budget as the document grows. One encrypted state with 16 MiB of outputs takes about 72 MiB of the budget. Installations with large outputs should raise this value. An upload that can never fit answers 413. |
| `--audit-chain` | `DUSTPAN_AUDIT_CHAIN` | `audit-chain` | `on`. `off` turns the chain off. A change starts the server in maintenance mode until a person approves it in the console |
| `--audit-checkpoints` | `DUSTPAN_AUDIT_CHECKPOINTS` | `audit-checkpoints` | (none: off). Set `true` to sign checkpoints. Needs `--audit-chain=on` and a signing key file |
| `--audit-signing-key-file` | `DUSTPAN_AUDIT_SIGNING_KEY_FILE` | `audit-signing-key-file` | (none) |
| `--audit-verify-keys-file` | `DUSTPAN_AUDIT_VERIFY_KEYS_FILE` | `audit-verify-keys-file` | (none) |
| `--soft-delete-retention` | `DUSTPAN_SOFT_DELETE_RETENTION` | `soft-delete-retention` | `720h` (30 days). A Go duration of at least `1h`, for example `168h`. A delete records the end of its retention, so a change applies to later deletes |
| `--plan-retention` | `DUSTPAN_PLAN_RETENTION` | `plan-retention` | `9600h` (400 days). A Go duration from `24h` to `87600h`. Each plan records its end of retention when it is made, so a change applies to later plans |
| `--job-log-retention` | `DUSTPAN_JOB_LOG_RETENTION` | `job-log-retention` | `9600h` (400 days). A Go duration from `24h` to `87600h`. Each log version records its end of retention when it is made, so a change applies to later versions |
| `--audit-sink` | `DUSTPAN_AUDIT_SINK` | `audit-sink` | (none: no sink). For example `siem=syslog+tls://logs.example:6514 blob:audit` |
| `--destination-ports` | `DUSTPAN_DESTINATION_PORTS` | `destination-ports` | (none: port 443 only). For example `8443,9443` |
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
  # The name of this installation.
  "installation": "eu-west",
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
| `DUSTPAN_AUDIT_WEBHOOK_SECRET` | The key of the HMAC-SHA256 signature of each request of a `webhook:` audit sink. Required when `--audit-sink` names a webhook and you set no secret file. |
| `DUSTPAN_AUDIT_WEBHOOK_SECRET_FILE` | A file that holds the webhook secret. Only its owner can read it. When you set both, `DUSTPAN_AUDIT_WEBHOOK_SECRET` wins. |
| `DUSTPAN_LOG_WEBHOOK_SECRET` | The key of the HMAC-SHA256 signature of each request of a `webhook:` log sink. Required when `--log-sink` names a webhook and you set no secret file. |
| `DUSTPAN_LOG_WEBHOOK_SECRET_FILE` | A file that holds the log webhook secret. Only its owner can read it. When you set both, `DUSTPAN_LOG_WEBHOOK_SECRET` wins. |
| `DUSTPAN_AUDIT_OTLP_HEADERS` | Extra headers of each request of an `otlp:` audit sink, as `Name=Value` items separated by commas or line breaks. Percent-encode a value. Optional. |
| `DUSTPAN_AUDIT_OTLP_HEADERS_FILE` | A file that holds those headers. Only its owner can read it. When you set both, `DUSTPAN_AUDIT_OTLP_HEADERS` wins. |
| `DUSTPAN_LOG_OTLP_HEADERS` | Extra headers of each request of an `otlp:` log sink, in the same form. Optional. |
| `DUSTPAN_LOG_OTLP_HEADERS_FILE` | A file that holds those headers. Only its owner can read it. When you set both, `DUSTPAN_LOG_OTLP_HEADERS` wins. |

Make a key-encryption key with this command:

```sh
opendustpan key generate --out <path>
```

`serve` needs a key from the variable, or from the file in
`--key-encryption-key-file` (`DUSTPAN_KEY_ENCRYPTION_KEY_FILE`). It has
no default key file and makes no key. Without a key, `serve` stops before
it opens the database. Each start logs the path of the key file in use,
never the key. A key file in the directory of the SQLite file or of the
blobs makes `serve` log a warning, because a backup of the data holds that
key too. An operator who loses the key file loses every secret that the
key encrypts, and nobody can recover them.

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
| `DUSTPAN_HELPER_SOCKET` | Set for a tool step only: the path of the Unix socket on which the launcher answers its helpers for OpenTofu and git. The variable holds a path. |

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
| `opendustpan store check --target <blobs|backup>` | `--server`, `--ca-file`, `--target` |
| `opendustpan upgrade check` | `--server`, `--ca-file` |
| `opendustpan audit-chain status` | `--server`, `--ca-file` |
| `opendustpan audit-chain approve` | (none) |
| `opendustpan health` | `--listen`, `--tls-cert` |
| `opendustpan key generate` | `--out` |

`opendustpan key generate` calls no server and opens no database. It writes a
new key-encryption key file to the path in `--out` and refuses a file that
exists.

`opendustpan doctor` takes each flag of `serve`, and `--server` and `--ca-file`.
It reads them through the same layers, and calls a server only when you set
`--server`. The [doctor guide](../guides/doctor.md) lists the checks.

`opendustpan health` reads `--listen`, `--tls-cert` and their variables, as
`serve` does. The [observe guide](../guides/observe.md) shows the probes.
The [backup guide](../guides/backup-restore-upgrade.md) shows the flags of
the backup commands. The [audit log guide](../guides/audit-log.md) shows
the audit chain commands.

## Dev command

`opendustpan dev` runs a server for a trial or for CI, with no setup. It reads
each setting from a flag only. No variable of `serve` and no configuration
file sets a flag of `dev`, also when `serve` has a flag with the same name.
The [dev server guide](../guides/dev-server.md) shows the steps.

| Flag | Default |
| --- | --- |
| `--data-dir` | (none). The default is a directory for each user. On Linux and other Unix systems, it is `$XDG_DATA_HOME/opendustpan/dev`, or `~/.local/share/opendustpan/dev` when `XDG_DATA_HOME` is not set. On macOS, it is `~/Library/Application Support/opendustpan/dev`. On Windows, it is `%LocalAppData%\opendustpan\dev`. With `DUSTPAN_DATA_DIR` set, it is the directory `dev` in the one that the variable names. Not with `--ephemeral` |
| `--demo` | (none: no demo person). Each start sets a new password for the local user `demo`. The person gets the admin role on the namespaces `dev`, `demo-platform` and `demo-data`. It gets the viewer role on the installation |
| `--ephemeral` | (none: the data stays). A stop deletes the data, and with it the state that tracks the cloud resources of the trial. Not with `--data-dir` |
| `--listen` | `127.0.0.1:8080`. Another address adds a line to the banner and to the log. With no `--tls`, the line also says that the token and the sign-in code cross the network in plain text |
| `--tls` | (none: HTTP). The `cloud` block of OpenTofu needs it. The certificate lives 30 days |
| `--hostname` | (none). The certificate holds `localhost`, `127.0.0.1`, `::1`, the host of `--listen` when it is not a wildcard, the name of the host and each `--hostname`. Repeat the flag for each name |
| `--admin-token-file` | (none: dev makes a token and keeps it in the token file). Only its owner can read it |
| `--console-dir` | (none) |
| `--log-level` | `info` (or `debug`, `warn`, `error`) |
| `--log-format` | `json` (or `text`) |
| `--disable-pulumi-journaling` | (none: journaling on). Set `true` to make the Pulumi CLI save checkpoints |
| `--max-checkpoint-bytes` | `0`: 32 MiB, the largest Pulumi checkpoint |
| `--max-secret-batch-bytes` | `0`: 32 MiB, the largest batch of Pulumi secrets |
| `--max-state-bytes` | `0`: 32 MiB, the largest OpenTofu state |
| `--max-body-bytes-in-flight` | `0`: 128 MiB of large request bodies of the Pulumi CLI and OpenTofu at the same time. The budget covers OpenTofu state uploads as well. An upload whose OpenTofu state has a large outputs document asks for more budget as the document grows. One encrypted state with 16 MiB of outputs takes about 72 MiB of the budget. Installations with large outputs should raise this value. An upload that can never fit answers 413. |
| `--blob-cache-bytes` | `0`: no cache. The cache keeps only blobs that are written once: OpenTofu state versions. One blob takes at most one eighth of the cache |

`dev` reads only these two variables:

| Environment | Description |
| --- | --- |
| `DUSTPAN_DATA_DIR` | The parent of the data directory of `dev`. The data directory is `dev` in it. `--ephemeral` ignores it, and `--data-dir` wins over it. |
| `DUSTPAN_DEV_ADMIN_TOKEN` | The administrator token of `dev`, in place of a token that `dev` makes. At least 32 bytes. `dev` clears the variable from its environment after the read, and `dev.env` takes the token from the variable in the shell that loads it. |

At start, `dev` clears each variable of a secret setting of `serve` from its
environment, and never reads `DUSTPAN_ADMIN_TOKEN` or
`DUSTPAN_ADMIN_TOKEN_FILE`.

## Host command

`opendustpan host create-temp-admin` opens the database itself, as `serve`
does, and makes a temporary administrator for one named person. It reads
these settings. The [host recovery guide](../guides/host-recovery.md) shows
the steps.

| Flag | Environment | Default |
| --- | --- | --- |
| `--database` | `DUSTPAN_DATABASE` | (none: `serve` stops without it). `sqlite:<path>` or `postgres://...` for PostgreSQL 18 or later. The server checks the failover settings of the URL, for example `target_session_attrs` and `pool_max_conn_lifetime`. It sets a default for each one that the URL does not name: see [Database settings for a failover](../guides/several-instances.md#database-settings-for-a-failover) |
| `--reason` | (none) | (none) |
| `--recovery-mode` | `DUSTPAN_RECOVERY_MODE` | `bootstrap`: recovery through the bootstrap administrator or the host command. `signed-request`: recovery with a request signed with an offline operator key. `none`: no break-glass, the operator restores from a backup, and the host command refuses to run |
| (none) | `DUSTPAN_DISABLE_HOST_RECOVERY` | (none: the host command is on). Set `true` or `1` in the environment of the operator account to turn it off. `false`, `0` and an empty value keep it on. The host command refuses any other value |
