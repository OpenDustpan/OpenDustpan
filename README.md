# OpenDustpan

A self-hosted backend for infrastructure-as-code state. Works with the Pulumi
CLI and works with OpenTofu.

[![CI](https://github.com/OpenDustpan/OpenDustpan/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/OpenDustpan/OpenDustpan/actions/workflows/ci.yml)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)

**Status: pre-alpha.** One node only. No release exists yet. Do not keep
production state on it.

<!-- Image: one real terminal recording of `pulumi login` and `tofu apply`
against this server. It waits for an approved recording tool. -->

OpenDustpan keeps state, history, locks and secrets on infrastructure that you
control. Each CLI connects through the remote-backend protocol that it already
speaks, so the CLI needs no plugin. All tools share one set of namespaces,
tokens and storage.

## Quickstart

The [quickstart guide](docs/guides/quickstart.md) shows the setup with SQLite on
loopback and a compose stack with PostgreSQL and S3-compatible storage. This
section shows the setup from source with SQLite.

No release exists yet, so build the server from source. You need Go 1.27.3 or
later, OpenSSL, and the Pulumi CLI or OpenTofu.

```sh
go build -o bin/opendustpan ./cmd/opendustpan
export DUSTPAN_ADMIN_TOKEN=$(openssl rand -hex 32)
export DUSTPAN_KEY_ENCRYPTION_KEY=$(openssl rand -base64 32)
export DUSTPAN_DATABASE=sqlite:dustpan.db
```

The administrator signs in with the admin token. The key-encryption key
protects secret values and OpenTofu state. Keep both values outside the server's data directory.
The server keeps its data in `./dustpan.db` and `./blobs`, and listens on
`:8080`.

`serve` needs a key-encryption key and a database that you set. It has no
default for either, and it makes no key. Without a key, or without a
database, it stops before it opens the database. To use a key file instead
of `DUSTPAN_KEY_ENCRYPTION_KEY`, make a file with
`opendustpan key generate --out <path>` and set `--key-encryption-key-file`
(`DUSTPAN_KEY_ENCRYPTION_KEY_FILE`) to it. The command refuses a file that
exists, and it needs no `openssl`. A key file in the directory of the
database gives a warning at each start, because a backup of the data holds
that key too. An operator who loses the key loses every secret and every
OpenTofu state that the key encrypts, and nobody can recover them.

### Pulumi CLI

Start the server:

```sh
./bin/opendustpan serve
```

A new installation has no namespace. In a second shell with the same
`DUSTPAN_ADMIN_TOKEN`, create one. Then log in and run the first update:

```sh
./bin/opendustpan namespace create --server http://localhost:8080 acme
mkdir demo && cd demo
export PULUMI_ACCESS_TOKEN=$DUSTPAN_ADMIN_TOKEN
pulumi login http://localhost:8080
printf 'name: demo\nruntime: yaml\n' > Pulumi.yaml
pulumi stack init acme/demo/dev
pulumi up --yes --non-interactive
```

Secret config values go through the server, which encrypts them with the
key-encryption key:

```sh
pulumi config set --secret apiKey s3cr3t
pulumi up --yes --non-interactive
pulumi config get apiKey
```

### OpenTofu

OpenTofu refuses plain HTTP for service discovery, so the server needs TLS.
Make a test certificate and start the server with it:

```sh
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 30 \
  -subj /CN=localhost -addext subjectAltName=DNS:localhost \
  -keyout key.pem -out cert.pem
./bin/opendustpan serve --tls-cert cert.pem --tls-key key.pem
```

A new installation has no namespace. In a second shell with the same
`DUSTPAN_ADMIN_TOKEN`, create one:

```sh
./bin/opendustpan namespace create --server https://localhost:8080 --ca-file cert.pem acme
```

In a directory of its own, a `main.tf` with a `cloud` block names the server,
the namespace and the workspace:

```hcl
terraform {
  cloud {
    hostname     = "localhost:8080"
    organization = "acme"
    workspaces {
      name = "web"
    }
  }
}
```

Give OpenTofu the token and the certificate, then run it. Replace
`<path to cert.pem>` with the full path of the certificate file.

```sh
printf 'credentials "localhost:8080" {\n  token = "%s"\n}\n' "$DUSTPAN_ADMIN_TOKEN" > dev.tfrc
export TF_CLI_CONFIG_FILE=$PWD/dev.tfrc SSL_CERT_FILE=<path to cert.pem>
tofu init
tofu apply
```

`SSL_CERT_FILE` works on Linux. On other systems, use a certificate that the
system trusts. For a host name without a port, `TF_TOKEN_<host>` can hold the
token instead of a credentials file. The [TFE-compatible API
notes](docs/tfe-api.md) list the endpoints, the `remote` backend with a
workspace prefix, and the differences from TFE.

A refused `tofu state push -force` of another lineage keeps the workspace
lock. The error names the command that clears it. Run
`tofu force-unlock <namespace>/<workspace>` to clear the lock. Do not push
the same file again, because its lineage is still different. Run `tofu apply`,
or push a state file that has the lineage of the workspace.

### Web console

The web console is a separate repository,
[OpenDustpan/console](https://github.com/OpenDustpan/console). Build it and
let the server host it in the same process with
`opendustpan serve --console-dir <console>/dist`. Sign in with the value of
`DUSTPAN_ADMIN_TOKEN`.

## Demo

`make demo` runs a local demo. You need Git, Go, Node and npm. You need
no Pulumi CLI and no OpenTofu CLI.

The command builds the web console, starts `opendustpan dev --demo` on
`http://127.0.0.1:8080`, and seeds two namespaces with demo stacks and update
history. It then reads one route that the console calls. A
404 on that route means that the console at `CONSOLE_REF` calls a route
that this server does not serve, and the command stops. It prints the URL, the
user name and the password of the demo person, and the sign-in token. Sign in
with the user name and the password, or use the token on the sign-in page.

The server uses port 8080 by default. Set another port with
`make demo DEMO_PORT=<port>`.

Ctrl-C stops the server and deletes its temporary directory.

## What works

The end-to-end suite drives each version below against the server, on SQLite
and on PostgreSQL, on Linux in CI. `latest` is the newest release on the day
of the run.

PostgreSQL 18 is the minimum supported version. CI runs the store tests and
the end-to-end suite on PostgreSQL 18 only. An earlier version is not tested,
and the query plans that the store tests check can differ on it.

| Tool | Tested versions | What works | What does not work yet |
| --- | --- | --- | --- |
| Pulumi CLI | 3.200.0, 3.250.0, latest | `login`, `up`, `preview`, `refresh`, `destroy`, `cancel`, `stack export`, `stack import`, config secrets, `stack rename`, `stack rm`, `stack tag set` | Pulumi Cloud features outside the state backend |
| OpenTofu | 1.11.14, 1.12.7, latest | `init`, `plan`, `apply`, `output`, `state pull`, `state push`, `force-unlock`, `workspace new`, `workspace list`, `workspace select`, `workspace delete` (remote backend), `destroy` | Remote runs, `tofu login`, workspace tags, workspace rename |

The list of tested versions is in [`e2e/tool.go`](e2e/tool.go).

The lowest tested version of each tool is the lowest supported version:
Pulumi CLI 3.200.0 and OpenTofu 1.11.14. An older version may work, but no
test drives it. Use a supported version, or a later one.

## What the server stores

| Data | Where | Protection |
| --- | --- | --- |
| Namespaces, stacks, workspaces, locks, history | The SQL database: a SQLite file, or PostgreSQL | None of its own. Protect the database. |
| Pulumi checkpoints and OpenTofu state | The blob store: a directory, or an S3-compatible bucket | The server encrypts OpenTofu state with the key-encryption key. |
| Data keys of Pulumi secret config values | The SQL database | Wrapped with the key-encryption key. The encrypted values stay in your stack configuration. |

The database and the backups hold no copy of the key-encryption key. It
lives in `DUSTPAN_KEY_ENCRYPTION_KEY` or in a key file that only its owner
can read.

The server never phones home. No data about your installation, its users or
its use goes to the project, to a vendor or to any other party that you did
not name. This is also true with no configuration, and no setting, also no opt-in
setting, turns it on. In OpenDustpan Cloud, the project is the operator:
its metrics, logs and traces of the installations that it runs stay in its
own systems.
Metrics and logs are for you, and they stay. You scrape the Prometheus
metrics, and the server sends logs only to the log outputs that you
configure. It connects out only to the PostgreSQL
database and the S3-compatible storage that you configure.
`opendustpan namespace create`, `opendustpan backup`, `opendustpan backup
check`, `opendustpan upgrade check` and `opendustpan restore` call only the
server that `--server` names. `opendustpan health` calls only the listen
address of the local server.

### What to back up

1. **The key-encryption key.** A lost key means lost secrets and lost
   OpenTofu state. The server refuses to start without a key, and with a key
   that does not open the wrapped keys in the database. Keep a copy of the
   key in a different place from the backups. The default key file sits next
   to the SQLite database, so a copy of that directory also copies the key.
2. **A backup of the installation.** `opendustpan backup` writes the SQL data
   and the blobs to a backup target. A backup holds OpenTofu state that the server encrypted, so protect the target.

The [backup guide](docs/guides/backup-restore-upgrade.md) shows each command.

## Move in and run it

The test for this step passes for both tools, on each version in the
matrix, on SQLite and on PostgreSQL. The upgrade steps that need a previous
tag run from the first tag on (see the [roadmap](docs/roadmap.md)).

Move state in with the [import guide](docs/guides/import.md). It moves
state and secrets from a Pulumi service backend, a Pulumi file backend,
an OpenTofu backend, or a TFE-compatible API. The source stays unchanged.

List OpenTofu state versions with
`GET /api/v2/state-versions` and restore one with
`POST /api/v2/state-versions/{state_version_id}/actions/restore`.
Restore a Pulumi checkpoint with
`POST /api/stacks/{namespace}/{project}/{stack}/restore/{version}`.
The restore answers 400 when the stored checkpoint of that version is not a deployment that the Pulumi CLI can read. It writes no new version.

`opendustpan backup`, `opendustpan backup check`, `opendustpan restore`,
`opendustpan upgrade check`, `opendustpan store check` and
`opendustpan namespace create` are API clients. Each takes `--server` and `--ca-file`, and reads the token from
`DUSTPAN_ADMIN_TOKEN`. `opendustpan restore` runs through the API in
maintenance mode, with `--backup`, `--check-only` and `--force`. The
[backup guide](docs/guides/backup-restore-upgrade.md) shows each command
and its routes: `/api/server/backups`, `/api/server/restores`, and the
upgrade check route.

The server refuses to start without a key-encryption key. Set it in
`DUSTPAN_KEY_ENCRYPTION_KEY`. A backup never holds the key, and a
restore with a wrong key stops before it writes.

Set storage with `--backup-target` and `--blob-store`, including
S3-compatible storage with `--s3-endpoint` and `--s3-region` (see
[Use S3-compatible object storage](docs/guides/s3-blobs.md)). Set request
logging with `--log-level` and `--log-format`. Check readiness at
`GET /health/ready` and scrape metrics at `GET /api/server/metrics`
(see [Observe the server](docs/guides/observe.md)).

## When not to use OpenDustpan

- **You need it in production now.** It is pre-alpha and has no release.
  Formats and APIs can change.
- **You need more than one site.** One installation runs several instances on
  one database and one object store (see
  [High availability](docs/guides/high-availability.md)). It has no
  replication between installations and no active-active across sites.
- **You need users, teams or roles.** Every token signs in as an
  administrator. Single sign-on and scoped tokens do not exist yet.
- **You need remote runs.** OpenDustpan stores state. It does not run
  `pulumi up` or `tofu apply` for you.
- **You need a full audit log, policy checks or drift detection.** The
  audit log records secret reads and overrides only, and can send a copy
  of each entry to a file, syslog, a webhook or object storage.
  Planned: every change and an export. Policy checks and drift
  detection do not exist yet.
- **Your file or S3 backend is enough.** A plain backend has fewer moving
  parts. OpenDustpan adds a server, a database and a key to look after.

## Leaving OpenDustpan

Your state stays yours. The standard CLI commands read it out, and the
end-to-end suite tests them against the server:

```sh
pulumi stack export --file dev.json
pulumi stack export --version 1 --file dev-v1.json
tofu state pull > web.tfstate
```

The [migrate-out guide](docs/guides/migrate-out.md) moves each stack and
each workspace to another backend, step by step, and marks the steps that
the end-to-end suite does not test yet. It moves the latest state of each
unit. A way out with the full history of each unit does not exist yet.

## Governance

The project owner, [@DwaineSaunderson](https://github.com/DwaineSaunderson),
maintains OpenDustpan and decides what merges. Agent sessions write most of
the code. Each commit that an agent writes names it in a `Co-Authored-By`
trailer.

## Documentation

How-to guides:

- [Run a trial server](docs/guides/dev-server.md) with `opendustpan dev`: no
  setup, a one-time sign-in link and the settings of the Pulumi CLI and
  OpenTofu.
- [Check an install](docs/guides/doctor.md) with `opendustpan doctor`: the
  key, the database, the token, the backups and the running server.
- [Import existing state](docs/guides/import.md) from a Pulumi service
  backend, a file backend, or an OpenTofu backend.
- [Migrate out](docs/guides/migrate-out.md) to another backend.
- [Back up, restore and upgrade](docs/guides/backup-restore-upgrade.md) the
  installation.
- [Use S3-compatible object storage](docs/guides/s3-blobs.md) for state
  content.
- [Observe the server](docs/guides/observe.md): probes, metrics and request
  logs.
- [Read the audit log](docs/guides/audit-log.md): the entries of a
  namespace and the check of their chain.
- [Deleted units](docs/guides/deleted-units.md): list, restore and purge
  the units that a delete keeps until its retention period ends.
- [Manual locks](docs/guides/manual-locks.md): stop OpenTofu locks and Pulumi updates on a
  stack, a workspace or a collection, with a reason, and end the lock.
- [Approval defaults and approval rules](docs/guides/approval-rules.md): turn on
  approval before apply for a namespace, and store named approval rules.
- [Gates and access limits on state writes](docs/guides/state-write-gates.md):
  what the server checks before it lets an OpenTofu lock or a restore go on,
  and what a refusal looks like.
- [Set and read context](docs/guides/context.md): the owners, the on-call
  text and the other context of a namespace and of a unit.
- [Check a workspace for drift](docs/guides/drift-checks.md): start a drift check
  of an OpenTofu workspace and read its latest drift result.
- [Run triggers](docs/guides/run-triggers.md): save a trigger on a stack or a
  workspace, fire it by hand, and see what the server checks.
- [The planner role](docs/guides/planner-role.md): what a planner can do, what
  it cannot do, and the limit of the role on a workstation.
- [Principals and groups](docs/guides/principals-and-groups.md): create, change,
  disable and delete principals, and manage the members of a group.
- [Roles and grants](docs/guides/roles-and-grants.md): the built-in roles, custom
  roles, and the grants that give a role to a principal or a group.
- [Access review](docs/guides/access-review.md): read the grants of an installation,
  a namespace, a unit or a principal, and save the review.
- [Access tokens](docs/guides/access-tokens.md): personal, service and principal
  tokens, rotation, inactivity, expiry warnings and the token policy.
- [Collections and placement rules](docs/guides/collections-and-placement.md): make
  a collection, move a unit into it, and write the rules that place new stacks.
- [IP allowlists](docs/guides/ip-allowlists.md): limit the client addresses of a
  namespace, read the effective state, and recover from a lock-out.
- [Output sharing](docs/guides/output-sharing.md): the actions that read
  outputs and manage sharing, and the setting that decides the sharing rule of
  a new stack or workspace.
- [Save and open a view of the unit list](docs/guides/saved-views.md): store
  the labels and references of a unit list under a name, and open the view
  again.
- [Connect GitHub](docs/guides/connect-github.md): register one GitHub App for
  the installation, give its installations to namespaces, and check a
  connection.
- [Publish a provider to the private registry](docs/guides/private-registry.md): register a
  signing key, publish a signed provider version, and install it with `tofu init`.
- [Connect a webhook and send a test message](docs/guides/notification-destinations.md):
  make a notification destination, check its signature, and send a test
  message.
- [Send messages to a Slack channel](docs/guides/slack-notifications.md):
  connect a Slack incoming webhook to a namespace and send a test message.
- [Architecture and sizing](docs/guides/architecture-and-sizing.md): the
  layout of an installation, and how to measure its size.
- [Install with Helm](docs/guides/kubernetes.md): the Helm chart, its Secrets, storage, upgrade and rollback steps.
- [Run several instances](docs/guides/several-instances.md): the settings
  that each instance shares, for the target design.
- [Console sign-in](docs/guides/console-sign-in.md) with an OpenID Connect
  provider.
- [Sign-in with an LDAP directory](docs/guides/ldap-sign-in.md): the
  settings, the TLS rules, the subject and the groups of an LDAP provider.
- [CAS sign-in](docs/guides/cas-sign-in.md): the checks of a sign-in through
  a CAS server.

Reference:

- [Code rules](docs/reference/code-rules.md): the rules that the server
  applies and that no grant, setting or administrator can change.
- [Configuration](docs/reference/configuration.md): every flag and
  environment variable.
- [Journaling](docs/reference/journaling.md): the setting, the recovery
  steps and the bounds of a journaled update.
- [Journal limits](docs/reference/journal-limits.md): the size and time limits
  of a journaled update.
- [Write attempts](docs/reference/write-attempts.md): the attempt deadline of
  the writes that store a blob, and the grace period of the sweep.
- [Placement rules API](docs/reference/placement-rules-api.md): the rules that
  choose the collection of a new stack, their patterns and their routes.
- [Gate configuration API](docs/reference/gate-config-api.md): the routes that store,
  read and delete gates and access limits.
- [Audit checkpoint layout](docs/reference/audit-checkpoint-layout.md): the
  keys and the bodies of the signed audit checkpoints in the blob store.
- [Preferences API](docs/reference/preferences-api.md): the routes that read
  and change the preferences of a person.
- [Git host account links API](docs/reference/git-host-links-api.md): the routes that
  link an account of a Git host to a principal.
- [Passkeys API](docs/reference/passkeys-api.md): the routes that add, list and
  delete the passkeys of a person.
- [Settings API](docs/reference/settings-api.md): the routes that read the
  settings of the installation and of a namespace, with the source of each
  value, and change them.
- [Error codes of a 403 answer](docs/reference/forbidden-error-codes.md): the
  stable codes that tell a missing permission, the IP allowlist and the session
  checks apart.
- [Registry API](docs/reference/registry-api.md): the routes that publish a provider,
  manage signing keys, and the routes that OpenTofu reads.
- [Notification destinations API](docs/reference/notification-destinations-api.md):
  the routes, fields and errors of notification destinations.
- [Languages and digits](docs/reference/languages-and-digits.md): the
  languages, the `digits` setting and the plural forms.
- [Proxy rules](docs/reference/proxy-rules.md): the format, the variables, the
  limits and the refusals of a proxy rule set of the job proxy.
- [TFE-compatible API](docs/tfe-api.md): the endpoints that OpenTofu uses.
- [Glossary](GLOSSARY.md): the words this project uses.

Explanation:

- [Security model](docs/explanation/security-model.md): what the server
  protects, how, and what it does not protect.
- [Concepts](#concepts) below.
- [Roadmap](docs/roadmap.md): the planned work and the tests for each step.
- [Architecture decisions](docs/adr/) and [open proposals](docs/rfc/).

## Concepts

- **Namespace**: the unit of tenancy, what each tool calls an organization.
  Each namespace has one primary installation.
- **Stacks and workspaces**: each tool keeps its own model of deployable units
  and their state (Pulumi stacks, OpenTofu workspaces). They share
  namespaces, identity, storage and locking rules, not data models.
- **History and restore**: each save of state makes a new version. A restore
  copies an earlier version into a new version, and that new version becomes
  current. The versions after the restored one stay in the history, and you
  can restore them too. A restore changes no infrastructure: the next
  `pulumi up` or `tofu apply` compares your code with the restored state.
  Only the holder of the lock can restore a locked OpenTofu workspace.
- **Storage**: a single node uses SQLite and local disk. Larger installations
  use PostgreSQL and object storage. Each storage backend is an adapter that
  you can replace.

## Container image

The `Dockerfile` builds one image for linux/amd64 and linux/arm64. The image
holds the server and the web console. The server runs as a non-root user and
keeps its data in `/data`.

Build the image. `CONSOLE_URL` and `CONSOLE_SHA256` name the console release
tarball and its checksum. No console release exists yet, so pass a release
tarball or build with a local test tarball as `scripts/smoke-image.sh` does.

```sh
docker build -t opendustpan:dev \
  --build-arg VERSION="$(git describe --tags --always --dirty)" \
  --build-arg CONSOLE_URL=<tarball URL> \
  --build-arg CONSOLE_SHA256=<tarball SHA-256> .
```

The image fails closed. Its command is `serve`, and `serve` stops without a
key-encryption key, so `docker run` with no key exits with the key error. To
try OpenDustpan in a container, run `dev`. It keeps its data in the directory
`dev` of the volume `/data` (the image sets `DUSTPAN_DATA_DIR=/data`), and
`--listen 0.0.0.0:8080` lets the port mapping reach it:

```sh
docker run -p 127.0.0.1:8080:8080 opendustpan:dev dev --listen 0.0.0.0:8080
```

Run it for production. Mount a data directory and pass the two secrets. The container user
must be able to write to the data directory. Start the server first, then
create a namespace through the API before the first login, as above.

```sh
mkdir -p data
export DUSTPAN_ADMIN_TOKEN=$(openssl rand -hex 32)
export DUSTPAN_KEY_ENCRYPTION_KEY=$(openssl rand -base64 32)
docker run -d --name dustpan -p 127.0.0.1:8080:8080 -v ./data:/data \
  -e DUSTPAN_ADMIN_TOKEN -e DUSTPAN_KEY_ENCRYPTION_KEY opendustpan:dev
docker exec -e DUSTPAN_ADMIN_TOKEN dustpan /opendustpan namespace create --server http://localhost:8080 acme
```

For the reference container deployment, mount a key file or pass the
environment variable. A mounted file outside the data volume needs the file
flag to point at it:

```sh
docker run -d --name dustpan -p 127.0.0.1:8080:8080 -v ./data:/data \
  -v ./dustpan.key:/run/secrets/dustpan.key:ro \
  -e DUSTPAN_ADMIN_TOKEN -e DUSTPAN_KEY_ENCRYPTION_KEY_FILE=/run/secrets/dustpan.key \
  opendustpan:dev
```

A key file next to the data does not protect a backup of that data. A backup
without the key cannot restore secrets or OpenTofu state. Keep the key apart
from the data and the backups.

`make image-smoke` builds the image and runs the local smoke test. The test
starts the container over TLS, checks the API (`/api/capabilities`) and the
console page, waits until the container reports `healthy`, and checks that
the process user is not root. Behind a TLS-intercepting proxy, set
`EXTRA_CA_FILE` to a PEM bundle file so the build trusts it. It is not part of
`make check`.

The image defines a `HEALTHCHECK` that probes `GET /health/ready` every 30
seconds with `opendustpan health`. The command reads the same listen address
and TLS settings as `serve` (`--listen` / `DUSTPAN_LISTEN`, `--tls-cert` /
`DUSTPAN_TLS_CERT`) and exits 0 on a 2xx answer. When the server uses TLS,
set `DUSTPAN_TLS_CERT` (with `DUSTPAN_TLS_KEY`) in the container environment,
not only as a serve flag, so the check probes over HTTPS. When `--listen`
differs from `:8080`, override the check. Read the status with
`docker inspect --format '{{.State.Health.Status}}' dustpan`. Kubernetes
ignores `HEALTHCHECK` and uses its own probes.

## Configuration

The [configuration reference](docs/reference/configuration.md) lists every
flag and environment variable of `opendustpan serve`.

## Development

`make check` runs every gate CI runs: `go mod tidy -diff`, `sqlc diff`, strict
golangci-lint, the module age check (no module version is younger than 14
days), NilAway, govulncheck, Semgrep, Trivy, TruffleHog, the license
check, the docs lint and the tests with the race detector. goleak fails a test
package that leaves a goroutine running. The `Makefile` lists the tool
versions to install. NilAway and govulncheck need no install. Set
`DUSTPAN_TEST_POSTGRES_URL` to include the PostgreSQL suite. `make test-s3`
runs the S3 blob suite. It needs `DUSTPAN_TEST_S3_ENDPOINT`,
`DUSTPAN_TEST_S3_BUCKET`, `DUSTPAN_TEST_S3_ACCESS_KEY` and
`DUSTPAN_TEST_S3_SECRET_KEY`. `make e2e` drives real CLI versions against the
server. It downloads them first, as `e2e/doc.go` describes. `E2E_BLOBS=s3`
with the four S3 variables drives the runs with the live blobs on the
S3-compatible server.

With the race detector, one run of `pkg/store/sqlite` or `pkg/store/postgres`
can take more than 10 minutes, the default limit of `go test`. On a
4-core CI runner, `pkg/store/postgres` alone took more than 20 minutes. `make
test` sets `-timeout 45m` (set `TEST_TIMEOUT` to change it). The CI targets
keep their own limits: `make test-ci-other` sets `-timeout 20m`, and
`make test-ci-store` sets `-timeout 30m`. When
you run a store package with `go test -race` directly, pass `-timeout 60m`.

`make checks-agent` syncs the current branch with `main`, then runs
`make generate`, `make check`, `make e2e`, and a TruffleHog scan of the branch
commits. It starts a PostgreSQL container when `DUSTPAN_TEST_POSTGRES_URL` is
not set, and it deletes the container and the temporary files it made.
`SYNC` selects how the branch takes in `main`: `merge` (default), `rebase`,
or `none`. `SKIP` lists steps to skip, for example `SKIP="e2e trufflehog"`.
`CLEAN_CACHE=1` also deletes the e2e CLI download cache.

Start with [CONTRIBUTING.md](CONTRIBUTING.md), then `CLAUDE.md` (rules for
agents and contributors) and `docs/adr/` (why things are the way they are).

[SUPPORT.md](SUPPORT.md) says where to ask a question or report a bug.
[SECURITY.md](SECURITY.md) says how to report a vulnerability in private. All
contributors follow the [code of conduct](CODE_OF_CONDUCT.md).

## License

OpenDustpan is under the [Apache License 2.0](LICENSE).

Each release archive holds the license files of the dependencies that its
binary links, in `third_party_licenses/`. The container image holds them in
`/licenses`. `scripts/third-party-licenses.sh` collects them.

OpenDustpan is an independent project, built to work with the tools above. It
is not affiliated with or endorsed by Pulumi Corporation, the OpenTofu project
or HashiCorp.
