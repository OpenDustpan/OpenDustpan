# 238. `opendustpan serve` needs decisions: an explicit key and database, `key generate` and `doctor`

Status: accepted · 2026-10-18. Accepts RFC 75 with ADR 237. Supersedes in part ADR 39 decisions 1 and 4, ADR 41 decision 12 and ADR 154 decision 10

Changed by:

- ADR 315 extends: the quickstart.
- ADR 356 changes decision 3: the trial variant of the message for a missing database goes.

## Context

`serve` has defaults that suit a trial and are wrong for production: the
database `sqlite:dustpan.db` in the working directory, the key file
`dustpan.key` next to the database, and a prompt that makes that key file.
A key file next to the data does not protect a backup of that data. ADR 41
decision 12 added `--generate-key-encryption-key` for a trial with no
terminal. ADR 154 decision 10 calls it the escape hatch for development and
logs a warning at each start.

RFC 75 proposes a `dev` command for a trial and a `serve` command that takes
none of the decisions that only the operator can take. On 18 October 2026
the project owner accepted the proposal. ADR 237 records the `dev` command
and the installation kind. This ADR records the changes to `serve`, two new
commands, the container default and the start gate of the SDK.

A decision of the product manager has the label "(product manager)" and a
"Benchmark:" line. The product manager decided under the owner's rule:
follow what Google, AWS, PCI DSS, SOC 2 and ISO 27001 would do. The other
decisions are the design of RFC 75 that the owner accepted with the
proposal.

Facts:

- No tag shipped. ADR 73 decision 4 applies to each command, flag and
  setting below: no alias and no shim for a shape that no tag shipped. ADR
  73 decision 5 asks that a tagged installation upgrades with no manual
  step. So this change must merge before the first tag.
- `dustpan.NewServer` reads no database. `Server.Run` runs the start-up
  steps before it listens. `Server.Handler` returns the handler and runs no
  start-up step.
- `serve` has 42 flags and 12 settings that only an environment variable
  sets.

## Decision

### `serve`

1. **An explicit key.** `serve` needs `DUSTPAN_KEY_ENCRYPTION_KEY`,
   `--key-encryption-key-file` (`DUSTPAN_KEY_ENCRYPTION_KEY_FILE`), or a KMS
   key-encryption key when that setting exists (ADR 217). The
   default key file path, the terminal prompt and
   `--generate-key-encryption-key` (`DUSTPAN_GENERATE_KEY_ENCRYPTION_KEY`)
   go, with no deprecation. With no key, `serve` stops before it opens the
   database. ADR 41 decision 12 and ADR 154 decision 10 change in part.
   ADR 154 decision 10 keeps its rule that a running server always has a
   key.
2. **An explicit database.** `--database` has no default. With no database,
   `serve` stops. The default of the blob directory stays next to the SQLite
   file, because the database setting already names a place that the
   operator chose.
3. **The errors for a missing key or database.** `serve` checks the key
   first, then the database, and reports only the first missing one. Each
   error names one command. When the operator set no setting of `serve` and
   stderr is a terminal, the person most likely wants a trial:

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

   When the KMS key-encryption key setting exists (ADR 217), the error names
   that setting too, and a KMS key counts as a key.

   For the trial hint, each of these counts as a setting of `serve`: a flag
   of `serve`, a key in a configuration file, `--config`, `DUSTPAN_CONFIG`
   and each `DUSTPAN_` variable of a setting of `serve`. These do not count,
   because a shell often exports them for other commands or other tools:
   `DUSTPAN_ADMIN_TOKEN`, `DUSTPAN_ADMIN_TOKEN_FILE`, `DUSTPAN_SERVER`,
   `DUSTPAN_DEV`, `DUSTPAN_DATA_DIR` and the AWS variables. `serve` checks
   stderr, not stdin, because the error goes to stderr.
4. **A warning for a key next to the data.** When the key file is in the
   directory of the SQLite file or of the blobs, `serve` logs a warning at
   each start. `serve` resolves symbolic links before the check. The
   warning of ADR 154 decision 10 for a key file that `serve` made goes,
   because `serve` makes no key file.
5. **`serve` refuses dev data.** `serve` refuses to start in each of these
   cases:
   - The key file starts with the dev header of ADR 237 decision 10.
   - The environment holds `DUSTPAN_DEV`:

     ```
     opendustpan: DUSTPAN_DEV is set. The dev.env file of opendustpan dev sets it. Start serve in a new shell.
     ```

   - The installation kind of the database is `dev` (ADR 237 decisions 25
     and 26).
6. **The listen address stays `:8080`.** A container and a host behind a
   proxy both need a non-loopback address, and the address is not a secret.
   `doctor` warns about plain HTTP on a non-loopback address.
7. **The usage text in groups.** The usage text lists the commands in
   groups by intent: "Try out" (`dev`), "Run in production" (`serve`,
   `key generate`, `doctor`), "Operate", "Back up" and "Upgrade". The names
   of the existing client commands do not change.

### `key generate`

8. **`key generate --out FILE`.** The command writes 32 random bytes from
   `crypto/rand` in base64 to `FILE`, with mode 0600. It refuses a file that
   exists. It prints the path and the warning that a lost key loses every
   secret. The operator chooses the path, so the decision about where the
   key lives stays with the operator. A host with no `openssl` can make a
   key too.
