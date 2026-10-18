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
  README quickstart runs `openssl` two times. For OpenTofu through the
  TFE-compatible API, it runs `openssl` a third time to make a TLS
  certificate, writes a credentials file and sets `SSL_CERT_FILE`.
- **Production.** `serve` has defaults that suit a trial and are wrong for
  production: the database `sqlite:dustpan.db` in the working directory,
  the key file `dustpan.key` next to the database, and a prompt that makes
  that key file. A key file next to the data does not protect a backup of
  that data (`docs/reference/configuration.md`).

To make the trial possible without a terminal, ADR 41 decision 12 added
`--generate-key-encryption-key`. ADR 154 decision 10 names this flag "the
escape hatch for development", and adds a warning on each start. So the
same flag is a development aid and a production risk, and the warning text
tells the operator what to do for production after the server started.
`scripts/demo.sh` uses the flag. The guide for several instances says not to
use it on more than one instance, because each instance then makes its own
key and cannot read the secrets of the others.

The cause is that one command serves two intents. A flag cannot fix that,
because a flag is a choice the user makes after they chose the command.

Facts from the code on `main` (commit `981056a`):

- The command table is in `cmd/opendustpan/commands.go`. Each row comes
  from `internal/cli`. `serve` is the only command that opens the database
  (ADR 39). Every other command is a client of the REST API.
- `serve` stops when the operator sets no admin token (`DUSTPAN_ADMIN_TOKEN`
  or `DUSTPAN_ADMIN_TOKEN_FILE`). It clears `DUSTPAN_ADMIN_TOKEN` from its
  environment after the read.
- The baseline migration of each dialect inserts the one `installation`
  row (`INSERT INTO installation (id, state) VALUES (1, 'normal')`). No
  command writes that row at creation.
- A backup leaves out the `installation` table, and a restore keeps the
  live row (`pkg/backup/backup.go`).
- No rewrap command exists (`pkg/secret/secret.go`): an installation cannot
  move its secrets to a new key-encryption key.
- `dustpan.NewServer` reads no database. `Server.Run` runs the start-up
  steps (`startUp` in `pkg/dustpan/dustpan.go`, with `secret.CheckWrapper`
  first) before it listens. `Server.Handler` returns the handler and runs
  no start-up step.
- The store adapters apply the migrations in `sqlite.Open` and
  `postgres.Open`.
- The container image declares `VOLUME /data` and runs as the user
  `nonroot`. The image has no `/data` directory, so the container runtime
  makes the mount point.
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
| `--tls-cert`, `--tls-key` | run in production | none | needed for OpenTofu through the TFE-compatible API |
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
| `DUSTPAN_KEY_ENCRYPTION_KEY` (no flag) | run in production | none | needs one of the key sources |
| `DUSTPAN_OIDC_CLIENT_SECRET`, `DUSTPAN_OIDC_CLIENT_SECRET_FILE` (no flag) | run in production | none | no value |
| `DUSTPAN_AUDIT_WEBHOOK_SECRET`, `DUSTPAN_LOG_WEBHOOK_SECRET` and their `_FILE` forms (no flag) | run in production | none | no value |
| `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN` (no flag) | run in production | none | no value |

`serve` has 42 flags (`internal/cli/settings.go`) and 12 settings that only
an environment variable sets (9 in `internal/cli/settings.go` and the 3 AWS
variables). A trial needs three of them: an admin token, a key, and for
OpenTofu through the TFE-compatible API a TLS certificate. Each of the
three costs the trial user a step. A production operator must decide 6 of
the defaults again: the database, the blob directory, the key file, the
listen address, TLS and the backup target. Today `serve` takes those
decisions for the operator without a word.

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
| Temporal | `temporal server start-dev` | Keeps data in memory, or in a SQLite file with `--db-filename`. Starts the web UI. Creates the `default` namespace. `--ip` binds to another address. The documentation page does not state the default bind address. The SDKs connect to `127.0.0.1`. | The Temporal server, a separate binary and image | A database, a configuration file, schema setup |
| HashiCorp Vault | `vault server -dev` | Binds to `127.0.0.1:8200`, or to `-dev-listen-address`. Keeps data in memory. Starts unsealed. Makes and prints a root token. Prints a warning that dev mode must not be used in production. `-dev-tls` makes a CA and a certificate in a temporary directory, or in `-dev-tls-cert-dir`. Without `-dev-tls`, it serves plain HTTP. | `vault server -config=<file>` | A configuration file with storage and a listener, then `vault operator init` to make the unseal keys |
| Keycloak | `kc.sh start-dev` | Turns on HTTP and turns off the strict host name check. Logs that it runs in development mode. | `kc.sh start` | Refuses to start without a host name setting and HTTPS, unless the operator turns each off by name |
| HashiCorp Consul, Nomad | `consul agent -dev`, `nomad agent -dev` | One process with every role, data in memory, bound to `127.0.0.1` | `agent -config=<file>` | A configuration file |
| HashiCorp Boundary | `boundary dev` | A built-in database in a container, made credentials, all services in one process | `boundary server -config=<file>` | A configuration file, a database, KMS settings |
| PostgreSQL container image | none | Not applicable | `docker run postgres` | Refuses to start without `POSTGRES_PASSWORD`. `POSTGRES_HOST_AUTH_METHOD=trust` is an escape hatch with a warning in the log. |
| MinIO | none | Not applicable | `minio server <dir>` | Starts with a known default user and password, and logs a warning |

Four lessons for OpenDustpan:

1. Two patterns exist for the trial. Vault, Consul and Nomad use a `-dev`
   flag on the production command. Temporal, Keycloak and Boundary use a
   separate subcommand. In both patterns, the name of the trial path says
   "dev", and the trial path writes no file that the production path then
   reads. The precedent does not choose between a flag and a command. The
   reasons in "Option 2" choose.
2. A trial path binds to the loopback address by default and makes its
   own credentials.
3. The production command refuses to start without the decisions that it
   cannot take for the operator. The error names the setting.
4. The PostgreSQL image and our `--generate-key-encryption-key` use the same
   pattern: an escape hatch on the production path, with a warning. MinIO's
   default user is the same pattern with no opt-in. These are the patterns
   this RFC takes away.

