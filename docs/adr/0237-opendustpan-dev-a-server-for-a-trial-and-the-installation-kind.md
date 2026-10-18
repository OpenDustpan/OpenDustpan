# 237. `opendustpan dev`: a server for a trial or for CI, and the installation kind

Status: accepted · 2026-10-18. Accepts RFC 75 with ADR 238. Supersedes in part ADR 23 decision 2 and ADR 39 decisions 1 and 4

## Context

Today one command, `serve`, has two jobs. A trial needs a server with no
setup. Production needs a server that takes no decision for the operator.
A person who wants a trial must make an admin token and a key-encryption
key before the first start. OpenTofu through the TFE-compatible API also
needs a TLS certificate. So the README quickstart runs `openssl` three
times. The escape hatch `--generate-key-encryption-key` (ADR 41 decision
12, ADR 154 decision 10) is a development aid on the production command.

RFC 75 compares four options and proposes two server commands, one for each
intent. On 18 October 2026 the project owner accepted that proposal and
answered open questions 1, 3, 4 and 5 of the RFC. The owner also decided
what the first run prints and which files hold the credentials of the CLIs.
The product manager decided open questions 2, 6, 7 and 8 and the backup
question under the owner's rule: follow what Google, AWS, PCI DSS, SOC 2
and ISO 27001 would do.

This ADR records the `dev` command and the marker that keeps the data of
`dev` and `serve` apart. ADR 238 records the changes to `serve`, the
`key generate` and `doctor` commands, the container default and the SDK
start gate. A decision of the owner has the label "(owner)". A decision of
the product manager has the label "(product manager)" and a "Benchmark:"
line. The other decisions are the design of RFC 75 that the owner accepted
with the proposal.

Facts:

- No tag shipped. ADR 73 decision 4 applies: no alias and no shim for a
  shape that no tag shipped.
- OpenTofu refuses plain HTTP for service discovery. So the TFE-compatible
  API (the `cloud` block) needs HTTPS. The OpenTofu `http` backend works
  over plain HTTP. The Pulumi CLI works over plain HTTP.
- In Go, `SSL_CERT_FILE` replaces the system root store. It does not add
  to it. Since Go 1.27, the variable also replaces the platform verifier on
  macOS and Windows.
- OpenTofu reads a `TF_TOKEN_` variable by host name only. Its encoding has
  no form for a port.
- The container image declares `VOLUME /data`, runs as the user `nonroot`
  (user ID 65532) and has no `/data` directory.
- The baseline migration of each dialect inserts the one `installation`
  row. A backup leaves out the `installation` table, and a restore keeps
  the live row.

## Decision

### The command

1. **Two server commands (owner).** The binary gets a second server
   command, `opendustpan dev`, for a trial or for CI. `dev` takes every
   decision for the user and says so. `serve` stays the command for
   production (ADR 238). A flag on `serve` was not chosen. Each setting of
   `serve` has an environment variable and a key in the configuration file,
   so a production file could turn on a dev flag by mistake.
2. **Same server code.** `dev` runs the server code of `serve` with its own
   defaults and its own small set of flags. Its wiring sits next to the
   wiring of `serve` in `cmd/opendustpan`, and it may import what `serve`
   imports. ADR 39 decisions 1 and 4 name `serve` and `dev` from now on.
   The test that fails when a command other than `serve` opens a store
   allows `dev` too.
3. **The settings of `dev`.** `dev` takes `--data-dir`, `--ephemeral`,
   `--listen`, `--tls`, `--hostname`, `--admin-token-file`,
   `--console-dir`, `--log-level`, `--log-format`,
   `--disable-pulumi-journaling` and the size limits. So a CI job can test
   the size limits of production. `dev` does not take `--config`, the
   database, the blob store, the backup target, a key setting, the audit
   sinks, OIDC or the proxy settings. A user who needs those runs `serve`.

### The data directory