9. **Where it lives.** `key generate` sits in `internal/cli` with the client
   commands. It does not import the key-encryption key code of
   `pkg/secret`. It opens no store and reads no key, so ADR 39 decision 4
   holds for it. ADR 39 decision 1 changes in its text: a command that
   writes a new key file does not open the key-encryption key of an
   installation.

### `doctor`

10. **What `doctor` checks.** `doctor` reads the settings of `serve` through
    the same layers (flag, environment, configuration file) and checks them.
    It refuses to start when the environment holds `DUSTPAN_DEV`.

    | Check | Result when it fails |
    | --- | --- |
    | The operator set a key-encryption key. The key file exists, has mode 0600 or stricter, and has the size of a base64 key. For a KMS key, `doctor` checks only that the operator set it. | fail |
    | The key file is not in the directory of the SQLite file or of the blobs, after link resolution. | warn |
    | The operator set a database. | fail |
    | When the operator set an admin token, it has at least 32 bytes. | fail |
    | The operator set no admin token: doctor prints that no bootstrap admin token is set. | pass |
    | The operator set a backup target. | warn |
    | The listen address is not loopback, the operator set no TLS, and no `--public-url` with `https` names a proxy. | warn |
    | With `--public-url`, the operator also set `--trusted-proxies`. | warn |
    | With `--server`: doctor sends the token of the client commands, and the capabilities document reports `installationKind` `production`. A missing field fails the check. | fail |
    | With `--server`: `upgrade check` gives no `cannot start`. | warn |
    | With `--server`: the last backup is less than 25 hours old. | warn |

11. **Where it lives.** `doctor` sits in `internal/cli`. It calls `stat` on
    the key file for the owner, the mode, the size and the path. It does not
    read the file. It does not open the database. It reads the token length
    from the setting layers, which the client commands already read. With
    `--server`, it asks the running server through the API. So ADR 39
    decisions 1 and 4 hold for it.
12. **The result is the exit code (product manager).** `doctor` prints one
    line for each check, for people. It exits with 0 when no check fails,
    and with 1 when a check fails. A pipeline reads the exit code. `doctor`
    has no JSON form now.
    Benchmark: the common convention of command-line tools. A check command
    reports to a pipeline with its exit code.

### The container image

13. **The image fails closed (product manager).** The `CMD` of the image
    stays `serve` with the database in `/data`. A `docker run` with no key
    stops with the key error. A trial runs
    `docker run -p 127.0.0.1:8080:8080 <image> dev --listen 0.0.0.0:8080`.
    The image sets `DUSTPAN_DATA_DIR=/data` (ADR 237 decisions 4 and 18).
    Benchmark: `kc.sh start` of Keycloak refuses to start without its host
    name and HTTPS settings. The PostgreSQL image refuses to start without
    `POSTGRES_PASSWORD`.

### The start gate of the SDK

14. **`Server.Start` and a gated `Handler`.** A new exported method,
    `Server.Start(ctx) error`, runs the start-up steps: the key check, the
    claim of the installation kind (ADR 237 decision 26), the running
    backups and restores with the fence, the audit chain and the sign-in
    providers. `Run` calls `Start`. `Start` runs once, so `Run` after a
    successful `Start` does not repeat the steps. An embedder that serves
    `Handler` calls `Start`.
15. **`Handler` answers 503 until `Start` succeeded.** Each route answers 503
    until then, except `GET /health/live`, which stays 204 (ADR 34). `GET
    /health/ready` answers 503 in the same time. An embedder can forget a
    call, and a forgotten check fails open. With the gate, a forgotten
    `Start` gives 503 at the first request. The gate also covers
    `secret.CheckWrapper`, which an embedder of `Handler` skips today.

### What replaces the escape hatch

16. **The replacements.**

    | Today | After this ADR |
    | --- | --- |
    | `serve --generate-key-encryption-key` for a trial with no terminal | `dev` |
    | The prompt of `serve` on a terminal | `dev` for a trial, `key generate` for production |
    | The default key file `dustpan.key` next to the database | Removed: `serve` needs an explicit key |
    | The warning for a key file that `serve` made | Removed. A warning stays for an explicit key file next to the data. |
    | `scripts/demo.sh` with the flag | `dev --data-dir` |
    | `serve` refuses no data | `serve` refuses dev data and a dev key, and `dev` refuses production data |

## Consequences

- Each start of `serve` is a production start. A security reviewer reads
  `serve --help` and sees that each start needs a key, a database and a
  token that the operator gave.
- An operator of an untagged build who relied on the default database or
  the default key file sets both settings explicitly before the next start.
  No tag shipped, so no tagged installation relies on them.
- The README quickstart, the configuration reference, the security model,
  the guide for several instances, the guide for backup and upgrade,
  `scripts/demo.sh`, the image and the e2e setup change in the same pull
  request as the code.
- The e2e harness, the load harness and the tests that serve `Handler`
  without `Run` add a call to `Start` in the same pull request.
- If a tag ships before this change, the change needs an explicit exception
  to ADR 73 decision 5 in a new ADR.
- The tool-compatible APIs do not change. `dev` and `serve` serve the same
  routes.
- ADR 27, ADR 34 and ADR 35 do not change. The kind option and
  `Server.Start` are part of `pkg/dustpan`, the entry point of the SDK.
