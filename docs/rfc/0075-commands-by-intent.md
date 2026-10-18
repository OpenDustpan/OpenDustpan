---
rfc: 75
title: "Commands by intent: a dev server with no setup, and a serve command that needs decisions"
status: draft
created: 2026-10-18
---

# RFC 75. Commands by intent: a dev server with no setup, and a serve command that needs decisions

Status: draft.

## Problem

The project owner asked for this RFC, relayed by the product manager on
2026-10-18. The owner wrote that the problem with a required
key-encryption key may be the command structure. Temporal has
`temporal server start-dev`, and the name makes it clear that the command is
not for production. The owner asked for commands by intent: the binary
starts with no setup for a trial and in CI, and the friction is where the
user must make a decision.

Today one command, `serve`, has two jobs that pull in opposite directions:

- **A trial.** A person who wants to try OpenDustpan for ten minutes must
  make an admin token and a key-encryption key before the first start. The
  README quickstart runs `openssl` two times. For OpenTofu, it runs
  `openssl` a third time to make a TLS certificate, writes a credentials
  file and sets `SSL_CERT_FILE`.
- **Production.** `serve` has defaults that suit a trial and are wrong for
  production: the database `sqlite:dustpan.db` in the working directory,
  the key file `dustpan.key` next to the database, and a prompt that makes
  that key file. A key file next to the data does not protect a backup of
  that data (`docs/reference/configuration.md`).

To make the trial possible without a terminal, ADR 41 decision 12 added
`--generate-key-encryption-key`. ADR 154 decision 10 (in review in pull
request 847) names this flag "the escape hatch for development", and adds a
warning on each start. So the same flag is a development aid and a
production risk, and the warning text tells the operator what to do for
production after the server started. `scripts/demo.sh` uses the flag. The
guide for several instances says not to use it on more than one instance,
because each instance then makes its own key and cannot read the secrets of
the others.

The cause is that one command serves two intents. A flag cannot fix that,
because a flag is a choice the user makes after they chose the command.

Facts from the code on `main` (commit `fcebecb`):

- The command table is in `cmd/opendustpan/commands.go`. Each row comes
  from `internal/cli`. `serve` is the only command that opens the database
  (ADR 39). Every other command is a client of the REST API.
- `serve` stops when no admin token is set (`DUSTPAN_ADMIN_TOKEN` or
  `DUSTPAN_ADMIN_TOKEN_FILE`).
- No tag shipped yet. The README says "No release exists yet". So ADR 73
  decision 4 applies to every command, flag and setting below: no alias and
  no shim for a shape that no tag shipped.

## The commands today

### Commands

| Command | Opens data | Intent | What it does |
| --- | --- | --- | --- |
| `serve` | yes | try out, run in production | Runs the server with the built-in adapters. |
| `health` | no | operate, debug | Probes the local server and prints `ready` on a 2xx answer. The container `HEALTHCHECK` runs it. |
| `namespace create <name>` | no | operate | Creates a namespace through the API. |
| `backup` | no | back up | Asks the server to back up the installation to its target. |
| `backup check --backup <id>` | no | back up | Asks the server to check a backup in its target. |
| `restore --backup <id>` | no | back up | Restores a backup from the target through the server, in maintenance mode. |
| `upgrade check` | no | migrate | Asks the server for its version and prints the upgrade verdict. |
| `audit-chain status` | no | operate | Shows the state of the audit chain and if a change waits. |
| `audit-chain approve` | no | operate | Approves a change of the audit chain setting. |
| `--version` | no | debug | Prints the binary version. |

No command today has the intent "try out" alone. No command has the intent
"migrate" for data from other backends: RFC 42 puts that in the API.

### Flags and settings of `serve`

Each setting has a flag, a `DUSTPAN_` environment variable and a key in the
configuration file, except where the table says so. "Intent" is the reason
a user sets it. "Trial" means that a trial needs no value.