4. **A directory for each user by default (owner).** `dev` keeps its data
   so that a stop of `dev` never loses the state of real cloud resources.
   `dev` finds its data directory in this order:
   1. `--data-dir DIR`: the directory is `DIR`.
   2. `DUSTPAN_DATA_DIR`: the variable names a parent directory, for
      example the volume `/data` of the image. The directory is
      `<parent>/dev`. `dev` never writes outside this subdirectory.
   3. `--ephemeral`: a new temporary directory (decision 5).
   4. None of these: the directory for each user.

   The directory for each user is:

   | Platform | Directory |
   | --- | --- |
   | Linux and other Unix | `$XDG_DATA_HOME/opendustpan/dev`, or `~/.local/share/opendustpan/dev` when `XDG_DATA_HOME` is not set |
   | macOS | `~/Library/Application Support/opendustpan/dev` |
   | Windows | `%LocalAppData%\opendustpan\dev` |

   Benchmark: the XDG Base Directory Specification, and the data
   directories of Apple and Microsoft for each user.
5. **`--ephemeral` (owner).** `--ephemeral` makes a new directory with
   `os.MkdirTemp`. `dev` deletes it at a clean stop. The banner and a log
   line at the start state that a stop deletes the data, and with it the
   state that tracks the cloud resources of the trial. `dev` logs the warning
   again at stop. `--ephemeral` and `--data-dir` exclude each other, and
   `dev` refuses to start with both.
6. **Ownership and file modes.** `dev` makes the data directory with mode
   0700. It sets mode 0700 on an empty directory that it claims. A
   directory that holds only `lost+found` at a mount root counts as empty.
   It makes the key
   file, the token file, `dev.addr`, `dev.env`, `dev.tfrc` and each private
   key with mode 0600. At each start, `dev` refuses to start when one of
   these is true:
   - The group or others can read, write or enter the data directory.
   - The current user ID does not own the data directory, the key file,
     `dev.addr`, `dev.env` or `dev.tfrc`.

   The error names the path, its mode or owner, and one command:
   `chmod 700 <dir>`, or `chown <uid> <path>` with the current user ID.
7. **The volume of the image.** The build stage of the image makes an
   empty directory. The final stage copies it with
   `COPY --from=build --chown=nonroot:nonroot /empty /data` before
   `VOLUME /data`. So a new named volume belongs to `nonroot`. The guide
   sets `fsGroup: 65532` for Kubernetes and names the owner for a bind
   mount. For a directory that `nonroot` cannot write, `dev` stops with an
   error that names the path and the user ID 65532. The image smoke test
   checks that `dev` starts on a new named volume.
8. **Only its own data.** `dev` opens a database only in a data directory
   that holds a key file of `dev` (decision 10). `dev` checks this before it
   opens or migrates any file:
   - A directory that does not exist, or that is empty: `dev` makes the key
     file first, then the database.
   - A directory with files but with no key file of `dev`: `dev` stops and
     opens no file in it.

   ```
   opendustpan: /srv/data holds files but no key file of opendustpan dev: dev refuses it. Set a new --data-dir.
   ```

   So `dev --data-dir` on the directory of a production database never
   opens, migrates or claims that database.
9. **Files in the data directory.** The directory holds the SQLite
   database, the blobs, the key file, the token file, the TLS certificate
   and its key when `--tls` is on, `dev.addr`, `dev.env` and `dev.tfrc`.
   `dev` uses the directory again at the next start.

### Credentials

10. **The key of `dev`.** `dev` makes a key file in the data directory at
    the first start. The file starts with a dev header line, and the 32
    random bytes in base64 follow it. `serve` refuses a key file with this
    header (ADR 238). `DUSTPAN_KEY_ENCRYPTION_KEY` and
    `--key-encryption-key-file` are not settings of `dev`. This changes ADR
    23 decision 2 in part. The server still never makes the key of `serve`.
    `dev` makes its own key, and the banner says where it is.
11. **The admin token.** `dev` never reads `DUSTPAN_ADMIN_TOKEN` or
    `DUSTPAN_ADMIN_TOKEN_FILE`. An operator shell or a CI job often exports
    the production token in those variables. `dev` clears both from its
    process environment at start. A job that must choose the token sets
    `DUSTPAN_DEV_ADMIN_TOKEN` or `--admin-token-file FILE`. `dev` clears
    `DUSTPAN_DEV_ADMIN_TOKEN` from its process environment after it reads
    the variable. Else `dev` makes a token at the first start and keeps it
    in the token file.
12. **A one-time sign-in link (owner).** At the first start of a data
    directory, `dev` prints a sign-in link for the console. The link holds a
    random code. The code signs one browser in to the console as the admin
    of the dev installation. It works one time, and it stops working when
    `dev` stops. `dev` keeps only a hash of the code. A later start prints
    no link. The banner never prints the admin token. When `dev` serves no
    console, the banner names `--console-dir` in place of the link.