One difference: Vault, Consul and Nomad keep dev data in memory only. So dev
data can never reach a production server. Temporal can keep dev data in a
file. OpenDustpan has two tools whose state a user might want to keep
through a restart of a trial, so this RFC lets dev data persist, and adds a
marker so that `serve` can refuse it (see "The dev marker"). No benchmark
turns dev data into production data: Vault and Temporal have no such path.

## Options

### Option 1: do nothing

Keep `serve` with its defaults, the prompt and `--generate-key-encryption-key`.

- Cost: none now.
- Gives: nothing new. The trial keeps its three steps. A production server
  can still start with a key file next to its data, and the only signal is
  a warning line after the start. CI pipelines keep the escape hatch on the
  production command.

### Option 2: a profile flag on `serve`

`serve --profile=dev` or `serve --dev` turns on the trial defaults, as
`vault server -dev` does.

- Cost: small. One flag and a set of defaults.
- Gives: a trial in one command. Against it:
  - The settings pattern gives each flag an environment variable and a key
    in the configuration file. So a production configuration file or a
    container specification can turn on dev by mistake, and the setting
    persists there. An exception for one flag breaks the pattern of
    ADR 63.
  - The help of `serve` must explain both modes. Each new setting must say
    what it does in each mode.

### Option 3: a `dev` command and a `serve` command that needs decisions

Two server commands, one for each intent. `dev` takes every decision for the
user and says so. `serve` takes none of the decisions that only the operator
can take. A marker stored with the data keeps each command away from the
data of the other.

- Cost: medium. A second server command, a column in the `installation`
  table with a store port, a change to the README quickstart, the guides,
  the container image, `scripts/demo.sh` and the e2e setup. ADRs 23, 39,
  41 and 154 get a status line.
- Gives: a trial with zero steps before the first start, also in CI. A
  `serve` command whose every start is a production start. The help of
  each command explains one intent.

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
   in base64 with mode 0600, refuses a file that exists, and prints the
   path and the warning that a lost key loses every secret. It opens no
   data and reads no key. The operator chooses the path, so the decision
   about where the key lives stays with the operator.
2. **TLS in `dev`.** The brief does not name TLS. OpenTofu refuses plain
   HTTP for service discovery, so the TFE-compatible API (the `cloud`
   block) needs HTTPS. The OpenTofu `http` backend works over plain HTTP.
   See "TLS in `dev`" and open question 3.
3. **A file of client settings.** The brief prints an admin token. A CI job
   cannot read a banner easily. `dev` writes `dev.env` and `dev.tfrc` in its
   data directory. `dev.env` holds the variables that the Pulumi CLI, OpenTofu
   and the `opendustpan` client commands read: the server URL, the token
   (see "What `dev` prints and writes"), `SSL_CERT_FILE` when TLS is on,
   `TF_CLI_CONFIG_FILE`, `TF_HTTP_USERNAME=dustpan`, `TF_HTTP_PASSWORD`, and
   `DUSTPAN_DEV=1`. OpenTofu sends basic authentication only with a user
   name, and the server ignores the user name. The server variables are
   `PULUMI_BACKEND_URL` and `DUSTPAN_SERVER`. A user
   runs `. <data dir>/dev.env` and every tool works. `dev.env` is a POSIX
   `sh` file. On GitHub Actions, a step runs `. dev.env` and the tools in
   the same step.
4. **`dev` refuses production data too.** The brief has `serve` refuse dev
   data. The other direction matters as well: `dev` makes keys and binds to
   loopback, so `dev` on a production database might start with a wrong key.
   So each command refuses the data of the other.

### Where the new commands live

ADR 39 decision 1 says that only the server opens the key-encryption key.
Decision 4 forbids the client package to import the key-encryption key code,
and a test fails when a command other than `serve` opens a store.

- `dev` is a server command. Its wiring sits next to the wiring of `serve`
  in `cmd/opendustpan`, and it may import what `serve` imports. ADR 39
  decisions 1 and 4 change to name `serve` and `dev`.
- `key generate` writes 32 bytes from `crypto/rand` in base64. It does not
  import the key-encryption key code of `pkg/secret`. It sits in
  `internal/cli` with the client commands. It opens no store and reads no
  key, so decision 4 holds for it. Decision 1 changes in its text: a command
  that writes a new key file does not open "the key-encryption key" of an
  installation.
- `doctor` sits in `internal/cli`. It calls `stat` on the key file (owner,
  mode, size, path) and does not read the file. It reads the token length
  from the setting layers, which the client commands already read. So
  decisions 1 and 4 hold for it.

### `opendustpan dev`

`dev` is a server command. It runs the same server code as `serve`, with
its own defaults and its own small set of flags.

- **Bind address.** `127.0.0.1:8080` by default. `--listen` accepts another
  address. A non-loopback address adds a line to the banner that names the
  address. A container needs `--listen 0.0.0.0:8080`, because a port mapping
  cannot reach a loopback address inside the container. The documented
  container command publishes the port on the host loopback address only:
  `docker run -p 127.0.0.1:8080:8080 <image> dev --listen 0.0.0.0:8080`.
  A plain `-p 8080:8080` publishes on every host interface.
- **The data directory.** `dev` finds its data directory in this order:
  1. `--data-dir DIR`: the directory is `DIR`.
  2. `DUSTPAN_DATA_DIR`: the variable names a parent directory, for example
     the volume `/data` of the image. The directory is `<parent>/dev`.
     `dev` makes this subdirectory with mode 0700, so the mode of the
     parent does not matter. The parent can also hold the database of
     `serve`, and `dev` never writes outside its subdirectory.
  3. Neither: a new directory from `os.MkdirTemp` (see open question 1 for
     what happens at stop).

  The directory holds the SQLite database, the blobs, the key file, the
  token file, the certificate, `dev.addr`, `dev.env` and `dev.tfrc`. `dev`
  uses it again at the next start.