| Flag | Intent | Default today | Trial |
| --- | --- | --- | --- |
| `--config` | run in production | none | no value |
| `--database` | run in production | `sqlite:dustpan.db` | no value |
| `--region` | run in production | `local` | no value |
| `--listen` | run in production | `:8080` (every interface) | no value |
| `--console-dir` | try out, run in production | none | optional |
| `--allowed-origins` | run in production | none | no value |
| `--tls-cert`, `--tls-key` | run in production | none | needed for OpenTofu |
| `--public-url` | run in production | none | no value |
| `--trusted-proxies`, `--trusted-proxy-header` | run in production | none, `x-forwarded-for` | no value |
| `--ip-allowlists`, `--ip-allowlists-include-bootstrap` | run in production | off | no value |
| `--log-level`, `--log-format`, `--log-sink` | operate, debug | `info`, `json`, stderr | no value |
| `--blob-dir`, `--blob-store`, `--blob-cache-bytes` | run in production | `blobs` next to the database, `fs`, `0` | no value |
| `--s3-endpoint`, `--s3-region` | run in production | AWS, `us-east-1` | no value |
| `--backup-target` | back up | none | no value |
| `--key-encryption-key-file` | run in production | `dustpan.key` next to the database | no value |
| `--generate-key-encryption-key` | try out | off: prompt on a terminal, else refuse | needed without a terminal |
| `--max-checkpoint-bytes`, `--checkpoint-delta-cutoff` | operate | 32 MiB, 1 MiB | no value |
| `--disable-pulumi-journaling` | debug | journaling on | no value |
| `--max-secret-batch-bytes`, `--max-state-bytes`, `--max-body-bytes-in-flight` | operate | 32 MiB, 32 MiB, 128 MiB | no value |
| `--audit-chain`, `--audit-checkpoints` | run in production | `on`, off | no value |
| `--audit-signing-key-file`, `--audit-verify-keys-file` | run in production | none | no value |
| `--audit-sink` | run in production | none | no value |
| `--soft-delete-retention` | operate | `720h` | no value |
| `--oidc-issuer`, `--oidc-provider`, `--oidc-display-name`, `--oidc-client-id`, `--oidc-scopes`, `--oidc-no-auto-create` | run in production | none | no value |
| `DUSTPAN_ADMIN_TOKEN`, `DUSTPAN_ADMIN_TOKEN_FILE` (no flag) | try out, run in production | none: `serve` refuses to start | needed |
| `DUSTPAN_KEY_ENCRYPTION_KEY` (no flag) | run in production | none | one of the key sources is needed |
| `DUSTPAN_OIDC_CLIENT_SECRET`, `DUSTPAN_OIDC_CLIENT_SECRET_FILE` (no flag) | run in production | none | no value |
| `DUSTPAN_AUDIT_WEBHOOK_SECRET`, `DUSTPAN_LOG_WEBHOOK_SECRET` and their `_FILE` forms (no flag) | run in production | none | no value |
| `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN` (no flag) | run in production | none | no value |

Of 42 flags and 12 settings that only an environment variable sets, a trial
needs three: an admin token, a key, and for
OpenTofu a TLS certificate. Each of the three costs the trial user a step.
A production operator must decide 6 of the defaults again: the database,
the blob directory, the key file, the listen address, TLS and the backup
target. Today `serve` takes those decisions for the operator without a
word.

### Flags of the client commands

| Flag | Commands | Intent |
| --- | --- | --- |
| `--server` (`DUSTPAN_SERVER`) | every client command except `health` | operate |
| `--ca-file` | every client command except `health` | operate |
| `--backup <id>` | `backup check`, `restore` | back up |
| `--check-only` | `restore` | back up |
| `--force` | `restore` | back up |
| `--listen`, `--tls-cert` | `health` | operate, debug |

The client commands read the token from `DUSTPAN_ADMIN_TOKEN` or
`DUSTPAN_ADMIN_TOKEN_FILE`.

## Benchmarks

What similar servers do. Each row is from the public documentation of the
project. Check the current documentation before a decision depends on a
detail.