13. **Credentials of the CLIs in `dev.env` and `dev.tfrc` (owner).** `dev`
    writes the credentials of the CLIs to two files in the data directory.
    `dev.env` is a POSIX `sh` file. A user runs `. <data dir>/dev.env`, and
    the Pulumi CLI, OpenTofu and the client commands of `opendustpan` work
    in that shell. `dev.env` sets:
    - `PULUMI_BACKEND_URL` and `DUSTPAN_SERVER` to the URL of the server.
    - `TF_CLI_CONFIG_FILE` to `dev.tfrc`.
    - `TF_HTTP_USERNAME=dustpan`. OpenTofu sends basic authentication only
      with a user name, and the server ignores the user name.
    - `DUSTPAN_DEV=1` (decision 14).
    - `SSL_CERT_FILE` when `--tls` is on (decision 17).

    The token in the files depends on its source:
    - A token that `dev` made: `dev.env` sets `DUSTPAN_ADMIN_TOKEN`,
      `PULUMI_ACCESS_TOKEN` and `TF_HTTP_PASSWORD` to it. `dev.tfrc` holds it
      in a `credentials` block.
    - A token from `DUSTPAN_DEV_ADMIN_TOKEN`: `dev.env` holds no token. It
      sets the three variables from `DUSTPAN_DEV_ADMIN_TOKEN` in the shell
      that loads it, for example
      `export DUSTPAN_ADMIN_TOKEN="${DUSTPAN_DEV_ADMIN_TOKEN:?}"`. `dev.tfrc`
      holds the token in a `credentials` block, because no `TF_TOKEN_`
      variable can name `localhost:8080`.
    - A token from `--admin-token-file`: `dev.env` sets
      `DUSTPAN_ADMIN_TOKEN_FILE` to that path and holds no token. The banner
      names the variables that the user sets for the Pulumi CLI and
      OpenTofu, because they read no token file.
14. **The guard against the reverse path.** `DUSTPAN_DEV` is not a setting.
    It has no flag and no key in the configuration file. Only `serve` and
    `doctor` read it, and both refuse to start when the environment holds
    it (ADR 238). So `. dev.env` and then `opendustpan serve` in the same
    shell cannot give a production server the token of `dev`.

### The network

15. **Loopback by default (product manager).** `dev` binds
    `127.0.0.1:8080` by default. `--listen` accepts another address. A
    non-loopback address adds a banner line that names the address. A
    container needs `--listen 0.0.0.0:8080`. The documented container
    command publishes the port on the host loopback address only:
    `docker run -p 127.0.0.1:8080:8080 <image> dev --listen 0.0.0.0:8080`.
    Benchmark: `vault server -dev` and `temporal server start-dev` bind
    loopback by default and need a flag for another address.
16. **HTTP by default, and `--tls` (owner).** `dev` serves plain HTTP by
    default. The Pulumi CLI and OpenTofu through `backend "http"` then work
    on every platform with no certificate step. `--tls` turns on HTTPS, as
    `vault server -dev-tls` does. The `cloud` block of OpenTofu needs
    `--tls`.
17. **The certificate of `--tls` (owner).** With `--tls`, `dev` makes one
    self-signed leaf certificate and no certificate authority. `dev` never
    changes a system or user trust store, also not with a flag.
    - The leaf key has mode 0600 in the data directory. The leaf lives for
      30 days.
    - `dev` makes a new leaf at a start when less than 7 days remain. It does
      not renew the leaf while it runs, as `serve` reads its certificate once
      at start (ADR 27). The banner prints the expiry date.
    - The leaf names `localhost`, `127.0.0.1`, `::1`, the host of `--listen`
      when it is not a wildcard, the name from `os.Hostname` and each
      `--hostname NAME`. The flag can repeat. A CI job that reaches a service
      container by its service name sets `--hostname dustpan`.
    - On Linux, `dev` writes a bundle file and `dev.env` points
      `SSL_CERT_FILE` at it. The bundle holds the roots that the shell of
      `dev` uses at the start, then the dev certificate. The roots come from
      `SSL_CERT_FILE`, from each directory in `SSL_CERT_DIR`, or from the
      first root file that Go reads on that platform. `dev.env` then clears
      `SSL_CERT_DIR`. So provider and plugin downloads keep their roots.
    - On macOS and Windows, `dev.env` does not set `SSL_CERT_FILE`, because
      no file holds the platform roots. The banner prints the command that
      trusts the certificate on that platform, and the user runs it.
