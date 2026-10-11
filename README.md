# OpenDustpan

Self-hosted state for your infrastructure as code. OpenDustpan is an
open-source backend for infrastructure-as-code tools: point your CLI at your
own server and keep state, history, locks and secrets on infrastructure you
control, with one set of organizations, access rules and storage for every
tool your teams use.

## Supported tools

Each tool talks to OpenDustpan through the remote-backend protocol it already
speaks, so the CLI needs no plugin.

| Tool | Protocol | Status |
| --- | --- | --- |
| Pulumi CLI | Pulumi Cloud-compatible API (`pulumi login <url>`) | Full deployments work on one node. Login, stacks, `up`, `preview`, `refresh`, `destroy`, `cancel`, `stack export`, `stack import`, config secrets, `stack rename`, `stack rm` and `stack tag set` work. |
| OpenTofu | TFE-compatible remote API (`cloud` block) | Works with OpenTofu on one node. State, locks, `init`, `plan`, `apply`, `output`, `state pull`, `state push`, `force-unlock`, `workspace new`, `workspace list`, `workspace select` and `destroy` work. Remote runs, `tofu login`, workspace tags, rename and delete are out. |

More tools follow the same pattern (ADR 12). An end-to-end suite that drives
the real CLI checks every supported version of every tool.

OpenDustpan is an independent project, built to work with the tools above. It
is not affiliated with or endorsed by Pulumi Corporation, the OpenTofu project
or HashiCorp.

> **Status: pre-alpha.** The Pulumi CLI runs full deployments on one node,
> with SQLite or PostgreSQL. OpenDustpan works with OpenTofu as a remote
> backend on one node.

## Try it

Requires Go 1.27.2+, the Pulumi CLI and OpenTofu.

Build the server. Generate the two secrets it needs. The administrator
signs in with the admin token. The key-encryption key protects secret
config values. Both are random values. Store them outside the database.

```sh
go build -o bin/opendustpan ./cmd/opendustpan

export DUSTPAN_ADMIN_TOKEN=$(openssl rand -hex 32)
export DUSTPAN_KEY_ENCRYPTION_KEY=$(openssl rand -base64 32)
```

A lost key-encryption key means lost secrets. The server encrypts secret
values with this key. Without the key, the server refuses to start once
the database holds secret values. The server keeps no other copy of the key.

Create a namespace. Then start the server. A new installation has no
namespace. Create one before the first login (ADR 18).

```sh
./bin/opendustpan namespace create acme
./bin/opendustpan serve             # SQLite in ./dustpan.db, API on :8080
```

Point the Pulumi CLI at the server. The commands below create an empty
YAML project, create a stack and run the first update.

```sh
mkdir demo && cd demo
export PULUMI_ACCESS_TOKEN=$DUSTPAN_ADMIN_TOKEN
pulumi login http://localhost:8080
pulumi whoami

printf 'name: demo\nruntime: yaml\n' > Pulumi.yaml
pulumi stack init acme/demo/dev
pulumi up --yes --non-interactive
```

The server encrypts secret config values with the key-encryption key. The
commands below store a secret, deploy it and read it back.

```sh
pulumi config set --secret apiKey s3cr3t
pulumi up --yes --non-interactive
pulumi config get apiKey
```