| Project | Trial command | What the trial command does | Production command | What production needs |
| --- | --- | --- | --- | --- |
| Temporal | `temporal server start-dev` | Binds to `localhost` by default. Keeps data in memory, or in a SQLite file with `--db-filename`. Starts the web UI. Creates the `default` namespace. `--ip` binds to another address. | The Temporal server, a separate binary and image | A database, a configuration file, schema setup |
| HashiCorp Vault | `vault server -dev` | Binds to `127.0.0.1:8200`. Keeps data in memory. Starts unsealed. Makes and prints a root token. Prints a warning that dev mode must not be used in production. | `vault server -config=<file>` | A configuration file with storage and a listener, then `vault operator init` to make the unseal keys |
| Keycloak | `kc.sh start-dev` | Turns on HTTP, turns off the strict host name check, uses a local database. Logs that it runs in development mode. | `kc.sh start` | Refuses to start without a host name setting and HTTPS, unless the operator turns each off by name |
| HashiCorp Consul, Nomad | `consul agent -dev`, `nomad agent -dev` | One process with every role, data in memory, bound to `127.0.0.1` | `agent -config=<file>` | A configuration file |
| HashiCorp Boundary | `boundary dev` | A built-in database in a container, made credentials, all services in one process | `boundary server -config=<file>` | A configuration file, a database, KMS settings |
| PostgreSQL container image | none | Not applicable | `docker run postgres` | Refuses to start without `POSTGRES_PASSWORD`. `POSTGRES_HOST_AUTH_METHOD=trust` is an escape hatch with a warning in the log. |
| MinIO | none | Not applicable | `minio server <dir>` | Starts with a known default user and password, and logs a warning |

Four lessons for OpenDustpan:

1. The projects that a security team trusts most (Vault, Keycloak, Temporal)
   give the trial its own command. The command name says "dev". None of
   them uses a flag on the production command for the trial.
2. A trial command binds to the loopback address by default and makes its
   own credentials.
3. The production command refuses to start without the decisions that it
   cannot take for the operator. The error names the setting.
4. The PostgreSQL image and our `--generate-key-encryption-key` use the same
   pattern: an escape hatch on the production path, with a warning. MinIO's
   default user is the same pattern with no opt-in. These are the patterns
   this RFC takes away.

One difference: Vault, Consul and Nomad keep dev data in memory only. So dev
data can never reach a production server. Temporal can keep dev data in a
file. OpenDustpan has two tools whose state a user might want to keep after
a trial, so this RFC lets dev data persist, and adds a marker so that
`serve` can refuse it (see "The dev marker").

## Options

### Option 1: do nothing

Keep `serve` with its defaults, the prompt and `--generate-key-encryption-key`.

- Cost: none now.
- Gives: nothing new. The trial keeps its three steps. A production server
  can still start with a key file next to its data, and the only signal is
  a warning line after the start. CI pipelines keep the escape hatch on the
  production command.

### Option 2: a profile flag on `serve`

`serve --profile=dev` or `serve --dev` turns on the trial defaults.

- Cost: small. One flag and a set of defaults.
- Gives: a trial in one command. But the trial is still the production
  command with a flag, as in the PostgreSQL image. The help of `serve` must
  explain both modes. Each new setting must say what it does in each mode.
  A configuration file can set the flag, so a production file can turn on
  dev by mistake.

### Option 3: a `dev` command and a `serve` command that needs decisions

Two server commands, one for each intent. `dev` takes every decision for the
user and says so. `serve` takes none of the decisions that only the operator
can take. A marker stored with the data keeps each command away from the
data of the other.

- Cost: medium. A second server command, a column in the `installation`
  table, a change to the README quickstart, the guides, the container image,
  `scripts/demo.sh` and the e2e setup. ADR 39 and ADR 41 get a status line.
- Gives: a trial with zero steps before the first start, also for OpenTofu
  and in CI. A `serve` command whose every start is a production start.
  The help of each command explains one intent.

### Option 4: an `init` command, then `serve`

`opendustpan init <dir>` writes a key file, a token file and a configuration
file. `serve --config <dir>/config.yaml` then starts.

- Cost: medium. A second set of defaults, now in a generated file.
- Gives: one place for the decisions. But `init` takes them for the user, as
  `serve` does today, and it puts the key next to the data unless the user
  names another path. A trial still needs two commands. Option 3 can add a
  small part of this later (`key generate` below) with no conflict.

## Proposal

Option 3. The tree below improves the starting point from the brief in four
places. Each change says why.

### The proposed tree

Commands in the usage text, in groups by intent:

```
Try out
  dev                      Run a local server for a trial or for CI. Not for production.

Run in production
  serve                    Run the server. Needs a key, a database and an admin token.
  key generate --out FILE  Write a new key-encryption key to a file. Opens no data.
  doctor                   Check the settings of serve before a production start.

Operate
  health                   Probe the local server.
  namespace create NAME    Create a namespace.
  audit-chain status       Show the state of the audit chain.
  audit-chain approve      Approve a change of the audit chain setting.

Back up
  backup                   Back up the installation.
  backup check             Check a backup in the target.
  restore                  Restore a backup.

Upgrade
  upgrade check            Print the upgrade verdict of the server.

  --version                Print the binary version.
```

