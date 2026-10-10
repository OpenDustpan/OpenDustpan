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
| Pulumi CLI | Pulumi Cloud-compatible API (`pulumi login <url>`) | Login and creating, selecting and listing stacks work. Organizations (RFC 2), updates (`pulumi up`), secrets and state export are in progress (RFC 3). |
| OpenTofu | TFE-compatible remote API (`cloud` block) | Designed (RFC 1), not built yet. |

More tools follow the same pattern (ADR 12). Every supported version of every
tool is checked by an end-to-end suite that drives the real CLI.

OpenDustpan is an independent project, built to work with the tools above. It
is not affiliated with or endorsed by Pulumi Corporation, the OpenTofu project
or HashiCorp.

> **Status: pre-alpha.** The architecture, storage adapters and the first API
> endpoints exist. No tool can run a full deployment against it yet.

## Try it

Requires Go 1.27.1+.

```sh
go build -o bin/opendustpan ./cmd/opendustpan

./bin/opendustpan namespace create acme
export DUSTPAN_ADMIN_TOKEN=$(openssl rand -hex 32)
./bin/opendustpan serve             # SQLite in ./dustpan.db, API on :8080
```

Then, with the Pulumi CLI:

```sh
export PULUMI_ACCESS_TOKEN=$DUSTPAN_ADMIN_TOKEN
pulumi login http://localhost:8080
pulumi whoami
```

Inside a Pulumi project, `pulumi stack init acme/<project>/dev` creates a stack.

The web console is a separate repository,
[OpenDustpan/console](https://github.com/OpenDustpan/console). Run it in
development against this server, or build it and let the server host it in one
process: `opendustpan serve --console-dir <console>/dist`. Sign in with the
value of `DUSTPAN_ADMIN_TOKEN`.

## Concepts

- **Namespace**: the unit of tenancy, what each tool calls an organization.
  Every namespace is authoritative in one region (ADR 4, RFC 2).
- **Stacks and workspaces**: each tool keeps its own model of deployable units
  and their state (Pulumi stacks, OpenTofu workspaces). They share
  namespaces, identity, storage and locking rules, not data models.
- **Storage**: A single node uses SQLite and local disk. Larger installs use
  PostgreSQL and object storage. Every storage backend is a swappable adapter
  (ADR 2).

## Configuration

| Flag | Environment | Default |
| --- | --- | --- |
| `--database` | `DUSTPAN_DATABASE` | `sqlite:dustpan.db` (or `postgres://...`) |
| `--region` | `DUSTPAN_REGION` | `local` |
| `--listen` | `DUSTPAN_LISTEN` | `:8080` |
| `--console-dir` | `DUSTPAN_CONSOLE_DIR` | (none) |
| `--allowed-origins` | `DUSTPAN_ALLOWED_ORIGINS` | (none) |
| | `DUSTPAN_ADMIN_TOKEN` | required, at least 32 bytes |

## Development

`make check` runs every gate CI runs: `go mod tidy -diff`, `sqlc diff`, strict
golangci-lint and the tests with the race detector. Set
`DUSTPAN_TEST_POSTGRES_URL` to include the PostgreSQL suite. `make e2e` drives
real CLI versions against the server. It downloads them first, as `e2e/doc.go`
describes.

Start with `CLAUDE.md` (rules for agents and contributors) and `docs/adr/`
(why things are the way they are).

## Licence

Not yet chosen.