18. **`health` finds `dev`.** `dev` deletes `dev.addr` at start, before it
    listens. When it listens, it writes `dev.addr` with mode 0600. The file
    holds an address that a client can dial, the path of the certificate,
    the process ID and the start time of the process. `dev` writes
    `127.0.0.1` for a wildcard host, and an IPv6 address as `[addr]:port`.
    `dev` deletes `dev.addr` at stop.
    - `health --data-dir DIR` reads `DIR/dev.addr` and probes that address
      with that certificate. It first checks that the current user ID owns
      `DIR` and `dev.addr`, and that the group and others cannot write them.
    - A missing `dev.addr`, a missing certificate or a process ID with
      another start time is "not ready", with exit code 1.
    - The image sets `DUSTPAN_DATA_DIR=/data`. The `HEALTHCHECK` stays
      `["/opendustpan", "health"]`. When `/data/dev/dev.addr` exists,
      `health` probes `dev`. Else it probes with the settings of `serve`.

### What a dev installation holds

19. **The namespace `dev` (owner).** `dev` creates the namespace `dev` when
    it does not exist. So the first `pulumi stack init dev/demo/dev` or
    `tofu init` works with no other command. The name repeats the warning
    in each stack and workspace name.
20. **No backup (product manager).** `dev` takes no `--backup-target`. So
    no backup of a dev installation exists, and no restore of dev data
    exists. The `backup` command against `dev` gets the error of a server
    with no target. ADR 30, ADR 38 and ADR 42 do not change.
    Benchmark: `vault server -dev` and `temporal server start-dev` keep dev
    data in memory or in one local file and have no backup of it.
21. **No promotion of dev data (product manager).** No command and no flag
    turns a dev installation into a production installation. To keep the
    units of a trial, the user moves them to a production server with the
    commands of each tool:
    - Pulumi CLI: `pulumi stack export --show-secrets` and
      `pulumi stack import`. The secret values of the stack configuration
      use the key of `dev`, so the user sets each one again on the new
      stack.
    - OpenTofu: `tofu state pull`, a change of the backend or `cloud` block,
      `tofu init -reconfigure` and `tofu state push`.

    The user deletes the exported files after the import, because they hold
    secret values in plain text. Stack history, tags and update events do
    not move.
    Benchmark: the dev modes of Vault and Temporal have no promotion. A
    promotion must replace a key that lived next to the data. A rewrap is
    not enough, because each copy of the dev directory opens the data keys.
    The project designs its key handling against PCI DSS requirement 3.7,
    which asks for the replacement of a key that others may know. That
    needs a full rekey of each data key and ciphertext, a large
    prerequisite for trial data. The escape hatch is the export and import
    of each tool.
22. **The banner.** At each start, `dev` writes this text to stderr before
    the first log line, also with `--log-format json`:

    ```
    opendustpan dev: this server is for a trial or for CI. Do not use it in production.
    Data:     /home/alex/.local/share/opendustpan/dev
    Server:   http://127.0.0.1:8080
    Sign in:  http://127.0.0.1:8080/sign-in#code=<code>   (first start only, works one time)
    Settings: . /home/alex/.local/share/opendustpan/dev/dev.env
    The key-encryption key is in the data directory. A backup of this directory holds the key.
    For production, run: opendustpan serve --help
    ```

    The exact path of the sign-in link is part of the implementation. With
    `--ephemeral`, the `Data` line states that `dev` deletes the directory
    at stop.

### The installation kind

23. **The name (owner).** The marker is the _installation kind_. Its values
    are `dev` and `production`. The owner rejected "mode", "purpose" and
    "origin". "Mode" is near the installation state `maintenance`.
24. **The column.** The `installation` table gets one column, `kind`, with
    the values `dev`, `production` and NULL. The baseline migration inserts
    the row with `kind` NULL, and no column default sets a kind. So a crash
    between the migration and the claim leaves NULL, never a wrong kind. On
    a database from an untagged build, the new migration gives the row the
    kind NULL, and the first command claims it. This converts no data (ADR
    73 decision 6).