The web console is a separate repository,
[OpenDustpan/console](https://github.com/OpenDustpan/console). Run it in
development against this server, or build it and let the server host it in one
process: `opendustpan serve --console-dir <console>/dist`. Sign in with the
value of `DUSTPAN_ADMIN_TOKEN`.

### OpenTofu

OpenDustpan works with OpenTofu through the TFE-compatible API. The steps
below store the state of one workspace on the server.

Create a namespace. Then start the server with TLS. OpenTofu refuses plain
HTTP for service discovery.

```sh
./bin/opendustpan namespace create acme
./bin/opendustpan serve --tls-cert cert.pem --tls-key key.pem
```

Point OpenTofu at the server. The `cloud` block names the host and the
workspace. The token comes from a `credentials` block or from
`TF_TOKEN_<host>`.

```hcl
terraform {
  cloud {
    hostname     = "dustpan.example"
    organization = "acme"
    workspaces {
      name = "web"
    }
  }
}
```

```hcl
credentials "dustpan.example" {
  token = "REDACTED"
}
```

Run `tofu init` to create the workspace. Run `tofu apply` to store state.
When a lock stays behind, release it with `tofu force-unlock`.

```sh
export TF_TOKEN_dustpan_example="REDACTED"
tofu init
tofu apply
tofu force-unlock acme/web
```

The `remote` backend with a workspace prefix covers many workspaces. With
the prefix `web-`, `tofu workspace new dev` creates the `web-dev`
workspace.

```hcl
terraform {
  backend "remote" {
    hostname     = "dustpan.example"
    organization = "acme"
    workspaces {
      prefix = "web-"
    }
  }
}
```

A TLS-terminating reverse proxy is a supported setup. The server never
trusts `X-Forwarded-*` headers. Set `--public-url` to the external base URL
so absolute URLs point at the proxy. The supported endpoints and the
differences from TFE are in `docs/tfe-api.md`.

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

Run it. Mount a data directory and pass the two secrets. The data directory
must be writable by the container user. Create a namespace before the first
login, as above.

```sh
mkdir -p data
export DUSTPAN_ADMIN_TOKEN=$(openssl rand -hex 32)
export DUSTPAN_KEY_ENCRYPTION_KEY=$(openssl rand -base64 32)
docker run -d --name dustpan -p 127.0.0.1:8080:8080 -v ./data:/data \
  -e DUSTPAN_ADMIN_TOKEN -e DUSTPAN_KEY_ENCRYPTION_KEY opendustpan:dev
docker exec dustpan /opendustpan namespace create --database sqlite:/data/dustpan.db acme
```

`make image-smoke` builds the image and runs the local smoke test. The test
starts the container over TLS, checks the API (`/api/capabilities`) and the
console page, waits until the container reports `healthy`, and checks that
the process user is not root. Behind a
TLS-intercepting proxy, set `EXTRA_CA_FILE` to a PEM bundle file so the build
trusts it. It is not part of `make check`.

The image defines a `HEALTHCHECK` that probes `GET /health/ready` every 30
seconds with `opendustpan health`. The command reads the same listen
address and TLS settings as `serve` (`--listen` / `DUSTPAN_LISTEN`,
`--tls-cert` / `DUSTPAN_TLS_CERT`) and exits 0 on a 2xx answer. When the
server uses TLS, set `DUSTPAN_TLS_CERT` (with `DUSTPAN_TLS_KEY`) in the
container environment, not only as a serve flag, so the check probes over
HTTPS. When `--listen` differs from `:8080`, override the check. Read the
status with `docker inspect --format '{{.State.Health.Status}}' dustpan`.
Kubernetes ignores `HEALTHCHECK` and uses its own probes.

## Concepts

- **Namespace**: the unit of tenancy, what each tool calls an organization.
  Every namespace is authoritative in one region (ADR 4, RFC 2).
- **Stacks and workspaces**: each tool keeps its own model of deployable units
  and their state (Pulumi stacks, OpenTofu workspaces). They share
  namespaces, identity, storage and locking rules, not data models.
- **History and restore**: each save of state makes a new version. A
  restore copies an earlier version into a new version, and that new version
  becomes current (ADR 28). The versions after the restored one stay in the
  history, and you can restore them too. A restore changes no
  infrastructure: the next `pulumi up` or `tofu apply` compares your code with
  the restored state. Only the holder of the lock can restore a locked
  OpenTofu workspace.
- **Storage**: A single node uses SQLite and local disk. Larger installs use
  PostgreSQL and object storage. Every storage backend is a swappable adapter
  (ADR 2).

## Guides

- [Backup, restore and upgrade](docs/guides/backup-restore-upgrade.md):
  back up the whole installation, check a backup, restore it into an empty
  installation, and upgrade the server.

## Configuration

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
| `--blob-dir` | `DUSTPAN_BLOB_DIR` | `blobs` next to the SQLite file, or `./blobs` for PostgreSQL |
| `--blob-store` | `DUSTPAN_BLOB_STORE` | `fs` (the directory in `--blob-dir`), or `s3://<bucket>[/<prefix>]` |
| `--s3-endpoint` | `DUSTPAN_S3_ENDPOINT` | (none: AWS) |
| `--s3-region` | `DUSTPAN_S3_REGION` | `us-east-1` |
| `--backup-target` | `DUSTPAN_BACKUP_TARGET` | (none: no backups), or `dir:/path` or `s3://<bucket>[/<prefix>]` |
| | `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN` | the S3 credentials. No flag takes them |
| | `DUSTPAN_ADMIN_TOKEN` | required when you set no token file, at least 32 bytes (comma-separated tokens sign in as distinct administrators: admin, admin-2, ...). The server clears it from its environment after the read |
| | `DUSTPAN_ADMIN_TOKEN_FILE` | file holding the tokens, one per line or comma-separated, readable only by its owner. When you set both, `DUSTPAN_ADMIN_TOKEN` wins |
| | `DUSTPAN_KEY_ENCRYPTION_KEY` | base64 of 32 random bytes (unset while the database holds no secrets) |
| | `DUSTPAN_KEY_ENCRYPTION_KEY_FILE` | file holding that value, readable only by its owner |

## Development

`make check` runs every gate CI runs: `go mod tidy -diff`, `sqlc diff`, strict
golangci-lint, NilAway, govulncheck, Semgrep and the tests with the race
detector. goleak fails a test package that leaves a goroutine running. The
`Makefile` lists the tool versions to install. NilAway and govulncheck need no
install. Set
`DUSTPAN_TEST_POSTGRES_URL` to include the PostgreSQL suite. `make e2e` drives
real CLI versions against the server. It downloads them first, as `e2e/doc.go`
describes.

Start with `CLAUDE.md` (rules for agents and contributors) and `docs/adr/`
(why things are the way they are).

## License

OpenDustpan is under the [Apache License 2.0](LICENSE).

Each release archive holds the license files of the dependencies that its
binary links, in `third_party_licenses/`. The container image holds them in
`/licenses`. `scripts/third-party-licenses.sh` collects them.