- **Ownership and file modes.** `dev` makes the data directory with mode
  0700. It sets mode 0700 on an empty directory that it claims. A
  `lost+found` directory at a mount root is allowed. It makes the key file, the token file, `dev.addr`, `dev.env`,
  `dev.tfrc` and each private key of the TLS design with mode 0600. At each
  start, `dev` refuses to start when one of these is true:
  - The group or others can read, write or enter the data directory, as
    `ssh` refuses a private key with such a mode.
  - The current user ID does not own the data directory, the key file,
    `dev.addr`, `dev.env` or `dev.tfrc`.

  The error names the path, its mode or owner, and one command:
  `chmod 700 <dir>`, or `chown <uid> <path>` with the current user ID.
- **The volume of the image.** The image has no `/data` directory, so the
  container runtime makes the mount point, owned by root. The user
  `nonroot` then cannot make `/data/dev`. The build stage makes an empty
  directory, and the final stage copies it with
  `COPY --from=build --chown=nonroot:nonroot /empty /data` before
  `VOLUME /data`. A named volume copies that owner only when the volume is
  new and empty. For a bind mount and for Kubernetes, the guide sets
  `fsGroup: 65532`. This also helps `serve` on a new named volume. A bind
  mount keeps the owner on the host. For a bind
  mount that `nonroot` cannot write, `dev` stops with an error that names
  the path and the user ID 65532 of `nonroot`. The image smoke test must
  check that `dev` starts on a new named volume.
- **Only its own data.** `dev` opens a database only in a data directory
  that holds its own key file, and only when that key file starts with the
  dev header. `dev` checks this before it opens or migrates anything:
  - A directory that does not exist, or that is empty: `dev` makes the key
    file first, then the database.
  - A directory with files but with no key file that starts with the dev
    header: `dev` stops, and opens no file in it.

  ```
  opendustpan: /srv/data holds files but no key file of opendustpan dev: dev refuses it. Set a new --data-dir.
  ```

  So `dev --data-dir` on the directory of a production database never
  opens, migrates or claims that database.
- **Key.** `dev` makes a key file in the data directory on the first start.
  `DUSTPAN_KEY_ENCRYPTION_KEY` and `--key-encryption-key-file` are not
  settings of `dev`. The key file starts with a dev header line, so that
  `serve` can refuse it (see "Threat model of the marker"). The 32 random
  bytes in base64 follow the header line.
- **Admin token.** `dev` never reads `DUSTPAN_ADMIN_TOKEN` or
  `DUSTPAN_ADMIN_TOKEN_FILE`. An operator shell or a CI job often exports
  the production token in those variables, because the client commands read
  them. `dev` clears both from its process environment at start, as `serve`
  clears `DUSTPAN_ADMIN_TOKEN`. A job that must choose the token sets
  `DUSTPAN_DEV_ADMIN_TOKEN` or `--admin-token-file FILE`. `dev` clears
  `DUSTPAN_DEV_ADMIN_TOKEN` from its process environment after it reads the
  variable. Else `dev` makes a token on the first start and keeps it in the
  token file.
- **What `dev` prints and writes.** `dev` writes the token into `dev.env`
  only when `dev` made it.
  - A token that `dev` made: `dev.env` sets `DUSTPAN_ADMIN_TOKEN`,
    `PULUMI_ACCESS_TOKEN` and `TF_HTTP_PASSWORD` to it, sets
    `TF_HTTP_USERNAME=dustpan`, and `dev.tfrc`
    holds it in a `credentials` block.
  - A token from `DUSTPAN_DEV_ADMIN_TOKEN`: `dev.env` takes the value from
    the variable in the shell that loads it, and holds no token:
    `export DUSTPAN_ADMIN_TOKEN="${DUSTPAN_DEV_ADMIN_TOKEN:?}"`, and the same
    for `PULUMI_ACCESS_TOKEN` and `TF_HTTP_PASSWORD`, and it sets
    `TF_HTTP_USERNAME=dustpan`. OpenTofu reads a `TF_TOKEN_` variable by
    host name only. Its encoding has no form for a port, so no `TF_TOKEN_`
    variable names `localhost:8080`. So for a token from
    `DUSTPAN_DEV_ADMIN_TOKEN`, `dev.tfrc` holds the token in a
    `credentials` block, with mode 0600 in the data directory with mode
    0700. The ADR checks this with the real CLI.
  - A token from `--admin-token-file`: `dev.env` sets
    `DUSTPAN_ADMIN_TOKEN_FILE` to that path, and holds no token. The Pulumi
    CLI and OpenTofu read no token file, so the banner names the variables
    that the user sets for them.
- **The guard against the reverse path.** `dev.env` exports `DUSTPAN_DEV=1`.
  `DUSTPAN_DEV` is not a setting: it has no flag and no key in the
  configuration file, and only `serve` and `doctor` read it. `serve` and
  `doctor` refuse to start when the environment holds `DUSTPAN_DEV`:

  ```
  opendustpan: DUSTPAN_DEV is set. The dev.env file of opendustpan dev sets it. Start serve in a new shell.
  ```

  So `. dev.env` and then `opendustpan serve` in the same shell cannot give
  a production server the dev token.
- **Namespace.** `dev` creates the namespace `dev` when it does not exist,
  so the first `pulumi stack init dev/demo/dev` or `tofu init` works with no
  other command (open question 4).
- **Console.** `--console-dir` works as in `serve`.
- **Other settings.** `dev` takes `--log-level`, `--log-format`,
  `--disable-pulumi-journaling` and the size limits, so that a CI job can
  test the same limits as production. It does not take `--config`, the
  database, the blob store, the backup target, the audit sinks, OIDC or the
  proxy settings. A user who needs those runs `serve`.
- **No backup.** `dev` does not take `--backup-target`. So no backup of a
  dev installation exists, and no restore path for dev data exists. The
  `backup` command against `dev` gets the error of a server with no target.
- **Banner.** At each start, `dev` writes this text to stderr before the
  first log line, also with `--log-format json`. The example uses
  `--data-dir`. Without it, the `Data` line also states what `dev` does
  with the directory at stop, as open question 1 decides.

  ```
  opendustpan dev: this server is for a trial or for CI. Do not use it in production.
  Data:     /home/alex/dustpan-trial
  Server:   https://localhost:8080
  Token:    <token>
  Settings: . /home/alex/dustpan-trial/dev.env
  The key-encryption key is in the data directory. A backup of this directory holds the key.
  For production, run: opendustpan serve --help
  ```

  `dev` prints the token only when stderr is a terminal and
  `DUSTPAN_DATA_DIR` is not set. Else the `Token` line names the token
  file: `Token:    in /home/alex/dustpan-trial/admin-token`. The logs of a
  CI run in a public repository are public. The image sets
  `DUSTPAN_DATA_DIR`, so `dev` in the image never prints the token: the
  container runtime keeps stderr in `docker logs`, also with
  `docker run -t`. A user of the image reads the token with
  `docker cp <container>:/data/dev/dev.env .`.
