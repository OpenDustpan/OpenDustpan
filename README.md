# OpenDustpan

Self-hosted state for every stack. OpenDustpan is an open-source backend for
the Pulumi CLI: point `pulumi login` at your own server and keep your stacks,
state and update history on infrastructure you control.

OpenDustpan is an independent project. It works with the Pulumi CLI and is not
affiliated with or endorsed by Pulumi Corporation.

> **Status: pre-alpha.** The architecture, storage adapters and the first API
> endpoints exist. `pulumi up` does not work yet: updates, checkpoints and
> leases are next.

## Try it

Requires Go 1.27.1+.

```sh
go mod tidy                         # first run: fetches and verifies module checksums
go build -o bin/opendustpan ./cmd/opendustpan

./bin/opendustpan namespace create acme
export DUSTPAN_ADMIN_TOKEN=$(openssl rand -hex 32)
./bin/opendustpan serve             # SQLite in ./dustpan.db, API on :8080
```

The web console is a separate repository,
[OpenDustpan/console](https://github.com/OpenDustpan/console). Run it in
development against this server, or build it and let the server host it in one
process: `opendustpan serve --console-dir <console>/dist`. Sign in with the
value of `DUSTPAN_ADMIN_TOKEN`.

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
`DUSTPAN_TEST_POSTGRES_URL` to include the PostgreSQL suite.

Start with `CLAUDE.md` (rules for agents and contributors) and `docs/adr/`
(why things are the way they are).

## License

Not yet chosen.