The names of the existing client commands do not change. The groups are a
change to the usage text only. So the cost of the reorganization stays in
the two server commands, where the problem is.

Changes from the starting point in the brief:

1. **`key generate`.** The brief takes the key out of `serve` without a new
   way to make one. Today the documents send the operator to
   `openssl rand -base64 32`. A Windows host or a minimal container image
   might not have `openssl`. `key generate --out FILE` writes 32 random bytes
   in base64 with owner-only mode, refuses a file that exists, and prints
   the path and the warning that a lost key loses every secret. It opens no
   data, so ADR 39 holds. The operator chooses the path, so the decision
   about where the key lives stays with the operator.
2. **TLS in `dev`.** The brief does not name TLS. OpenTofu refuses plain
   HTTP for service discovery. A `dev` with no TLS gives OpenTofu users a
   trial with three manual steps. So `dev` makes a local certificate
   authority and a certificate for `localhost` and `127.0.0.1`. See open
   question 3.
3. **A file of client settings.** The brief prints an admin token. A CI job
   cannot read a banner easily. `dev` writes `dev.env` and `dev.tfrc` in its
   data directory. `dev.env` holds the variables that the Pulumi CLI, OpenTofu
   and the `opendustpan` client commands read: the server URL, the token,
   `SSL_CERT_FILE` and `TF_CLI_CONFIG_FILE`. A user runs
   `. <data dir>/dev.env` and every tool works.
4. **`dev` refuses production data too.** The brief has `serve` refuse dev
   data. The other direction matters as well: `dev` makes keys and binds to
   loopback, so `dev` on a production database might start with a wrong key
   or print a production admin token. So each command refuses the data of the
   other.

### `opendustpan dev`

`dev` is a server command. It runs the same server code as `serve`, with
its own defaults and its own small set of flags.

- **Bind address.** `127.0.0.1:8080` by default. `--listen` accepts another
  address. A non-loopback address adds a line to the banner that names the
  address. A container needs `--listen 0.0.0.0:8080`, because a port mapping
  cannot reach a loopback address inside the container (open question 2).
- **Data.** A new temporary directory by default. `dev` deletes it at a
  clean stop. `--data-dir DIR` keeps the data in `DIR` and uses it again at
  the next start. The directory holds the SQLite database, the blobs, the
  key file, the token file, the certificates, `dev.env` and `dev.tfrc`
  (open question 1).
- **Key.** `dev` makes a key file in the data directory on the first start.
  `DUSTPAN_KEY_ENCRYPTION_KEY` and `--key-encryption-key-file` are not
  settings of `dev`.
- **Admin token.** When `DUSTPAN_ADMIN_TOKEN` is set, `dev` uses it, so a CI
  job can choose the token. Else `dev` makes one on the first start, keeps
  it in the data directory and prints it.
- **Namespace.** `dev` creates the namespace `dev` when it does not exist,
  so the first `pulumi stack init dev/demo/dev` or `tofu init` works with no
  other command (open question 4).
- **Console.** `--console-dir` works as in `serve`.
- **Other settings.** `dev` takes `--log-level`, `--log-format`,
  `--disable-pulumi-journaling` and the size limits, so that a CI job can
  test the same limits as production. It does not take `--config`, the
  database, the blob store, the backup target, the audit sinks, OIDC or the
  proxy settings. A user who needs those runs `serve`.
- **Banner.** At each start, `dev` writes this text to stderr before the
  first log line, also with `--log-format json`:

  ```
  opendustpan dev: this server is for a trial or for CI. Do not use it in production.
  Data:     /tmp/opendustpan-dev-123456 (deleted at stop)
  Server:   https://localhost:8080
  Token:    <token>
  Settings: . /tmp/opendustpan-dev-123456/dev.env
  The key-encryption key is in the data directory. A backup of this directory holds the key.
  For production, run: opendustpan serve --help
  ```

- **Visible in the API and the console.** The installation API reports
  `"mode": "dev"`. The console shows a band on each page while the mode is
  `dev`. Each audit entry of the start records the mode.

### `opendustpan serve`

`serve` keeps its settings, with three changes:

