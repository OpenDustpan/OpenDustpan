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

Requires Go 1.27.1+ and Node 22+.

```sh
go mod tidy                         # first run: fetches and verifies module checksums
go build -o bin/opendustpan ./cmd/opendustpan

./bin/opendustpan namespace create acme
export DUSTPAN_ADMIN_TOKEN=$(openssl rand -hex 32)
./bin/opendustpan serve             # SQLite in ./dustpan.db, API on :8080
```

Console, in another terminal (proxies `/api` to `:8080`):

```sh
cd web && npm ci && npm run dev
```

Sign in with the value of `DUSTPAN_ADMIN_TOKEN`.

To serve API and console from one process: `npm run build` in `web/`, then
`opendustpan serve --console-dir web/dist`.

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

`make check` runs every gate CI runs: strict golangci-lint, tests (with the
race detector), `sqlc diff`, and Prettier, ESLint, TypeScript and Vitest for the
console. Set `DUSTPAN_TEST_POSTGRES_URL` to include the PostgreSQL suite.

Start with `CLAUDE.md` (rules for agents and contributors) and `docs/adr/`
(why things are the way they are).

## License

Not yet chosen.
