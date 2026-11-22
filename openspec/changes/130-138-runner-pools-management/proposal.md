---
version: 6
---

Issue: #3929. This change replaces the first cut of `130-138-runner-pools-management`. Main already serves the pool, join-token, drain and retire routes, and the guide `docs/guides/runner-pools.md` shows them with curl. The one missing part is a CLI command to list pools.

## Why

**Persona: namespace administrator.** A namespace administrator lists the runner pools of a namespace before they hand a join token to a team. Today the guide shows `curl` only, and the CLI has no `runner` command.

The path this change makes work:

1. **API.** The route `GET /api/dustpan/v1/namespaces/{namespace}/runner-pools` answers with the pools (unchanged).
2. **CLI.** `opendustpan runner pools list --namespace <ns>` calls that route and prints one line for each pool.
3. **Docs.** The guide shows the command next to the curl example.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The token lacks `namespace:read-runners` | 403 from the server; the command prints the server message and exits 1 | `opendustpan runner pools list --namespace <ns>` after an administrator grants the action |
| The namespace does not exist | 404 from the server; the command prints the message and exits 1 | `opendustpan namespace list` |

Evidence for the claims above:

- The list handler exists on main. Proof: internal/api/runnerpools/admin.go:117 (func Handler.listPools)
- The pool answer has the fields id, name, purpose, concurrency and isolation. Proof: internal/api/runnerpools/admin.go:19 (type poolJSON)
- The CLI registers commands as rows of one table. Proof: internal/cli/namespace_create.go:5 (func NamespaceCreateCommand)

## What Changes

- The NEW file `internal/cli/runner_pools.go` holds the command `opendustpan runner pools list`.
- The command table in `cmd/opendustpan/commands.go` and the reference table in `internal/cli/reference.go` get one row each.
- `docs/guides/runner-pools.md` shows the command in the section "List the pools".

Out of scope: any new route, field, error code, store change or API change; create, patch, delete, drain and retire commands.

## Usability

- The output is one line for each pool, with the columns name, id, purpose, concurrency and isolation, in exact form, sorted by name. The flag `--output json` prints the server answer unchanged.

## What main already does

- The pool routes, the stores, the error codes and the guide exist on main. (`internal/api/runnerpools/admin.go:117 (func Handler.listPools)`)

## ADR check

- ADR 130 (runner pools, owner-tagged): this change adds no behavior of a pool. Quote: "A pool has a purpose that never changes (owner)." The command only reads.
- ADR 17: "Give every feature a REST API" is met; the command is a client of the existing API.

## Open questions

None.