- **Visible in the API and the console.** The installation API reports
  the mode (`"mode": "dev"` until open question 5 names the field). The
  console shows a band on each page while the mode is `dev`. Each audit
  entry of the start records the mode.

### TLS in `dev`

The default (TLS on or off) is open question 3. The rules below apply to
every option that serves TLS.

- **No trust store change.** `dev` never changes a system or user trust
  store, also not with a flag. When a client needs the certificate in a
  trust store, the banner prints the command for the platform, and the user
  runs it.
- **One self-signed leaf, no CA.** `dev` makes one self-signed certificate
  for the server, with no certificate authority. A leaf cannot sign another
  certificate, so a user who trusts it trusts only this server name. The
  leaf key has mode 0600 in the data directory. The leaf lives for 30 days.
- **Renewal at start only.** `dev` makes a new leaf at a start when less
  than 7 days remain. `dev` does not renew the leaf while it runs, as
  `serve` reads its certificate once at start (ADR 27). So one run of `dev`
  serves for at least 7 days. The banner prints the expiry date. After the
  expiry, the clients refuse the server, and a restart of `dev` makes a new
  leaf. A new leaf also needs a new trust step on a platform where the user
  trusted the old leaf by hand.
- **If a CA is necessary.** If a client refuses a self-signed leaf, the
  ADR may choose a CA instead, with all of these rules: X.509 name
  constraints for `localhost`, `127.0.0.1`, `::1` and the extra names
  below, a lifetime of at most 30 days, and the CA key never on disk. `dev`
  signs the leaf, discards the CA key, and makes a new CA and leaf at
  expiry.
- **Names in the certificate.** `localhost`, `127.0.0.1` and `::1`, the host
  of `--listen` when it is not a wildcard, the name from `os.Hostname`, and
  each name in `--hostname NAME` (a flag that can repeat). A CI job in a
  container reaches a service container by its service name, so the job
  sets `--hostname dustpan`.
- **`SSL_CERT_FILE` replaces the roots.** In Go, `SSL_CERT_FILE` replaces
  the system root store. It does not add to it. A `dev.env` that points it
  at the dev certificate alone breaks `tofu init` provider downloads,
  Pulumi plugin downloads and each cloud API call of a provider in that
  shell. So on Linux, `dev` writes a bundle file and `dev.env` points
  `SSL_CERT_FILE` at the bundle. The bundle holds the roots that the shell
  of `dev` uses now, then the dev certificate:
  - With `SSL_CERT_FILE` in the environment: the certificates of that file.
  - With `SSL_CERT_DIR` in the environment: the certificates in each
    directory of the list. Go splits the list at `:`.
  - With both: the certificates of both.
  - With neither: the certificates of the first root file that Go reads on
    that platform.

  `dev.env` then sets `SSL_CERT_FILE` and clears `SSL_CERT_DIR`, so that
  the clients read the bundle only. The bundle is a copy: a change to the
  system roots after the start of `dev` shows in the bundle at the next
  start.
- **macOS and Windows.** Since Go 1.27, `SSL_CERT_FILE` and `SSL_CERT_DIR`
  also replace the platform verifier on macOS and Windows, unless the
  environment holds `GODEBUG=x509sslcertoverrideplatform=0`
  (`crypto/x509/root.go`).
  Before Go 1.27, Go on those platforms ignored both variables. So the
  result depends on the Go version that built each Pulumi CLI and OpenTofu
  release. On those platforms, no file of the system roots exists to put in
  a bundle, so a bundle loses the roots that live only in the platform
  store. On macOS and Windows, `dev.env` does not set `SSL_CERT_FILE`. The
  banner prints the trust command instead. A test with real CLI builds on
  macOS and Windows must check this before the ADR.

### `opendustpan health` and `dev`

`health` probes plain HTTP unless the user sets `--tls-cert`, and the container
`HEALTHCHECK` runs `/opendustpan health` with no flags. So the probe must
find the address and the certificate of `dev`.

- **`dev.addr`.** `dev` deletes `dev.addr` at start, before it listens.
  When it listens, it writes `dev.addr` (mode 0600) with the bound address
  and the certificate path. `dev.addr` also holds the process ID and the
  start time of `dev` (`/proc/<pid>/stat`). `health` treats a `dev.addr`
  whose process ID has another start time as missing, and then probes with
  the settings of `serve`. At stop, it deletes `dev.addr` again. The file
  holds an address that a client can dial: `dev` writes `127.0.0.1` for
  the wildcards `0.0.0.0`, `::` and an empty host. It writes an IPv6
  address in the form `[addr]:port`.
  A crash leaves a stale `dev.addr`. The probe then fails to connect and
  reports "not ready".
- **`health --data-dir DIR`** reads `DIR/dev.addr`, and probes that address
  with that certificate. Before it reads, `health` checks that the current
  user ID owns `DIR` and `dev.addr`, and that the group and others cannot
  write either. Else it reports "not ready" and names the path.
- A missing `dev.addr` or a missing certificate is "not ready", exit code 1.
  So a CI loop that runs `opendustpan health --data-dir "$DIR"` until it
  exits with 0 works before `dev.env` exists.
- **The container.** The image sets `DUSTPAN_DATA_DIR=/data` in the
  environment for `dev` and for `health`. The `HEALTHCHECK` instruction
  stays `["/opendustpan", "health"]`. When the environment holds
  `DUSTPAN_DATA_DIR` and `/data/dev/dev.addr` exists, `health` probes `dev`. Else it probes with
  the listen and TLS settings of `serve`, as today.

### `opendustpan serve`

`serve` keeps its settings, with these changes:

- **An explicit key.** `serve` needs `DUSTPAN_KEY_ENCRYPTION_KEY` or
  `--key-encryption-key-file`. The default key file path, the terminal
  prompt and `--generate-key-encryption-key` go. With no key, `serve`
  stops before it opens the database.
- **An explicit database.** `--database` has no default. With no database,
  `serve` stops. The blob directory default stays next to the SQLite file,
  because the database setting already names a place the operator chose.
- **The errors for a missing key or database.** `serve` checks the key
  first, then the database, and reports only the first missing one. Each
  error names one command. When no setting of `serve` counts as set and
  stderr is a terminal, the person most likely wants a trial, and the error
  is the same for both:

  ```
  opendustpan: no key-encryption key is set. For a trial, run: opendustpan dev
  ```

  ```
  opendustpan: no database is set. For a trial, run: opendustpan dev
  ```

  Else:

  ```
  opendustpan: no key-encryption key is set: set --key-encryption-key-file (DUSTPAN_KEY_ENCRYPTION_KEY_FILE) or DUSTPAN_KEY_ENCRYPTION_KEY. To make a key file, run: opendustpan key generate --out <path>
  ```

  ```
  opendustpan: no database is set: set --database (DUSTPAN_DATABASE), for example sqlite:/var/lib/opendustpan/dustpan.db
  ```

  These count as a setting of `serve` for the trial hint: a flag of
  `serve`, a key in a configuration file, `--config` or `DUSTPAN_CONFIG`,
  and each `DUSTPAN_` variable of the settings table above. These do not
  count, because a shell often exports them for other commands or other
  tools: `DUSTPAN_ADMIN_TOKEN`, `DUSTPAN_ADMIN_TOKEN_FILE`,
  `DUSTPAN_SERVER`, `DUSTPAN_DEV`, `DUSTPAN_DATA_DIR` and the AWS
  variables. `serve` checks stderr, not stdin, because the error goes to
  stderr: a terminal on stderr means that a person reads it.
- **A warning for a key next to the data.** When the key file is in the
  directory of the SQLite file or of the blobs, `serve` logs a warning at
  each start. `serve` resolves symbolic links before the check. `doctor`
  reports the same check, but `doctor` is optional.
- **Refuses a dev key.** `serve` refuses a key file that starts with the
  dev header.
- **Refuses `DUSTPAN_DEV`.** See "The guard against the reverse path".
- **Refuses dev data.** See the next section.

`serve` keeps the listen default `:8080`, because a container and a host
behind a proxy both need a non-loopback address, and the address is not a
secret. `doctor` warns about plain HTTP on a non-loopback address.

### The dev marker

The `installation` table gets one column, `mode`, with the values `dev`,
`production` and NULL. This RFC says _mode_ until open question 5 has an
answer.

- **The baseline row has no mode.** The baseline migration inserts the row
  with `mode` NULL. No column default sets a mode. So a crash between the
  migration and the claim leaves NULL, never a wrong mode.
- **A database from an untagged build.** The migration that adds the
  column gives the existing row the mode NULL. The first command that
  starts on that database claims it, as on a new database. This converts no
  data (ADR 73 decision 6).
- **The check before the migrations.** `sqlite.Open` and `postgres.Open`
  get an option with the mode of the command. When the `mode` column
  exists, the adapter reads the mode before it applies a migration. When
  the stored mode is not NULL and differs from the mode of the command, the
  adapter returns an error and applies no migration. So `serve` on a dev
  database stops before it changes the schema. When the column does not
  exist yet, the adapter applies the migrations, and the claim below
  decides. An adapter opened with no mode option does no check before the
  migrations (the SDK default), and the claim decides.
- **The claim.** The server claims the mode first in the start-up steps,
  before any step that writes, and before the listener opens. The claim is one
  compare-and-set: `UPDATE installation SET mode = ? WHERE id = 1 AND mode
  IS NULL`. Then the server reads the mode. When the stored mode differs
  from the mode of the command, the start stops. Several instances of
  `serve` on one new database all claim `production`, so the order does not
  matter.
- **The store port.** A new small port, `installation.ModeStore` (a name
  for the ADR to choose), holds one method:
  `ClaimMode(ctx, mode) (Mode, error)`. It stores `mode` when the stored
  mode is NULL and returns the stored mode. It reports `ErrUnavailable`
  when the row is missing, when the mode cannot be read, and when the
  stored value is outside the known set: an unknown stored value is a
  server fault, as `maintenance.Store.GetState` reports it. A separate port
  keeps `maintenance.Store` to the maintenance state, and keeps a fake for
  a test small. Its conformance tests land in `pkg/store/storetest` before
  an adapter implements it: a claim on NULL stores the mode. A second claim
  with another mode returns the first mode and changes nothing. Two claims
  at the same time store one mode. An unknown stored value gives
  `ErrUnavailable`.
- **The mode in the SDK.** `dustpan.NewServer` gets one option, for
  example `dustpan.WithInstallationMode(mode)`, and the mode store option.
  `NewServer` reads no database, as today. `cmd/opendustpan` passes
  `production` for `serve` and `dev` for `dev`. With no mode option,
  `NewServer` uses `production`, so an SDK user gets the production rules.
  No API and no command changes the mode after the claim.
- **An embedder that serves `Handler`.** `Server.Run` runs the start-up
  steps, but an embedder that mounts `Server.Handler` in its own server
  never calls `Run`. The rule: `Handler` answers 503 on each route until
  the start-up steps succeeded, except `GET /health/live`, which stays 204
  (ADR 34). `GET /health/ready` answers 503 in the same time. A new
  exported method, `Server.Start(ctx) error`, runs the start-up steps
  (today `runStartUp`: the key check, the claim, the running backups and
  restores with the fence, the audit chain and the sign-in providers).
  `Run` calls `Start`. `Start` runs once: `Run` after a successful `Start`
  does not repeat the steps. An embedder that serves `Handler` calls `Start`. Why the gate
  and not an exported start check alone: an embedder can forget a call,
  and a forgotten check fails open. With the gate, a forgotten `Start`
  gives 503, which the embedder sees at the first request. The gate also
  covers `secret.CheckWrapper`, which an embedder of `Handler` skips today.

The errors:

```
opendustpan: opendustpan dev made this database: serve refuses it. To start over, set a new --database.
```