25. **The check before the migrations.** `sqlite.Open` and `postgres.Open`
    get an option with the kind of the command. When the `kind` column
    exists, the adapter reads the kind before it applies a migration. When
    the stored kind is not NULL and differs from the kind of the command,
    the adapter returns an error and applies no migration. When the column
    does not exist yet, the adapter applies the migrations and the claim
    decides. An adapter with no kind option does no check before the
    migrations, and the claim decides.
26. **The claim.** The claim is the first start-up step, before any step
    that writes and before the listener opens. It is one compare-and-set:
    `UPDATE installation SET kind = ? WHERE id = 1 AND kind IS NULL`. The
    server then reads the kind. When the stored kind differs from the kind
    of the command, the start stops. Several instances of `serve` on one new
    database all claim `production`, so their order does not matter. No API
    and no command changes the kind after the claim.
27. **The port.** A new domain package, `pkg/installation`, holds the type
    `Kind` with `KindDev` and `KindProduction`, and the port `KindStore`
    with one method:

    ```go
    // ClaimKind stores kind when the stored kind is NULL, and returns the
    // stored kind. It returns ErrUnavailable when the row is missing, when
    // the kind cannot be read, or when the stored value is not a known kind.
    ClaimKind(ctx context.Context, kind Kind) (Kind, error)
    ```

    `maintenance.Store` keeps the installation state only. The conformance
    tests land in `pkg/store/storetest` before an adapter implements the
    port. They check that a claim on NULL stores the kind, and that a second
    claim with another kind returns the first kind and changes nothing. They
    also check that two claims at the same time store one kind, and that an
    unknown stored value gives `ErrUnavailable`.
28. **The kind in the SDK.** `dustpan.NewServer` gets two options:
    `dustpan.WithInstallationKind(kind)` and an option for the kind store.
    `NewServer` reads no database, as today. `cmd/opendustpan` passes
    `production` for `serve` and `dev` for `dev`. With no kind option,
    `NewServer` uses `production`, so an SDK user gets the production rules.
29. **Visible in the API, the console and the audit log.** The installation
    API reports the field `kind`. The console shows a band on each page
    while the kind is `dev`. The audit entry of each start records the kind.
30. **The errors.**

    ```
    opendustpan: opendustpan dev made this database: serve refuses it. To start over, set a new --database.
    ```

    ```
    opendustpan: opendustpan serve made this database: dev refuses it. Set a new --data-dir.
    ```

31. **The threat model.** The kind is a plain column. It stops a mistake: a
    copy of a test database, a wrong path in a unit file, or a `dev.env` in
    a production shell. It does not stop a person with write access to the
    database. That person can also read the data, so a stronger marker
    gives no protection against them. The dev header of the key file stops
    one more mistake: an operator who points `serve` at the key file of
    `dev`.

## Consequences

- A trial needs zero steps before the first start: `opendustpan dev`, then
  `. <data dir>/dev.env`. A CI job starts `dev --data-dir "$RUNNER_TEMP/..."`
  and waits on `health --data-dir`. No key, certificate or token is a CI
  secret.
- A CI job on a reused runner that sets no `--data-dir` and no
  `--ephemeral` leaves files in the directory for each user.
- The `cloud` block of OpenTofu needs `--tls`. On macOS and Windows, it also
  needs the trust command that the banner prints. An e2e run with real CLI
  builds on macOS and Windows must check this before the guide promises it.
- If a client refuses a self-signed leaf, a new ADR must decide the
  replacement.
- The console needs the sign-in route of the one-time link and the band for
  the kind `dev`. The console change merges before the next server tag (ADR
  73 decision 3).
- A user who keeps the names of the units after a move keeps the `dev/`
  prefix.
- One e2e step starts `dev` with no settings and drives the real CLIs:
  `pulumi login`, `stack init` and `up`, and `tofu init` and `apply`
  through `backend "http"` and, with `--tls`, through the `cloud` block. It
  also checks that `dev` refuses a `serve` database and the reverse. The full
  suite still runs against `serve`.
- `GLOSSARY.md` gets the terms _installation kind_ and _dev installation_.
- RFC 7 asks whether `serve --role` replaces `serve`. This ADR does not
  answer that question. `dev` runs every module in one process.