- **An explicit key.** `serve` needs `DUSTPAN_KEY_ENCRYPTION_KEY` or
  `--key-encryption-key-file`. The default key file path, the terminal
  prompt and `--generate-key-encryption-key` go. With no key, `serve`
  stops before it opens the database:

  ```
  opendustpan: no key-encryption key is set: set --key-encryption-key-file (DUSTPAN_KEY_ENCRYPTION_KEY_FILE) or DUSTPAN_KEY_ENCRYPTION_KEY. To make a key file, run: opendustpan key generate --out <path>. For a trial, run: opendustpan dev
  ```

- **An explicit database.** `--database` has no default. With no database,
  `serve` stops:

  ```
  opendustpan: no database is set: set --database (DUSTPAN_DATABASE), for example sqlite:/var/lib/opendustpan/dustpan.db. For a trial, run: opendustpan dev
  ```

  The blob directory default stays next to the SQLite file, because the
  database setting already names a place the operator chose.
- **Refuses dev data.** See the next section.

`serve` keeps the listen default `:8080`, because a container and a host
behind a proxy both need a non-loopback address, and the address is not a
secret. `doctor` warns about plain HTTP on a non-loopback address.

### The dev marker

The `installation` table gets one column, `mode`, with the values `dev` and
`production`. This RFC says _mode_ until open question 5 has an answer. The command that creates the database writes it in the same
transaction as the first row. No API changes it, except the promotion
below.

- `serve` reads the mode before it serves a request. On `dev`, it stops:

  ```
  opendustpan: the database was made by opendustpan dev: serve refuses it. To keep the data, run: opendustpan serve --promote-dev-data. To start over, set a new --database.
  ```

- `dev` reads the mode. On `production`, it stops with the same form of
  error.
- A backup records the mode. `restore` into a production installation
  refuses a backup of a dev installation unless the request sets
  `--promote-dev-data`, with the same rules as below.

**To start over.** The user points `serve` at a new database. Each tool
moves its own state with its own commands: `pulumi stack export` and
`pulumi stack import`, `tofu state pull` and `tofu state push`. RFC 66 holds
tooling to move units between installations. The dev data stays until the
user deletes the data directory.

**To promote.** `serve --promote-dev-data` is a one-time flag for a dev
installation with `--data-dir`. It changes the mode to `production` and
writes an audit entry. Then it runs as a normal `serve`. It needs the same
key that encrypted the data, so the operator moves the dev key file out of
the data directory first and names it with `--key-encryption-key-file`.
`serve` refuses the promotion when the key file is in the data directory or
in the same directory as the SQLite file. A configuration file cannot set
this flag. A second start with the flag on a production database stops
with an error, so a stale flag in a unit file shows at once. The admin token
of `dev` keeps working, because it is the token in `DUSTPAN_ADMIN_TOKEN`
that `serve` reads. The operator sets a new token for production.

A database that an untagged build made has no mode. The migration that adds
the column sets `production` for it, because only `serve` made databases
before this change. ADR 73 decision 6 applies: this is not compatibility
code for a shipped shape.

### `opendustpan doctor`

`doctor` reads the settings of `serve` through the same layers (flag,
environment, configuration file) and checks them. It does not open the
database, so ADR 39 holds. With `--server`, it also asks a running server
through the API for the checks that need the server.

| Check | Result when it fails |
| --- | --- |
| A key-encryption key is set. The key file exists and only its owner can read it. | fail |
| The key file is not in the directory of the SQLite file or of the blobs. | warn |
| A database is set. | fail |
| An admin token is set and has at least 32 bytes. | fail |
| A backup target is set. | warn |
| The listen address is not loopback and TLS is not set, and no `--public-url` with `https` names a proxy. | warn |
| `--trusted-proxies` is set when `--public-url` is set. | warn |
| With `--server`: the server reports `production`, `upgrade check` gives no `cannot start`, the last backup is less than 25 hours old. | fail, warn, warn |

`doctor` prints one line for each check and exits with 1 when a check
fails. The output form is for people. A JSON form is open question 6.

### What replaces the escape hatch

| Today | After this RFC |
| --- | --- |
| `serve --generate-key-encryption-key` for a trial with no terminal | `dev` |
| `serve` prompt on a terminal | `dev` for a trial, `key generate` for production |
| default key file `dustpan.key` next to the database | gone: `serve` needs an explicit key |
| warning of ADR 154 decision 10 on each start with the made key file | gone: `serve` never uses a key file that it made. The banner of `dev` says where the key is. |
| `scripts/demo.sh` with the flag | `dev --data-dir` |
| `serve` refuses no data | `serve` refuses dev data, and `dev` refuses production data |

