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

No release exists yet, so build the server from source. You need Go 1.27.2 or
later, OpenSSL, and the Pulumi CLI or OpenTofu.

```sh
go build -o bin/opendustpan ./cmd/opendustpan
export DUSTPAN_ADMIN_TOKEN=$(openssl rand -hex 32)
export DUSTPAN_KEY_ENCRYPTION_KEY=$(openssl rand -base64 32)
```

The administrator signs in with the admin token. The key-encryption key
protects secret values. Keep both values outside the server's data directory.
The server keeps its data in `./dustpan.db` and `./blobs`, and listens on
`:8080`.

Without `DUSTPAN_KEY_ENCRYPTION_KEY`, the server uses a key file. The default
file is `dustpan.key` next to the database. On a terminal, `serve` asks for an
explicit yes before it makes the file. Without a terminal, set
`--generate-key-encryption-key`
(`DUSTPAN_GENERATE_KEY_ENCRYPTION_KEY=true`) to make the file with no prompt.
The server never makes a key silently. An operator who loses the key file
loses every secret that the key encrypts, and nobody can recover them. The
default key file is in the data directory, so for production set
`--key-encryption-key-file` to a path outside it, or set
`DUSTPAN_KEY_ENCRYPTION_KEY`.

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

### Web console

The web console is a separate repository,
[OpenDustpan/console](https://github.com/OpenDustpan/console). Build it and
let the server host it in the same process with
`opendustpan serve --console-dir <console>/dist`. Sign in with the value of
`DUSTPAN_ADMIN_TOKEN`.

## What works

The end-to-end suite drives each version below against the server, on SQLite
and on PostgreSQL, on Linux in CI. `latest` is the newest release on the day
of the run.

| Tool | Tested versions | What works | What does not work yet |
| --- | --- | --- | --- |
| Pulumi CLI | 3.200.0, 3.250.0, latest | `login`, `up`, `preview`, `refresh`, `destroy`, `cancel`, `stack export`, `stack import`, config secrets, `stack rename`, `stack rm`, `stack tag set` | Pulumi Cloud features outside the state backend |
| OpenTofu | 1.11.14, 1.12.7, latest | `init`, `plan`, `apply`, `output`, `state pull`, `state push`, `force-unlock`, `workspace new`, `workspace list`, `workspace select`, `destroy` | Remote runs, `tofu login`, workspace tags, workspace rename and delete |

The list of tested versions is in [`e2e/tool.go`](e2e/tool.go).

## What the server stores

| Data | Where | Protection |
| --- | --- | --- |
| Namespaces, stacks, workspaces, locks, history | The SQL database: a SQLite file, or PostgreSQL | None of its own. Protect the database. |
| Pulumi checkpoints and OpenTofu state | The blob store: a directory, or an S3-compatible bucket | OpenTofu state is plain text. |
| Data keys of Pulumi secret config values | The SQL database | Wrapped with the key-encryption key. The encrypted values stay in your stack configuration. |

The database and the backups hold no copy of the key-encryption key. It
lives in `DUSTPAN_KEY_ENCRYPTION_KEY` or in a key file that only its owner
can read.

The server sends no telemetry. It connects out only to the PostgreSQL
database and the S3-compatible storage that you configure.
`opendustpan namespace create`, `opendustpan backup`, `opendustpan backup
check` and `opendustpan upgrade check` call only the server that `--server`
names. `opendustpan health` calls only the listen address of the local
server.

### What to back up

1. **The key-encryption key.** A lost key means lost secrets. The server
   refuses to start without a key, and with a key that does not open the
   wrapped keys in the database. Keep a copy of the key in a different place
   from the backups. The default key file sits next to the SQLite database,
   so a copy of that directory also copies the key.
2. **A backup of the installation.** `opendustpan backup` writes the SQL data
   and the blobs to a backup target. A backup holds OpenTofu state in plain
   text, so protect the target.

The [backup guide](docs/guides/backup-restore-upgrade.md) shows each command.

## When not to use OpenDustpan

- **You need it in production now.** It is pre-alpha and has no release.
  Formats and APIs can change.
- **You need more than one node.** The server runs on one node. High
  availability is a plan, not a feature.
- **You need users, teams or roles.** Every token signs in as an
  administrator. Single sign-on and scoped tokens do not exist yet.
- **You need remote runs.** OpenDustpan stores state. It does not run
  `pulumi up` or `tofu apply` for you.
- **You need an audit log, policy checks or drift detection.** These do not
  exist yet.
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

- [Import existing state](docs/guides/import.md) from a Pulumi service
  backend, a file backend, or an OpenTofu backend.
- [Migrate out](docs/guides/migrate-out.md) to another backend.
- [Back up, restore and upgrade](docs/guides/backup-restore-upgrade.md) the
  installation.
- [Use S3-compatible object storage](docs/guides/s3-blobs.md) for state
  content.
- [Observe the server](docs/guides/observe.md): probes, metrics and request
  logs.

Reference:

- [Configuration](docs/reference/configuration.md): every flag and
  environment variable.
- [TFE-compatible API](docs/tfe-api.md): the endpoints that OpenTofu uses.
- [Glossary](GLOSSARY.md): the words this project uses.

Explanation:

- [Security model](docs/explanation/security-model.md): what the server
  protects, how, and what it does not protect.
- [Concepts](#concepts) below.
- [Roadmap](docs/roadmap.md): the milestones and their exit tests.
- [Architecture decisions](docs/adr/) and [open proposals](docs/rfc/).

## Concepts

- **Namespace**: the unit of tenancy, what each tool calls an organization.
  Each namespace has one home region.
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

Run it. Mount a data directory and pass the two secrets. The container user
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
golangci-lint, NilAway, govulncheck, Semgrep, Trivy, TruffleHog, the license
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