```
opendustpan: opendustpan serve made this database: dev refuses it. Set a new --data-dir.
```

**No backup path.** `dev` has no backup target, so no backup of a dev
installation exists. A backup leaves out the `installation` table and a
restore keeps the live row, so a restore never changes the mode. This RFC
adds nothing to `backup` or `restore`, and ADR 30, ADR 38 and ADR 42 do not
change.

**Dev data stays dev data.** No command and no flag turns a dev
installation into a production installation (see "Decisions of the product
manager"). To keep the units of a trial, the user moves them into a
production server with the commands of each tool. For each Pulumi stack:

```
pulumi login https://localhost:8080                 # in the shell with dev.env (the --tls case)
pulumi config --stack dev/demo/dev --show-secrets --json > demo-config.json   # in the shell with dev.env
pulumi stack export --stack dev/demo/dev --show-secrets --file demo.json
pulumi login https://dustpan.example.com            # in a new shell, with the production token
pulumi stack init team/demo/dev
pulumi stack import --stack team/demo/dev --file demo.json
# set each secret value again: pulumi config set --stack team/demo/dev --secret <key> <value>
```

For each OpenTofu workspace, in the project directory:

```
tofu state pull > demo.tfstate                      # with the dev backend configuration
# change the backend block or the cloud block to the production server
tofu init -reconfigure
tofu state push demo.tfstate
```

`--show-secrets` writes the secret values in plain text to `demo.json`, so
that the production server encrypts them with its own key. The user
deletes `demo.json` and `demo.tfstate` after the import. The secret values
of the stack configuration use the key of `dev`, so the user
sets each one again on the production stack. The user deletes
`demo-config.json` too. Stack history, tags and update events do not move. RFC 66 holds
tooling to move units between installations. The dev data stays until the
user deletes the data directory.

### Threat model of the marker

The marker is a plain column. It stops a mistake: a copy of a test database,
a wrong path in a unit file, a `dev.env` in a production shell. It does not
stop a person with write access to the database, who can change the column.
That person can also read the data, so a stronger marker gives no
protection against them. The dev header in the key file closes one more
mistake: an operator who points `serve` at the key file of `dev`.

### `opendustpan doctor`

`doctor` reads the settings of `serve` through the same layers (flag,
environment, configuration file) and checks them. It does not open the
database and does not read the key file (see "Where the new commands
live"). With `--server`, it also asks a running server through the API for
the checks that need the server. `doctor` refuses to start when the
environment holds `DUSTPAN_DEV`.

| Check | Result when it fails |
| --- | --- |
| The operator set a key-encryption key. The key file exists, has mode 0600 or stricter, and has the size of a base64 key. | fail |
| The key file is not in the directory of the SQLite file or of the blobs, after link resolution. | warn |
| The operator set a database. | fail |
| The operator set an admin token of at least 32 bytes. | fail |
| The operator set a backup target. | warn |
| The listen address is not loopback, the operator set no TLS, and no `--public-url` with `https` names a proxy. | warn |
| With `--public-url`, the operator also set `--trusted-proxies`. | warn |
| With `--server`: the server reports `production`, `upgrade check` gives no `cannot start`, the last backup is less than 25 hours old. | fail, warn, warn |

`doctor` prints one line for each check, for people. It exits with 0 when
no check fails, and with 1 when a check fails. A pipeline uses the exit
code. A JSON form is not part of this RFC (product manager decision, below).

### What replaces the escape hatch

| Today | After this RFC |
| --- | --- |
| `serve --generate-key-encryption-key` for a trial with no terminal | `dev` |
| `serve` prompt on a terminal | `dev` for a trial, `key generate` for production |
| default key file `dustpan.key` next to the database | gone: `serve` needs an explicit key |
| warning of ADR 154 decision 10 on each start with the made key file | gone: `serve` never uses a key file that it made. A warning stays when the explicit key file is next to the data. The banner of `dev` says where the key is. |
| `scripts/demo.sh` with the flag | `dev --data-dir` |
| `serve` refuses no data | `serve` refuses dev data and a dev key, and `dev` refuses production data |

`dustpan.NewServer` gets the mode option, and `Server` gets `Start`. ADR 154
decision 10 keeps its rule that a running server always has a key: `dev`
always makes one, and `serve` always needs one.

## ADRs that change if the owner accepts this RFC

ADR 1 lets an accepted ADR change only in its status line. The new ADR
names each change, and each of these ADRs gets a status line in the ADR 1
form, for example "decision 12 superseded in part by ADR N":

| ADR | Decision | Status line | What changes |
| --- | --- | --- | --- |
| 23 | 2, "Format" | decision 2 superseded in part by ADR N | The server still never makes the key of `serve`. `dev` makes its own key in its data directory. The key file of `dev` starts with a dev header line before the 32 bytes in base64. The format of a key for `serve` does not change. |
| 39 | 1, "only the server opens the data" | decision 1 superseded in part by ADR N | Two server commands open data: `serve` and `dev`. `key generate` writes a new key file and opens no key of an installation. `doctor` opens no data and reads no key. |
| 39 | 4, "a gate, not only a rule" | decision 4 superseded in part by ADR N | The gate allows the wiring of `serve` and `dev`. The test that fails when a command other than `serve` opens a store names `dev` too. |
| 41 | 12, "the key, with no silent default" | decision 12 superseded in part by ADR N | The default key file path, the terminal prompt and `--generate-key-encryption-key` go from `serve`. `dev` makes its own key. The error text changes. The "upgrade check" item names the new settings. |
| 154 | 10, the escape hatch and its warning | decision 10 superseded in part by ADR N | The escape hatch goes to `dev`. The error text of `serve` changes. The warning for a made key file goes. A warning for an explicit key file next to the data stays. |

These ADRs do not change:

- ADR 27: `dev` serves TLS with `crypto/tls` and makes its leaf with
  `crypto/x509`, so it adds no dependency. `dev` reads its certificate once
  at start, as decision 1 and the consequences say for `serve`. The warning
  of decision 3 for plain HTTP on a non-loopback address applies to `dev`
  too.
- ADR 34: `GET /health/live` stays 204 and checks nothing, also before
  `Start` succeeded. `GET /health/ready` answers 503 until then, which is a
  readiness check of ADR 32 decision 3.
- ADR 35: the mode option and `Server.Start` are part of `pkg/dustpan`, the
  SDK entry point. The mode store is a port with a conformance suite in
  `pkg/store/storetest`. The layout does not change.
- ADR 30, ADR 38 and ADR 42: no backup of a dev installation exists, and
  the restore and the backup manifest do not change.
- ADR 73: this RFC lands before the first tag (see "Compatibility").

RFC 7 asks if `serve --role` replaces `serve` or sits beside it. This RFC
does not answer that question. `dev` runs every module in one process, as
the Temporal and Consul trial commands do.

`GLOSSARY.md` gets one term when the owner accepts the ADR: _dev
installation_ (an installation that `dev` made). The term _mode_ is near
the installation state `maintenance`. Open question 5 asks for a word.

## Personas

All personas are equal. Each row shows the first start and the normal run.

| Persona | First start | Normal run |
| --- | --- | --- |
| Homelab user | `opendustpan dev --data-dir ~/dustpan-trial`, then `. ~/dustpan-trial/dev.env`. The Pulumi CLI works at once. OpenTofu works at once through `backend "http"`, and through the `cloud` block at once or after one flag, as open question 3 decides. | When the trial becomes real: `opendustpan key generate --out ~/.config/opendustpan/dustpan.key`, then `serve --database sqlite:/srv/dustpan/dustpan.db --key-encryption-key-file ...` in a systemd unit, from a shell without `dev.env`. To keep the units, the user moves them with `pulumi stack export` and `import`, and `tofu state pull` and `push` (see "Dev data stays dev data"). |
| Small team | One person runs `opendustpan dev` on a laptop for a day. | The container image on one host with a mounted key file, SQLite or PostgreSQL, a backup target and OIDC sign-in. `opendustpan doctor` before the first start. |
| Startup | `opendustpan dev` locally, and in CI from day one. | PostgreSQL and S3 from a cloud provider, the key in a secret store mounted as a file, two instances behind a load balancer. `doctor --server` in the deploy pipeline after each upgrade. |
| Enterprise | A platform engineer runs `opendustpan dev` to read the API and the console. A security reviewer reads `serve --help`: every production start needs a key, a database and a token that the operator gave. | Configuration file from configuration management, the key from a secret store or a KMS (RFC 33), audit sinks, signed audit checkpoints, IP allowlists. `serve` refuses dev data and a dev key, so a copy of a test database cannot become production by mistake. No dev key and no dev token ever reaches production. |
| CI pipeline | `opendustpan dev --data-dir "$RUNNER_TEMP/dustpan" & pid=$!`, then a loop: `until opendustpan health --data-dir "$RUNNER_TEMP/dustpan"; do kill -0 "$pid" \|\| exit 1; sleep 1; done`, then `. "$RUNNER_TEMP/dustpan/dev.env"`. The `kill -0` check stops the loop when `dev` stopped with an error. When the job sets `DUSTPAN_DEV_ADMIN_TOKEN`, the job knows the token before the start, and `dev` never prints it. In a service container: `dev --listen 0.0.0.0:8080 --hostname dustpan`. The job sets the variables itself: `PULUMI_BACKEND_URL` and `DUSTPAN_SERVER` as `http://dustpan:8080`, the token from `DUSTPAN_DEV_ADMIN_TOKEN`, `TF_HTTP_USERNAME` and `TF_HTTP_PASSWORD`. | The same at each run. The data directory goes with the runner. No key, certificate or token is a CI secret. A production token in the job environment does not reach `dev`. |

## Compatibility

- **This RFC lands before the first tag.** ADR 73 decision 5 says that an
  installation of a tagged release upgrades with no manual step. The
  removal of the default database and the default key path forces a manual
  step on an installation that relied on them. So this change must merge
  before the first tag. If a tag ships first, the change needs an explicit
  exception to ADR 73 decision 5 in a new ADR, and the author must revise
  this RFC.
- **No alias.** ADR 73 decision 4 forbids an alias or a shim for an
  unreleased shape. `--generate-key-encryption-key`,
  `DUSTPAN_GENERATE_KEY_ENCRYPTION_KEY`, the file key, the prompt and the
  default database go with no deprecation.
- **No data conversion.** The `mode` column is a new migration file. It
  does not convert the data that an untagged build wrote: the first command
  claims the NULL mode (ADR 73 decision 6). The collapse before the first
  tag folds the column into the baseline.
- **The tool-compatible APIs do not change.** `dev` serves the same routes
  as `serve`. The Pulumi CLI and OpenTofu see no difference.
- **The e2e cost.** Two full runs of `make e2e`, one against `dev` and one
  against `serve`, double the time of the suite. So the full suite runs
  against `serve`, as today. One more e2e step starts `dev` with no
  settings and drives the real CLIs through it: `pulumi login`,
  `stack init` and `up`. `tofu init` and `apply` through `backend "http"`
  and through the `cloud` block. The refusal of a `serve` database by `dev`,
  and the reverse. The step adds one server start and one short run of each
  CLI to the suite.
- **The tool-neutral APIs.** The installation API gains the mode field.
  The console change (the band) merges before the next server tag (ADR 73
  decision 3).
- **The container image.** The `CMD` of the image stays `serve` with the
  database in `/data` (product manager decision, below). A `docker run` with
  no key stops with the key error. A trial runs
  `docker run -p 127.0.0.1:8080:8080 <image> dev --listen 0.0.0.0:8080`.
  The image sets `DUSTPAN_DATA_DIR=/data`, so `dev` keeps its data in
  `/data/dev` on the volume and the `HEALTHCHECK` finds it (see
  "`opendustpan health` and `dev`"). The Dockerfile makes `/data` owned by
  `nonroot` (see "The volume of the image").
- **The SDK.** `Handler` answers 503 until `Start` succeeded. No tag
  shipped, so no outside embedder depends on the current behavior (ADR 73
  decision 4). In this repository, the e2e harness (`e2e/harness.go`), the
  load harness (`load/main.go`) and the tests in `pkg/dustpan` and `e2e`
  that serve `Handler` without `Run` add a call to `Start` in the same pull
  request.
- **Documents and scripts.** The README quickstart, the configuration
  reference, the security model, the guide for several instances, the guide
  for backup and upgrade, `scripts/demo.sh` and the e2e setup change in the
  same pull request as the code.

## Decisions of the product manager

The product manager decided these questions on 2026-10-18, by the rule
"what Google, AWS, PCI DSS, SOC 2 and ISO 27001 would do". They are
product manager decisions, not answers of the project owner. The owner can
change each of them before the ADR.

- **No promotion of dev data (open question 7).** No command and no flag
  turns a dev installation into a production installation. Dev data is
  trial data. To keep units, the user moves them with the commands of each
  tool (`pulumi stack export` and `import`, `tofu state pull` and `push`)
  into a production server (see "Dev data stays dev data"). Reasons:
  Vault and Temporal dev modes have no promotion. A promotion must replace
  the dev key, because the dev key lived next to the data. A rewrap moves
  the wrapped data keys to a new key-encryption key, but the data keys stay
  the same, and every copy of the dev directory holds the dev key that
  opens them. So a rewrap is not a rekey. A full rekey of every data key
  and ciphertext is a large prerequisite for trial data. With no
  promotion, `serve` needs no dev token hash and no list of dev tokens to
  revoke, and `dev` stores neither.
- **`doctor` output (open question 6).** The exit code serves pipelines.
  No JSON form now.
- **Container default (open question 8).** The image fails closed: its
  `CMD` stays `serve`, as Keycloak `start` and the PostgreSQL image refuse
  to start without their settings.
- **Bind address of `dev` (open question 2).** Loopback by default.
  `--listen` accepts another address, with a banner line. The guides show
  `-p 127.0.0.1:8080:8080`. The certificate names come from the `--listen`
  host, `os.Hostname` and `--hostname`.
- **Backup of a dev installation.** No backup of a dev installation exists,
  because `dev` has no backup target.

## Open questions

Questions 2, 6, 7 and 8 have product manager decisions (above). The
questions for the owner are 1, 3, 4 and 5.

### Open question 1: where does `dev` keep its data by default?

This question applies when the user sets no `--data-dir` and no
`DUSTPAN_DATA_DIR`. The image sets `DUSTPAN_DATA_DIR`, so `dev` keeps
its data in `/data/dev` on the volume and never deletes it. A
`docker run --rm` deletes an anonymous volume with the container, and with
it the state of the trial. The guides show a named volume.

- (a) A temporary directory, deleted at a clean stop. As Vault and
  Temporal. A CI run leaves nothing. But Vault and Temporal lose only
  their own data. A state backend that loses state at Ctrl-C leaves the
  cloud resources of the trial with no state, and they cost money. The
  homelab user is worse off than today.
- (b) A directory per user, for example `$XDG_DATA_HOME/opendustpan/dev`.
  Data survives a restart. A CI run leaves files on a reused runner,
  unless the job sets `--data-dir "$RUNNER_TEMP/..."`.
- (c) `./.opendustpan-dev` in the working directory. Easy to find. A user
  might commit it to git with the key in it.
- (d) As (a), but at stop, when the installation holds resources, `dev`
  keeps the directory and prints its path and the `--data-dir` command to
  use it again. The installation holds resources when one of these is true
  at stop:
  - The latest state of a stack or a workspace has at least one resource.
    `dev` counts the resources in each state. For a Pulumi stack, the count
    leaves out the stack resource and the provider resources, because they
    are not cloud resources.
  - An update of a stack is in progress.
  - A workspace has a lock.

Recommendation: (d), because it leaves nothing after a CI run with no
resources and never deletes the state of real resources.

### Open question 3: does `dev` serve TLS by default?

OpenTofu has two ways to reach `dev`. The `http` backend works over plain
HTTP, so OpenTofu through `backend "http"` needs no certificate step. The
TFE-compatible API (the `cloud` block) needs HTTPS, because OpenTofu
refuses plain HTTP for service discovery. The Pulumi CLI works over plain
HTTP on every platform today with zero certificate steps, and on macOS and
Windows it is worse off under any option that is TLS only (see "TLS in
`dev`").

- (a) Yes, TLS only. OpenTofu works at once on Linux through both
  backends. On macOS and Windows, the user might need to trust the
  certificate by hand, also for the Pulumi CLI.
- (b) No. Plain HTTP only. The Pulumi CLI and OpenTofu through
  `backend "http"` work at once on every platform. The `cloud` block does
  not work.
- (c) Both: HTTP on one port, loopback only, and HTTPS on the next port.
  Two ports: `dev.addr`, `dev.env`, the banner and the health probe name
  both, and a container publishes two ports.
- (d) HTTP by default, plus a `--tls` flag that turns on HTTPS. This is
  the pattern of Vault `-dev-tls`. The Pulumi CLI and `backend "http"`
  work at once. The `cloud` block needs the flag.

Each option that serves TLS, (a), (c) and (d), has a second choice for the
form of the certificate: one self-signed leaf with no CA (see "TLS in
`dev`"), or a CA with name constraints when a client refuses the leaf.

Recommendation: (d) with the self-signed leaf. The Pulumi CLI and
OpenTofu through `backend "http"` keep zero steps on every platform. The
`cloud` block needs one flag and no manual certificate step on Linux.

### Open question 4: does `dev` create a namespace?

- (a) Yes, `dev`.
- (b) Yes, `default`, as Temporal.
- (c) No. The user runs `namespace create`.

Recommendation: (a), because the name repeats the warning in every stack
and workspace name. A unit that moves to a production server gets the name
that the user gives the new stack or workspace at the import. A user who
keeps the names keeps the `dev/` prefix.

### Open question 5: the word for the marker

- (a) _mode_ (`dev`, `production`). Short. Near the installation state
  `maintenance`, which a reader might confuse with it.
- (b) _purpose_ (`dev`, `production`).
- (c) _origin_ (`dev`, `serve`): names the command that made the data. With
  no promotion, the origin never changes. But an SDK embedder gets the mode
  `production` with no `serve` command, so the value `serve` would name a
  command that did not run.

Recommendation: (b), because it does not collide with the installation
state, and its values stay true for an SDK embedder.

## Outcome

Empty until the owner accepts or drops the RFC.