The `secret` package and `dustpan.NewServer` do not change. ADR 154
decision 10 keeps its rule that a running server always has a key: `dev`
always makes one, and `serve` always needs one.

## ADRs that change if this is accepted

ADR 1 lets an accepted ADR change only in its status line. The new ADR
names each change, and each of these ADRs gets a status line that points to
it:

| ADR | Decision | What changes |
| --- | --- | --- |
| 39 | 1, "only the server opens the data" | Two server commands open data: `serve` and `dev`. `key generate` and `doctor` open no data. |
| 41 | 12, "the key, with no silent default" | The default key file path, the terminal prompt and `--generate-key-encryption-key` go from `serve`. `dev` makes its own key. The error text changes. The "upgrade check" item names the new settings. |
| 154 | 10, the escape hatch and its warning | The escape hatch goes to `dev`. The warning on each start goes. This applies when pull request 847 merges first. Else the new ADR changes the text of that pull request before the merge. |
| 33 | the warning of the upgrade check | The warning names `--key-encryption-key-file`, `DUSTPAN_KEY_ENCRYPTION_KEY` and `key generate`. |
| 63 | the file keys of the configuration file | The key `generate-key-encryption-key` goes. `database` has no default. |

ADR 23 does not change: the key still comes from `DUSTPAN_KEY_ENCRYPTION_KEY`
or a file, and is 32 random bytes in base64.

RFC 7 asks if `serve --role` replaces `serve` or sits beside it. This RFC
does not answer that question. `dev` runs every module in one process, as
the Temporal and Consul trial commands do.

`GLOSSARY.md` gets two terms when the ADR is written: _dev installation_
(an installation that `dev` made, with the mode `dev`) and _promotion_ (the
change of a dev installation to production). The term _mode_ is near the
installation state `maintenance`. Open question 5 asks for a word.

## Personas

All personas are equal. Each row shows the first start and the normal run.

| Persona | First start | Normal run |
| --- | --- | --- |
| Homelab user | `opendustpan dev --data-dir ~/dustpan-trial`, then `. ~/dustpan-trial/dev.env`. Pulumi CLI and OpenTofu work at once. | When the trial becomes real: `opendustpan key generate --out ~/.config/opendustpan/dustpan.key`, then `serve --database sqlite:/srv/dustpan/dustpan.db --key-encryption-key-file ...` in a systemd unit. Either start over, or move the key out and run `serve --promote-dev-data` one time. |
| Small team | One person runs `opendustpan dev` on a laptop for a day. | The container image on one host with a mounted key file, SQLite or PostgreSQL, a backup target and OIDC sign-in. `opendustpan doctor` before the first start. |
| Startup | `opendustpan dev` locally, and in CI from day one. | PostgreSQL and S3 from a cloud provider, the key in a secret store mounted as a file, two instances behind a load balancer. `doctor --server` in the deploy pipeline after each upgrade. |
| Enterprise | A platform engineer runs `opendustpan dev` to read the API and the console. A security reviewer reads `serve --help`: every production start needs a key, a database and a token that the operator gave. | Configuration file from configuration management, the key from a secret store or a KMS (RFC 33), audit sinks, signed audit checkpoints, IP allowlists. `serve` refuses dev data, so a copy of a test database cannot become production by mistake. |
| CI pipeline | `opendustpan dev --data-dir "$RUNNER_TEMP/dustpan" &`, then `opendustpan health` in a loop, then `. "$RUNNER_TEMP/dustpan/dev.env"`. With `DUSTPAN_ADMIN_TOKEN` set by the job, the token is known before the start. In a service container: `dev --listen 0.0.0.0:8080`. | The same at each run. The data directory goes with the runner. No key, certificate or token is a CI secret. |

## Compatibility

- **No tag shipped.** ADR 73 decision 4 forbids an alias or a shim for an
  unreleased shape. If this lands before the first tag,
  `--generate-key-encryption-key`, `DUSTPAN_GENERATE_KEY_ENCRYPTION_KEY`, the
  file key, the prompt and the default database go with no deprecation.
- **If a tag ships first.** ADR 73 decision 1 then applies to each setting
  that the tag shipped: the binary refuses to start, and the error names the
  setting and its replacement. For example:
  `--generate-key-encryption-key was removed: for a trial, run opendustpan dev; for production, set --key-encryption-key-file`.
  A start with no `--database`, on a host that relied on the old default,
  gets the error of "An explicit database", which names the old default as
  the example. The binary never ignores a setting that it no longer has.
- **The tool-compatible APIs do not change.** `dev` serves the same routes
  as `serve`. The Pulumi CLI and OpenTofu see no difference. `make e2e`
  runs against `dev` and against `serve`, so both start paths stay tested.
- **The tool-neutral APIs.** The installation API gains the field `mode`.
  The console change (the band) merges before the next server tag (ADR 73
  decision 3).
- **The container image.** The `CMD` of the image stays
  `serve` with the database in `/data`. A `docker run` with no key stops with
  the key error, which names `dev`. A trial runs
  `docker run -p 8080:8080 <image> dev --listen 0.0.0.0:8080` (open
  question 2).
- **Documents and scripts.** The README quickstart, the configuration
  reference, the security model, the guide for several instances, the guide
  for backup and upgrade, `scripts/demo.sh` and the e2e setup change in the
  same pull request as the code.

## Open questions

1. **Where does `dev` keep its data by default?**
   - (a) A temporary directory, deleted at a clean stop. As Vault and
     Temporal. A CI run leaves nothing. A trial user loses the data at a
     restart unless they set `--data-dir`.
   - (b) A directory per user, for example `$XDG_DATA_HOME/opendustpan/dev`.
     Data survives a restart. A CI run leaves files on a reused runner.
   - (c) `./.opendustpan-dev` in the working directory. Easy to find. A user
     might commit it to git with the key in it.

   Recommendation: (a). The banner says that the data goes at stop and
   names `--data-dir`.
2. **May `dev` bind to a non-loopback address?**
   - (a) Yes, with `--listen`, and a banner line. Containers and CI service
     containers need it.
   - (b) No. Containers run `serve` with explicit settings.
   - (c) Yes, only with a second flag, for example `--expose`.

   Recommendation: (a). A loopback-only `dev` fails in every container,
   and a second flag adds a step without a decision behind it.
3. **Does `dev` serve TLS by default?**
   - (a) Yes, with a local certificate authority that `dev` makes.
     OpenTofu works at once. Each client must trust the authority:
     `dev.env` sets `SSL_CERT_FILE`, which works on Linux. On macOS and
     Windows, Go clients read the system store, so the user might need to
     trust the authority by hand.
   - (b) No. Plain HTTP. The Pulumi CLI works at once on every system.
     OpenTofu through the TFE-compatible API does not. The OpenTofu http
     backend might work over plain HTTP.
   - (c) Both: HTTP on one port and HTTPS on the next port.

   Recommendation: (a) on Linux, with a check before a decision on how
   macOS clients behave. If macOS needs manual steps, (c).
4. **Does `dev` create a namespace?**
   - (a) Yes, `dev`.
   - (b) Yes, `default`, as Temporal.
   - (c) No. The user runs `namespace create`.

   Recommendation: (a). The name repeats the warning in every stack and
   workspace name.
5. **The word for the marker.**
   - (a) _mode_ (`dev`, `production`). Short. Near the installation state
     `maintenance`, which a reader might confuse with it.
   - (b) _purpose_.
   - (c) _origin_ (`dev`, `serve`): names the command that made the data.

   Recommendation: (b). It does not collide with the installation state.
6. **Does `doctor` have a JSON output?**
   - (a) Not now. People read it, and the exit code is for scripts.
   - (b) Yes, `--format json`, for deploy pipelines.

   Recommendation: (a), and (b) when a user asks.
7. **Promotion or start over only?**
   - (a) Both, as above.
   - (b) Start over only. Simpler code. A homelab user with a month of
     state in a trial loses a little time.

   Recommendation: (a). The flag is small, and it puts the decision (move
   the key out of the data) in the hands of the operator.
8. **Does the container image default to `dev` or `serve`?**
   - (a) `serve`. A production image that fails with no key is the safe
     default.
   - (b) `dev`. `docker run <image>` gives a trial at once.

   Recommendation: (a). The error names the `dev` command.

## Outcome

Empty until the RFC is accepted or dropped.
